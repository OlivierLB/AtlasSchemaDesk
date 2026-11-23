package catalog

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/OlivierLB/AtlasSchemaDesk/internal/analysis"
	"github.com/OlivierLB/AtlasSchemaDesk/internal/config"
	"github.com/OlivierLB/AtlasSchemaDesk/internal/model"
)

// ReferenceCatalog is read from local source files, independently of any DB.
type ReferenceCatalog struct {
	GeneratedAt string                   `json:"generatedAt"`
	Engines     map[string]*model.Engine `json:"engines"`
	Warnings    []string                 `json:"warnings"`
	FilesRead   int                      `json:"filesRead"`
}

type sqlToken struct {
	value      string
	start, end int
	quoted     bool
	literal    bool
}

// Tokens exclude comments and retain string boundaries. A CREATE in a string or
// comment can never become a definition. Executable version comments are SQL.
func sourceTokens(text string) []sqlToken {
	var out []sqlToken
	for i := 0; i < len(text); {
		c := text[i]
		if c <= 32 {
			i++
			continue
		}
		if c == '#' || (c == '-' && i+2 < len(text) && text[i+1] == '-' && text[i+2] <= 32) {
			for i < len(text) && text[i] != '\n' {
				i++
			}
			continue
		}
		if c == '/' && i+1 < len(text) && text[i+1] == '*' {
			end := strings.Index(text[i+2:], "*/")
			if end < 0 {
				break
			}
			end += i + 2
			if i+2 < len(text) && text[i+2] == '!' {
				begin := i + 3
				for begin < end && text[begin] >= '0' && text[begin] <= '9' {
					begin++
				}
				for _, t := range sourceTokens(text[begin:end]) {
					t.start += begin
					t.end += begin
					out = append(out, t)
				}
			}
			i = end + 2
			continue
		}
		start := i
		if c == '\'' || c == '"' || c == '`' {
			i++
			var b strings.Builder
			for i < len(text) {
				if text[i] == '\\' && i+1 < len(text) {
					b.WriteByte(text[i+1])
					i += 2
					continue
				}
				if text[i] == c {
					i++
					if i < len(text) && text[i] == c {
						b.WriteByte(c)
						i++
						continue
					}
					break
				}
				b.WriteByte(text[i])
				i++
			}
			out = append(out, sqlToken{b.String(), start, i, true, c != '`'})
			continue
		}
		if strings.ContainsRune(".;=(),@+-*/", rune(c)) {
			i++
		} else {
			for i < len(text) && text[i] > 32 && !strings.ContainsRune(".;=(),@+-*/'\"`#", rune(text[i])) {
				i++
			}
			if i == start {
				i++
			}
		}
		out = append(out, sqlToken{text[start:i], start, i, false, false})
	}
	return out
}
func keyword(t sqlToken, word string) bool { return !t.quoted && strings.EqualFold(t.value, word) }
func sourceName(ts []sqlToken, i int, schema string) (string, string, int) {
	if i >= len(ts) || ts[i].literal {
		return schema, "", i
	}
	name := ts[i].value
	i++
	if i+1 < len(ts) && ts[i].value == "." {
		schema, name = name, ts[i+1].value
		i += 2
	}
	return schema, name, i
}
func literalDate(ts []sqlToken) *string {
	if len(ts) != 1 || !ts[0].literal {
		return nil
	}
	value := strings.ReplaceAll(ts[0].value, "T", " ")
	if len(value) == 10 {
		value += " 00:00:00"
	}
	if _, err := time.Parse("2006-01-02 15:04:05", value); err != nil {
		return nil
	}
	return &value
}

