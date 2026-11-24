// Package eventplan projects catalog schedules. It never runs SQL or infers
// actual execution duration, historical scheduler state, or query cost.
package eventplan

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata"

	"github.com/OlivierLB/Sulcata/internal/model"
)

type Match struct {
	Engine     string     `json:"engine"`
	ProgramID  string     `json:"programId"`
	TimeZone   string     `json:"timeZone"`
	Scheduler  string     `json:"scheduler"`
	Starts     int64      `json:"starts"`
	FirstAt    *time.Time `json:"firstAt"`
	LastAt     *time.Time `json:"lastAt"`
	PreviousAt *time.Time `json:"previousAt"`
}
type Issue struct {
	Engine    string `json:"engine"`
	ProgramID string `json:"programId"`
	Reason    string `json:"reason"`
}
type Bucket struct {
	From   time.Time        `json:"from"`
	To     time.Time        `json:"to"`
	Counts map[string]int64 `json:"counts"`
}
type Report struct {
	GeneratedAt     string    `json:"generatedAt"`
	From            time.Time `json:"from"`
	To              time.Time `json:"to"`
	LookbackMinutes int       `json:"lookbackMinutes"`
	Events          []Match   `json:"events"`
	Unknown         []Issue   `json:"unknown"`
	Excluded        []Issue   `json:"excluded"`
	Buckets         []Bucket  `json:"buckets"`
}

// Build uses half-open windows [from,to); ENDS itself is inclusive. Results
// are aggregated per event, so sub-minute schedules cannot flood the response.
func Build(engines map[string]*model.Engine, generatedAt string, from, to time.Time, lookback int) Report {
	r := Report{GeneratedAt: generatedAt, From: from, To: to, LookbackMinutes: lookback, Events: []Match{}, Unknown: []Issue{}, Excluded: []Issue{}, Buckets: []Bucket{}}
	step := time.Duration((int64(to.Sub(from).Seconds())+24*60-1)/(24*60)) * time.Minute
	if step < time.Minute {
		step = time.Minute
	}
	for at := from; at.Before(to); at = at.Add(step) {
		end := at.Add(step)
		if end.After(to) {
			end = to
		}
		r.Buckets = append(r.Buckets, Bucket{at, end, map[string]int64{}})
	}
	for engine, source := range engines {
		if source == nil {
			continue
		}
		for _, p := range source.Programs {
			if p == nil || p.Kind != "event" {
				continue
			}
			id := p.ID
			if id == "" {
				id = engine + ":event:" + p.Schema + "." + p.Name
			}
			issue := func(reason string) Issue { return Issue{engine, id, reason} }
			if p.Status != "ENABLED" {
				r.Excluded = append(r.Excluded, issue("Désactivé ou état non activé dans le dernier catalogue"))
				continue
			}
			if p.EventSchedule == nil {
				r.Unknown = append(r.Unknown, issue("Horaires structurés absents : actualise le catalogue"))
				continue
			}
			if p.EventSchedule.Scheduler == "OFF" || p.EventSchedule.Scheduler == "DISABLED" {
				r.Excluded = append(r.Excluded, issue("Planificateur désactivé au dernier export"))
				continue
			}
			if p.Source != nil && p.Source.Status == "reference" && p.EventSchedule.Type == "RECURRING" && p.EventSchedule.Starts == nil {
				r.Unknown = append(r.Unknown, issue("Départ absent ou calculé dans le script : l’heure dépend du déploiement sur l’environnement de référence"))
				continue
			}
			count, zone, err := counter(p.EventSchedule, from.Add(-time.Duration(lookback)*time.Minute), to)
			if err != nil {
				r.Unknown = append(r.Unknown, issue(err.Error()))
				continue
			}
			n, first, last := count(from, to)
			var previous *time.Time
			if lookback > 0 {
				_, _, previous = count(from.Add(-time.Duration(lookback)*time.Minute), from)
			}
			if n == 0 && previous == nil {
				continue
			}
			r.Events = append(r.Events, Match{engine, id, zone, p.EventSchedule.Scheduler, n, first, last, previous})
			for i := range r.Buckets {
				n, _, _ := count(r.Buckets[i].From, r.Buckets[i].To)
				if n > 0 {
					r.Buckets[i].Counts[id] = n
				}
			}
		}
	}
	sort.Slice(r.Events, func(i, j int) bool {
		a, b := r.Events[i], r.Events[j]
		if a.FirstAt == nil {
			if b.FirstAt != nil {
				return false
			}
		} else if b.FirstAt == nil {
			return true
		} else if !a.FirstAt.Equal(*b.FirstAt) {
			return a.FirstAt.Before(*b.FirstAt)
		}
		return a.ProgramID < b.ProgramID
	})
	for _, list := range [][]Issue{r.Unknown, r.Excluded} {
		sort.Slice(list, func(i, j int) bool { return list[i].ProgramID < list[j].ProgramID })
	}
	return r
}

