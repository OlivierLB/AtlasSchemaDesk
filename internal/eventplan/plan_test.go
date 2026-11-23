package eventplan

import (
	"github.com/OlivierLB/AtlasSchemaDesk/internal/model"
	"testing"
	"time"
)

func str(value string) *string { return &value }
func instant(value string) time.Time {
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		panic(err)
	}
	return t
}
func recurring(unit, value, start, zone string) *model.Program {
	return &model.Program{Kind: "event", ID: "sample:event:tick", Status: "ENABLED", EventSchedule: &model.EventSchedule{Type: "RECURRING", TimeZone: zone, Scheduler: "ON", Starts: str(start), IntervalField: str(unit), IntervalValue: str(value)}}
}
func report(p *model.Program, from, to string, lookback int) Report {
	return Build(map[string]*model.Engine{"sample": {Programs: []*model.Program{p}}}, "2026-01-01", instant(from), instant(to), lookback)
}

func TestHalfOpenWindowAndPriorCandidate(t *testing.T) {
	p := recurring("MINUTE", "15", "2026-01-01 00:00:00", "UTC")
	r := report(p, "2026-11-23T10:00:00Z", "2026-11-23T11:00:00Z", 30)
	if len(r.Events) != 1 || r.Events[0].Starts != 4 || !r.Events[0].FirstAt.Equal(instant("2026-11-23T10:00:00Z")) || !r.Events[0].LastAt.Equal(instant("2026-11-23T10:45:00Z")) || !r.Events[0].PreviousAt.Equal(instant("2026-11-23T09:45:00Z")) {
		t.Fatalf("wrong projection: %+v", r)
	}
	var total int64
	for _, b := range r.Buckets {
		total += b.Counts[p.ID]
	}
	if total != 4 {
		t.Fatalf("histogram lost occurrences: %d", total)
	}
}
func TestDayAndFixedOffsetUseEventClock(t *testing.T) {
	for _, zone := range []string{"Europe/Paris", "+01:00"} {
		p := recurring("DAY", "1", "2026-01-01 03:00:00", zone)
		r := report(p, "2026-11-23T01:59:00Z", "2026-11-23T02:01:00Z", 0)
		if len(r.Events) != 1 || r.Events[0].Starts != 1 {
			t.Fatalf("event clock ignored for %s: %+v", zone, r)
		}
	}
}
func TestDaylightSavingIsNotSilentlyGuessed(t *testing.T) {
	p := recurring("HOUR", "1", "2026-01-01 00:00:00", "Europe/Paris")
	r := report(p, "2026-10-25T00:00:00Z", "2026-10-25T03:00:00Z", 0)
	if len(r.Unknown) != 1 || len(r.Events) != 0 {
		t.Fatal("ambiguous DST range must be explicit")
	}
}

func TestPartialRepeatedOrMissingHoursStayExplicit(t *testing.T) {
	p := recurring("DAY", "1", "2026-01-01 02:30:00", "Europe/Paris")
	for _, window := range []struct{ from, to string }{{"2026-10-25T00:15:00Z", "2026-10-25T00:45:00Z"}, {"2026-03-29T01:00:00Z", "2026-03-29T02:00:00Z"}} {
		r := report(p, window.from, window.to, 0)
		if len(r.Unknown) != 1 || len(r.Events) != 0 {
			t.Fatal("partial DST day must not return a misleading instant")
		}
	}
}
func TestMonthEndDoesNotDrift(t *testing.T) {
	p := recurring("MONTH", "1", "2024-01-31 12:00:00", "UTC")
	for _, window := range []struct{ from, to string }{{"2024-02-29T00:00:00Z", "2024-03-01T00:00:00Z"}, {"2024-03-31T00:00:00Z", "2024-04-01T00:00:00Z"}} {
		r := report(p, window.from, window.to, 0)
		if len(r.Events) != 1 || r.Events[0].Starts != 1 {
			t.Fatalf("month end: %+v", r)
		}
	}
}
func TestStartsEndsAndLookback(t *testing.T) {
	p := recurring("HOUR", "1", "2026-11-23 10:00:00", "UTC")
	p.EventSchedule.Ends = str("2026-11-23 11:00:00")
	r := report(p, "2026-11-23T09:00:00Z", "2026-11-23T12:00:00Z", 0)
	if len(r.Events) != 1 || r.Events[0].Starts != 2 {
		t.Fatalf("bounds: %+v", r)
	}
	r = report(p, "2026-11-23T11:15:00Z", "2026-11-23T11:30:00Z", 30)
	if len(r.Events) != 1 || r.Events[0].Starts != 0 || r.Events[0].PreviousAt == nil {
		t.Fatal("prior execution candidate missing")
	}
	r = report(p, "2026-11-23T11:15:00Z", "2026-11-23T11:30:00Z", 0)
	if len(r.Events) != 0 {
		t.Fatal("lookback ignored")
	}
}
func TestOneTimeAndUnknownMetadata(t *testing.T) {
	p := &model.Program{Kind: "event", ID: "sample:event:once", Status: "ENABLED", EventSchedule: &model.EventSchedule{Type: "ONE TIME", TimeZone: "UTC", ExecuteAt: str("2026-11-23 10:00:00")}}
	r := report(p, "2026-11-23T10:00:00Z", "2026-11-23T11:00:00Z", 0)
	if len(r.Events) != 1 || r.Events[0].Starts != 1 {
		t.Fatal("one-time event missing")
	}
	p.EventSchedule = nil
	r = report(p, "2026-11-23T10:00:00Z", "2026-11-23T11:00:00Z", 0)
	if len(r.Unknown) != 1 {
		t.Fatal("legacy catalogs must request refresh")
	}
}
func TestInactiveSchedulersAreSeparate(t *testing.T) {
	p := recurring("SECOND", "1", "2020-01-01 00:00:00", "UTC")
	p.Status = "DISABLED"
	r := report(p, "2026-11-23T00:00:00Z", "2026-11-24T00:00:00Z", 0)
	if len(r.Excluded) != 1 || len(r.Events) != 0 {
		t.Fatal("disabled event included")
	}
	p.Status = "ENABLED"
	p.EventSchedule.Scheduler = "OFF"
	r = report(p, "2026-11-23T00:00:00Z", "2026-11-24T00:00:00Z", 0)
	if len(r.Excluded) != 1 {
		t.Fatal("disabled scheduler included")
	}
}
func TestHighFrequencyIsAggregated(t *testing.T) {
	p := recurring("SECOND", "1", "2020-01-01 00:00:00", "UTC")
	r := report(p, "2026-11-23T00:00:00Z", "2026-11-24T00:00:00Z", 0)
	if len(r.Events) != 1 || r.Events[0].Starts != 86400 || len(r.Buckets) > 24 {
		t.Fatal("high-frequency result must be bounded")
	}
}
func TestUnknownZoneAndIntervalRemainVisible(t *testing.T) {
	p := recurring("DAY", "1", "2020-01-01 00:00:00", "SYSTEM")
	p.EventSchedule.SystemTimeZone = "UNKNOWN"
	r := report(p, "2026-11-23T00:00:00Z", "2026-11-24T00:00:00Z", 0)
	if len(r.Unknown) != 1 {
		t.Fatal("unknown zone guessed")
	}
	p.EventSchedule.TimeZone = "UTC"
	p.EventSchedule.IntervalField = str("CUSTOM")
	r = report(p, "2026-11-23T00:00:00Z", "2026-11-24T00:00:00Z", 0)
	if len(r.Unknown) != 1 {
		t.Fatal("unknown interval hidden")
	}
}