func sourcePrograms(text, relative, schema, zone string) []*model.Program {
	ts := sourceTokens(text)
	type definition struct {
		start, nameEnd           int
		kind, schema, name, zone string
	}
	var defs []definition
	currentZone := zone
	for i := 0; i < len(ts); i++ {
		if keyword(ts[i], "USE") {
			if i+1 < len(ts) {
				schema = ts[i+1].value
			}
			continue
		}
		if keyword(ts[i], "SET") {
			for j := i + 1; j+2 < len(ts) && j < i+12 && ts[j].value != ";"; j++ {
				if strings.EqualFold(ts[j].value, "time_zone") && ts[j+1].value == "=" && ts[j+2].literal {
					currentZone = ts[j+2].value
				}
			}
		}
		if !keyword(ts[i], "CREATE") {
			continue
		}
		for j := i + 1; j < len(ts) && j < i+24; j++ {
			if ts[j].value == ";" {
				break
			}
			kind := strings.ToLower(ts[j].value)
			if !ts[j].quoted && (kind == "table" || kind == "view" || kind == "database" || kind == "trigger" || kind == "index") {
				break
			}
			if ts[j].quoted || (kind != "event" && kind != "procedure" && kind != "function") {
				continue
			}
			k := j + 1
			if k+2 < len(ts) && keyword(ts[k], "IF") && keyword(ts[k+1], "NOT") && keyword(ts[k+2], "EXISTS") {
				k += 3
			}
			s, n, k := sourceName(ts, k, schema)
			if n != "" {
				defs = append(defs, definition{i, k, kind, s, n, currentZone})
			}
			break
		}
	}
	var result []*model.Program
	for index, d := range defs {
		end := len(ts)
		if index+1 < len(defs) {
			end = defs[index+1].start
		}
		p := &model.Program{Layout: model.MySQLRoutine, Kind: d.kind, Schema: d.schema, Name: d.name, Body: text[ts[d.start].start:ts[end-1].end], Source: &model.Source{Status: "reference", Path: relative, Line: strings.Count(text[:ts[d.start].start], "\n") + 1}}
		if d.kind == "event" {
			p.Layout = model.MySQLEvent
			p.Status = "ENABLED"
			s := &model.EventSchedule{TimeZone: d.zone, Scheduler: "ON", Type: "UNKNOWN"}
			p.EventSchedule = s
			headerEnd := end
			for j := d.nameEnd; j < end; j++ {
				if keyword(ts[j], "DO") {
					headerEnd = j
					break
				}
			}
			h := ts[d.nameEnd:headerEnd]
			for j := 0; j < len(h); j++ {
				if keyword(h[j], "DISABLE") {
					p.Status = "DISABLED"
				}
				if !keyword(h[j], "SCHEDULE") {
					continue
				}
				p.Schedule = text[h[j].end:ts[headerEnd-1].end]
				if j+1 >= len(h) {
					continue
				}
				if keyword(h[j+1], "EVERY") && j+3 < len(h) {
					v, u := h[j+2].value, strings.ToUpper(h[j+3].value)
					if _, err := strconv.ParseInt(v, 10, 64); err == nil {
						s.Type = "RECURRING"
						s.IntervalValue = &v
						s.IntervalField = &u
					}
				}
				for k := j + 1; k < len(h); k++ {
					word := strings.ToUpper(h[k].value)
					if h[k].quoted || (word != "STARTS" && word != "ENDS" && word != "AT") {
						continue
					}
					stop := k + 1
					for stop < len(h) && !keyword(h[stop], "ENDS") && !keyword(h[stop], "ON") && !keyword(h[stop], "ENABLE") && !keyword(h[stop], "DISABLE") && !keyword(h[stop], "COMMENT") {
						stop++
					}
					value := literalDate(h[k+1 : stop])
					switch word {
					case "STARTS":
						s.Starts = value
					case "ENDS":
						s.Ends = value
						if value == nil {
							s.Type = "UNKNOWN"
						}
					case "AT":
						s.Type = "ONE TIME"
						s.ExecuteAt = value
					}
				}
			}
			p.Starts = s.Starts
			if headerEnd == end {
				s.Type = "UNKNOWN"
			}
		}
		result = append(result, p)
	}
	return result
}