type countFunc func(time.Time, time.Time) (int64, *time.Time, *time.Time)

func location(s *model.EventSchedule) (*time.Location, error) {
	zone := s.TimeZone
	if zone == "SYSTEM" {
		zone = s.SystemTimeZone
	}
	if len(zone) == 6 && (zone[0] == '+' || zone[0] == '-') && zone[3] == ':' {
		h, e1 := strconv.Atoi(zone[1:3])
		m, e2 := strconv.Atoi(zone[4:])
		if e1 == nil && e2 == nil && h <= 14 && m < 60 && (h < 14 || m == 0) {
			offset := (h*60 + m) * 60
			if zone[0] == '-' {
				offset = -offset
			}
			return time.FixedZone(zone, offset), nil
		}
	}
	// Abbreviations such as CEST do not identify DST rules reliably.
	if zone == "" || zone == "Local" {
		return nil, fmt.Errorf("Fuseau de l’événement absent")
	}
	loc, err := time.LoadLocation(zone)
	if err != nil {
		return nil, fmt.Errorf("Fuseau de l’événement non résolu : %s", zone)
	}
	return loc, nil
}
func wall(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), 0, time.UTC)
}
func parse(value *string) (time.Time, error) {
	if value == nil || *value == "" {
		return time.Time{}, fmt.Errorf("Date de lancement absente")
	}
	t, err := time.Parse("2006-01-02 15:04:05", *value)
	if err != nil {
		return time.Time{}, fmt.Errorf("Date du catalogue non interprétable")
	}
	return t, nil
}
func real(t time.Time, loc *time.Location) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), 0, loc).UTC()
}
func transition(loc *time.Location, from, to time.Time) bool {
	_, offset := from.In(loc).Zone()
	for at := from; at.Before(to); at = at.Add(30 * time.Minute) {
		_, o := at.In(loc).Zone()
		if o != offset {
			return true
		}
	}
	_, end := to.In(loc).Zone()
	return end != offset
}
func counter(s *model.EventSchedule, from, to time.Time) (countFunc, string, error) {
	loc, err := location(s)
	if err != nil {
		return nil, "", err
	}
	zone := s.TimeZone
	if zone == "SYSTEM" {
		zone += " (" + s.SystemTimeZone + ")"
	}
	firstDay, lastDay := from.In(loc), to.Add(-time.Second).In(loc)
	dayStart := time.Date(firstDay.Year(), firstDay.Month(), firstDay.Day(), 0, 0, 0, 0, loc)
	dayEnd := time.Date(lastDay.Year(), lastDay.Month(), lastDay.Day()+1, 0, 0, 0, 0, loc)
	// Even a window immediately after a DST jump can be affected by a launch
	// normalized out of the missing hour. Do not silently invent that behavior.
	if transition(loc, dayStart, dayEnd) {
		return nil, zone, fmt.Errorf("Jour avec changement d’heure : planification à vérifier")
	}
	if s.Type == "ONE TIME" {
		at, err := parse(s.ExecuteAt)
		if err != nil {
			return nil, zone, err
		}
		instant := real(at, loc)
		// A nonexistent wall time must not silently normalize to another hour.
		if !wall(instant.In(loc)).Equal(at) {
			return nil, zone, fmt.Errorf("Heure de lancement inexistante dans ce fuseau")
		}
		return func(a, b time.Time) (int64, *time.Time, *time.Time) {
			if !instant.Before(a) && instant.Before(b) {
				return 1, &instant, &instant
			}
			return 0, nil, nil
		}, zone, nil
	}
	if s.Type != "RECURRING" {
		return nil, zone, fmt.Errorf("Type de planification non reconnu")
	}
	start := s.Starts
	if start == nil {
		start = s.Created
	}
	anchor, err := parse(start)
	if err != nil {
		return nil, zone, err
	}
	var end *time.Time
	if s.Ends != nil {
		value, e := parse(s.Ends)
		if e != nil {
			return nil, zone, e
		}
		end = &value
	}
	if s.IntervalValue == nil || s.IntervalField == nil {
		return nil, zone, fmt.Errorf("Intervalle absent du catalogue")
	}
	quantity, err := strconv.ParseInt(*s.IntervalValue, 10, 64)
	if err != nil || quantity <= 0 || quantity > 1_000_000_000 {
		return nil, zone, fmt.Errorf("Intervalle non interprétable")
	}
	unit := *s.IntervalField
	seconds := map[string]int64{"SECOND": 1, "MINUTE": 60, "HOUR": 3600, "DAY": 86400, "WEEK": 604800}
	months := map[string]int64{"MONTH": 1, "QUARTER": 3, "YEAR": 12}
	// MySQL normalizes composite interval values; unsupported representations
	// are surfaced rather than guessed from the localized schedule label.
	if scale, ok := seconds[unit]; ok {
		step := quantity * scale
		return func(a, b time.Time) (int64, *time.Time, *time.Time) {
			lo, hi := wall(a.In(loc)), wall(b.In(loc))
			if end != nil && hi.After(end.Add(time.Second)) {
				hi = end.Add(time.Second)
			}
			if !lo.Before(hi) {
				return 0, nil, nil
			}
			diff := lo.Unix() - anchor.Unix()
			n := int64(0)
			if diff > 0 {
				n = (diff + step - 1) / step
			}
			first := time.Unix(anchor.Unix()+n*step, 0).UTC()
			if !first.Before(hi) {
				return 0, nil, nil
			}
			total := (hi.Unix()-1-first.Unix())/step + 1
			last := time.Unix(first.Unix()+(total-1)*step, 0).UTC()
			f, l := real(first, loc), real(last, loc)
			return total, &f, &l
		}, zone, nil
	}
	if scale, ok := months[unit]; ok {
		step := quantity * scale
		return func(a, b time.Time) (int64, *time.Time, *time.Time) {
			lo, hi := wall(a.In(loc)), wall(b.In(loc))
			if end != nil && hi.After(end.Add(time.Second)) {
				hi = end.Add(time.Second)
			}
			if !lo.Before(hi) {
				return 0, nil, nil
			}
			diff := int64((lo.Year()-anchor.Year())*12 + int(lo.Month()-anchor.Month()))
			n := int64(0)
			if diff > 0 {
				n = diff / step
			}
			var first, last *time.Time
			total := int64(0)
			for ; ; n++ {
				candidate := addMonths(anchor, n*step)
				if !candidate.Before(hi) {
					break
				}
				if candidate.Before(lo) {
					continue
				}
				instant := real(candidate, loc)
				if first == nil {
					f := instant
					first = &f
				}
				l := instant
				last = &l
				total++
			}
			return total, first, last
		}, zone, nil
	}
	return nil, zone, fmt.Errorf("Intervalle %s non pris en charge : vérifier la définition SQL", strings.ToUpper(unit))
}
func addMonths(anchor time.Time, months int64) time.Time {
	index := int64(anchor.Year())*12 + int64(anchor.Month()) - 1 + months
	year, month := int(index/12), time.Month(index%12+1)
	day := anchor.Day()
	max := time.Date(year, month+1, 0, 0, 0, 0, 0, time.UTC).Day()
	if day > max {
		day = max
	}
	return time.Date(year, month, day, anchor.Hour(), anchor.Minute(), anchor.Second(), 0, time.UTC)
}