// Reference reads only the configured repository. It never connects to a DB,
// executes SQL, or writes a catalog containing the user's private definitions.
func Reference(options *config.RoutinesRepository, zone string) (ReferenceCatalog, error) {
	r := ReferenceCatalog{GeneratedAt: time.Now().UTC().Format(time.RFC3339), Engines: map[string]*model.Engine{}, Warnings: []string{}}
	if options == nil {
		r.Warnings = append(r.Warnings, "Configure un dépôt de sources SQL dans les connexions pour analyser l’environnement de référence.")
		return r, nil
	}
	root, err := filepath.EvalSymlinks(options.Path)
	if err != nil {
		return r, fmt.Errorf("Dépôt SQL local indisponible")
	}
	programs := []*model.Program{}
	seen := map[string]*model.Program{}
	var bytesRead int64
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			r.Warnings = append(r.Warnings, "Un dossier source ne peut pas être lu.")
			return nil
		}
		if entry.IsDir() {
			if strings.HasPrefix(entry.Name(), ".") && path != root {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.EqualFold(filepath.Ext(path), ".sql") {
			return nil
		}
		target := resolvePath(path)
		if !within(root, target) {
			r.Warnings = append(r.Warnings, "Un lien hors du dépôt SQL a été ignoré.")
			return nil
		}
		info, e := os.Stat(target)
		if e != nil || !info.Mode().IsRegular() || info.Size() > 8<<20 || bytesRead+info.Size() > 64<<20 || r.FilesRead >= 10000 {
			r.Warnings = append(r.Warnings, "Une source dépasse les limites de lecture.")
			return nil
		}
		text, e := readSource(target)
		if e != nil {
			r.Warnings = append(r.Warnings, "Un fichier SQL ne peut pas être lu.")
			return nil
		}
		r.FilesRead++
		bytesRead += int64(len(text))
		rel, _ := filepath.Rel(root, path)
		schema := options.DefaultSchema
		parts := strings.Split(filepath.ToSlash(rel), "/")
		if strings.HasPrefix(options.Pattern, "{schema}/") && len(parts) > 2 {
			schema = parts[0]
		}
		for _, p := range sourcePrograms(text, filepath.ToSlash(rel), schema, zone) {
			key := p.Kind + ":" + p.Schema + "." + p.Name
			if previous := seen[key]; previous != nil {
				if previous.EventSchedule != nil {
					previous.EventSchedule.Type = "UNKNOWN"
				}
				r.Warnings = append(r.Warnings, "Définition SQL en doublon : première définition conservée pour l’inspection, horaires à vérifier.")
				continue
			}
			seen[key] = p
			programs = append(programs, p)
		}
		return nil
	})
	if err != nil {
		return r, fmt.Errorf("Lecture du dépôt SQL impossible")
	}
	// Seed the static analyzer with relations mentioned by the sources. These
	// are source references, not proof that a relation exists on a live server.
	var tables []model.Table
	for _, p := range programs {
		ts := sourceTokens(p.Body)
		for i, t := range ts {
			if keyword(t, "FROM") || keyword(t, "JOIN") || keyword(t, "UPDATE") || keyword(t, "INTO") {
				s, n, _ := sourceName(ts, i+1, p.Schema)
				if n != "" && n != "(" {
					tables = append(tables, model.Table{Schema: s, Name: n})
				}
			}
		}
	}
	index := analysis.Analyse("mysql", programs, tables)
	// A single virtual source avoids duplicating events across local connections.
	for _, p := range programs {
		p.ID = strings.Replace(p.ID, "mysql:", "reference:", 1)
		p.Impact.Calls = referenceIDs(p.Impact.Calls)
		p.Impact.CalledBy = referenceIDs(p.Impact.CalledBy)
	}
	for _, entry := range index {
		entry.Writes = referenceIDs(entry.Writes)
		entry.Reads = referenceIDs(entry.Reads)
		entry.IndirectWrites = referenceIDs(entry.IndirectWrites)
		entry.ProbableWrites = referenceIDs(entry.ProbableWrites)
		entry.ProbableReads = referenceIDs(entry.ProbableReads)
	}
	r.Engines["reference"] = &model.Engine{GeneratedAt: r.GeneratedAt, Programs: programs, TableImpacts: index, TableStats: map[string]model.TableStat{}}
	return r, nil
}
func referenceIDs(ids []string) []string {
	for i, id := range ids {
		ids[i] = strings.Replace(id, "mysql:", "reference:", 1)
	}
	return ids
}
