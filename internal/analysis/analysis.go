// Package analysis normalizes stored programs and runs a conservative,
// regular-expression based SQL impact analysis (reads, writes, calls).
package analysis

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/OlivierLB/AtlasSchemaDesk/internal/model"
	"github.com/OlivierLB/AtlasSchemaDesk/internal/pyre"
)

// Row is one catalog row: values are nil, string, bool or a JSON number.
type Row map[string]any

// Str returns a nullable text value.
func (r Row) Str(key string) *string {
	switch value := r[key].(type) {
	case nil:
		return nil
	case string:
		return &value
	default:
		text := fmt.Sprint(value)
		return &text
	}
}

// Text returns a text value, or "" when null (Python's `value or ""`).
func (r Row) Text(key string) string {
	if value := r.Str(key); value != nil {
		return *value
	}
	return ""
}

// Python returns the text value as Python's str() would print it ("None" when null).
func (r Row) Python(key string) string {
	if value := r.Str(key); value != nil {
		return *value
	}
	return "None"
}

// Bool interprets SQL and JSON booleans (t/f, 1/0, true/false).
func (r Row) Bool(key string) bool {
	switch value := r[key].(type) {
	case bool:
		return value
	case string:
		return value == "t" || value == "1" || strings.EqualFold(value, "true")
	case nil:
		return false
	default:
		text := fmt.Sprint(value)
		return text != "0" && text != ""
	}
}

// Int returns a nullable integer.
func (r Row) Int(key string) (*int64, error) {
	text := r.Str(key)
	if text == nil {
		return nil, nil
	}
	number, err := strconv.ParseInt(*text, 10, 64)
	if err != nil {
		float, floatErr := strconv.ParseFloat(*text, 64)
		if floatErr != nil {
			return nil, fmt.Errorf("%s: invalid integer %q", key, *text)
		}
		number = int64(float)
	}
	return &number, nil
}

func nullableOrEmpty(value *string) *string {
	if value == nil || *value == "" {
		return nil
	}
	return value
}

var (
	pgTiming = regexp.MustCompile(`\b(BEFORE|AFTER|INSTEAD OF)\b`)
	pgEvent  = regexp.MustCompile(`\b(INSERT|UPDATE|DELETE|TRUNCATE)\b`)
)

// PGPrograms normalizes the PostgreSQL programs catalog.
func PGPrograms(catalog map[string][]Row) []*model.Program {
	var programs []*model.Program
	for _, row := range catalog["functions"] {
		kind := "function"
		if row.Bool("is_trigger_function") {
			kind = "trigger_function"
		}
		programs = append(programs, &model.Program{
			Layout: model.PGFunction, Kind: kind, Schema: row.Text("schema_name"), Name: row.Text("name"),
			Signature: row.Text("name") + "(" + row.Text("arguments") + ")", Returns: row.Str("returns"),
			Language: row.Text("language"), Body: row.Text("body"),
		})
	}
	for _, row := range catalog["triggers"] {
		definition := row.Text("definition")
		var timing *string
		if match := pgTiming.FindStringSubmatch(definition); match != nil {
			timing = &match[1]
		}
		head, _, _ := strings.Cut(definition, " ON ")
		events := []string{}
		for _, match := range pgEvent.FindAllStringSubmatch(head, -1) {
			events = append(events, match[1])
		}
		programs = append(programs, &model.Program{
			Layout: model.PGTrigger, Kind: "trigger", Schema: row.Text("schema_name"), Name: row.Text("name"),
			Table:  &model.Ref{Schema: row.Text("schema_name"), Name: row.Text("table_name")},
			Timing: timing, Events: events,
			Function: &model.Ref{Schema: row.Text("function_schema"), Name: row.Text("function_name")},
			Body:     definition,
		})
	}
	for _, row := range catalog["views"] {
		kind := "view"
		if row.Bool("materialized") {
			kind = "materialized_view"
		}
		programs = append(programs, &model.Program{Layout: model.View, Kind: kind, Schema: row.Text("schema_name"), Name: row.Text("name"), Body: row.Text("body")})
	}
	return programs
}

// MySQLPrograms normalizes the MySQL programs catalog.
func MySQLPrograms(catalog map[string][]Row) []*model.Program {
	parameters := map[[3]string][]string{}
	for _, row := range catalog["parameters"] {
		key := [3]string{row.Text("schema_name"), row.Text("name"), row.Text("type")}
		var parts []string
		for _, part := range []string{row.Text("mode"), row.Text("parameter"), row.Text("data_type")} {
			if part != "" {
				parts = append(parts, part)
			}
		}
		parameters[key] = append(parameters[key], strings.Join(parts, " "))
	}
	var programs []*model.Program
	for _, row := range catalog["routines"] {
		params := parameters[[3]string{row.Text("schema_name"), row.Text("name"), row.Text("type")}]
		programs = append(programs, &model.Program{
			Layout: model.MySQLRoutine, Kind: strings.ToLower(row.Text("type")), Schema: row.Text("schema_name"), Name: row.Text("name"),
			Signature: row.Text("name") + "(" + strings.Join(params, ", ") + ")", Returns: row.Str("returns"),
			LastAltered: row.Str("last_altered"), Comment: nullableOrEmpty(row.Str("comment")), Body: row.Text("body"),
		})
	}
	for _, row := range catalog["triggers"] {
		programs = append(programs, &model.Program{
			Layout: model.MySQLTrigger, Kind: "trigger", Schema: row.Text("schema_name"), Name: row.Text("name"),
			Table:  &model.Ref{Schema: row.Text("table_schema"), Name: row.Text("table_name")},
			Timing: row.Str("timing"), Events: []string{row.Text("event")}, Body: row.Text("body"),
		})
	}
	for _, row := range catalog["views"] {
		programs = append(programs, &model.Program{Layout: model.View, Kind: "view", Schema: row.Text("schema_name"), Name: row.Text("name"), Body: row.Text("body")})
	}
	for _, row := range catalog["events"] {
		schedule := "une fois le " + row.Python("execute_at")
		if row.Text("event_type") == "RECURRING" {
			schedule = "toutes les " + row.Python("interval_value") + " " + row.Python("interval_field")
		}
		programs = append(programs, &model.Program{
			Layout: model.MySQLEvent, Kind: "event", Schema: row.Text("schema_name"), Name: row.Text("name"),
			Status: row.Text("status"), Schedule: schedule, Starts: row.Str("starts"),
			LastExecuted: row.Str("last_executed"), Body: row.Text("body"),
		})
	}
	return programs
}

// TableStats returns row/size estimates keyed by "schema.name".
func TableStats(catalog map[string][]Row) (map[string]model.TableStat, error) {
	stats := map[string]model.TableStat{}
	for _, row := range catalog["table_stats"] {
		rows, err := row.Int("estimated_rows")
		if err != nil {
			return nil, err
		}
		if rows != nil && *rows < 0 {
			rows = nil
		}
		if _, present := row["heap_bytes"]; present && rows != nil && *rows == 0 {
			// PostgreSQL: reltuples stays 0 when data is loaded after the indexes and the
			// table was never analysed (e.g. a fresh restore): unknown, not empty.
			if heap, err := row.Int("heap_bytes"); err == nil && heap != nil && *heap > 0 {
				rows = nil
			}
		}
		total, err := row.Int("total_bytes")
		if err != nil {
			return nil, err
		}
		stats[row.Text("schema_name")+"."+row.Text("name")] = model.TableStat{Rows: rows, Bytes: total}
	}
	return stats, nil
}

const ident = `(?:[A-Za-z_][` + pyre.Word + `$]*\.)?[A-Za-z_][` + pyre.Word + `$]*`

const s = pyre.S

var (
	writePatterns = []*regexp.Regexp{
		pyre.MustCompile(`(?i)\bINSERT` + s + `+(?:LOW_PRIORITY` + s + `+|DELAYED` + s + `+|HIGH_PRIORITY` + s + `+)?(?:IGNORE` + s + `+)?(?:INTO` + s + `+)?(` + ident + `)`),
		pyre.MustCompile(`(?i)\bREPLACE` + s + `+(?:LOW_PRIORITY` + s + `+|DELAYED` + s + `+)?(?:INTO` + s + `+)?(` + ident + `)`),
		updatePattern,
		pyre.MustCompile(`(?i)\bDELETE` + s + `+(?:LOW_PRIORITY` + s + `+|QUICK` + s + `+|IGNORE` + s + `+)*(?:` + ident + `(?:` + s + `*,` + s + `*` + ident + `)*` + s + `+)?FROM` + s + `+(?:ONLY` + s + `+)?(` + ident + `)`),
		pyre.MustCompile(`(?i)\bTRUNCATE` + s + `+(?:TABLE` + s + `+)?(?:ONLY` + s + `+)?(` + ident + `)`),
	}
	// Python used (?<!KEY\s)(?<!DO\s) before UPDATE; RE2 has no lookbehind, see writeMatches.
	updatePattern  = pyre.MustCompile(`(?i)\bUPDATE` + s + `+(?:LOW_PRIORITY` + s + `+)?(?:IGNORE` + s + `+)?(?:ONLY` + s + `+)?(` + ident + `)`)
	updateExcluded = pyre.MustCompile(`(?i)(?:KEY|DO)` + s + `$`)
	alias          = `(?:` + s + `+(?:AS` + s + `+)?[A-Za-z_]` + pyre.W + `*)?`
	readPattern    = pyre.MustCompile(`(?i)\b(?:FROM|JOIN)` + s + `+(` + ident + alias + `(?:` + s + `*,` + s + `*` + ident + alias + `)*)`)
	callPattern    = pyre.MustCompile(`(?i)\bCALL` + s + `+(` + ident + `)`)
	functionCall   = pyre.MustCompile(`(` + ident + `)` + s + `*\(`)
	dynamicMySQL   = pyre.MustCompile(`(?i)\bPREPARE` + s + `+` + pyre.W + `+` + s + `+FROM\b|\bEXECUTE` + s + `+IMMEDIATE\b`)
	// Python: \bEXECUTE\s+(?!PROCEDURE\b|FUNCTION\b)(?:format\s*\(|'|\w); see isDynamicPG.
	executeKeyword   = pyre.MustCompile(`(?i)\bEXECUTE` + s + `+`)
	executeExcluded  = pyre.MustCompile(`(?i)^(?:PROCEDURE|FUNCTION)\b`)
	executeDynamic   = pyre.MustCompile(`(?i)^(?:format` + s + `*\(|'|` + pyre.W + `)`)
	blockComment     = pyre.MustCompile(`(?s)/\*.*?\*/`)
	lineComment      = pyre.MustCompile(`--[^\n]*`)
	hashComment      = pyre.MustCompile(`(?m)#[^\n]*`)
	mysqlSingle      = pyre.MustCompile(`'(?:[^'\\]|\\.|'')*'`)
	mysqlDouble      = pyre.MustCompile(`"(?:[^"\\]|\\.|"")*"`)
	pgSingle         = pyre.MustCompile(`'(?:[^']|'')*'`)
	pgQuoted         = pyre.MustCompile(`"([^"]+)"`)
	mysqlLiterals    = pyre.MustCompile(`'((?:[^'\\]|\\.|'')*)'|"((?:[^"\\]|\\.|"")*)"`)
	postgresLiterals = pyre.MustCompile(`'((?:[^']|'')*)'`)
)

// StripSQL removes comments and string literals and unquotes identifiers.
func StripSQL(body, engine string) string {
	text := blockComment.ReplaceAllString(body, " ")
	text = lineComment.ReplaceAllString(text, " ")
	if engine == "mysql" {
		text = hashComment.ReplaceAllString(text, " ")
		text = mysqlSingle.ReplaceAllString(text, "''")
		text = mysqlDouble.ReplaceAllString(text, "''")
		return strings.ReplaceAll(text, "`", "")
	}
	text = pgSingle.ReplaceAllString(text, "''")
	return pgQuoted.ReplaceAllString(text, "${1}")
}

// StringLiterals returns the contents of string literals (where dynamic SQL is
// usually assembled).
func StringLiterals(body, engine string) string {
	text := lineComment.ReplaceAllString(blockComment.ReplaceAllString(body, " "), " ")
	pattern := postgresLiterals
	if engine == "mysql" {
		pattern = mysqlLiterals
	}
	var parts []string
	for _, match := range pattern.FindAllStringSubmatch(text, -1) {
		for _, group := range match[1:] {
			if group != "" {
				parts = append(parts, group)
			}
		}
	}
	return strings.ReplaceAll(strings.Join(parts, " "), "`", "")
}

// writeMatches returns the captured relation names of a write pattern,
// emulating Python's lookbehind exclusion for UPDATE.
func writeMatches(pattern *regexp.Regexp, text string) []string {
	if pattern != updatePattern {
		var names []string
		for _, match := range pattern.FindAllStringSubmatch(text, -1) {
			names = append(names, match[1])
		}
		return names
	}
	var names []string
	for offset := 0; offset <= len(text); {
		location := pattern.FindStringSubmatchIndex(text[offset:])
		if location == nil {
			break
		}
		start := offset + location[0]
		if updateExcluded.MatchString(lastRunes(text[:start], 4)) {
			offset = start + 1
			continue
		}
		names = append(names, text[offset+location[2]:offset+location[3]])
		offset += location[1]
	}
	return names
}

func lastRunes(text string, n int) string {
	runes := []rune(text)
	if len(runes) > n {
		runes = runes[len(runes)-n:]
	}
	return string(runes)
}

func isDynamicPG(text string) bool {
	for _, location := range executeKeyword.FindAllStringIndex(text, -1) {
		rest := text[location[1]:]
		if !executeExcluded.MatchString(rest) && executeDynamic.MatchString(rest) {
			return true
		}
	}
	return false
}

type relation struct{ schema, name string }

type resolver struct {
	byName   map[string][]relation
	lower    map[relation]relation
	defaults []string
}

func newResolver(relations map[relation]bool, defaults []string) *resolver {
	r := &resolver{byName: map[string][]relation{}, lower: map[relation]relation{}, defaults: defaults}
	ordered := make([]relation, 0, len(relations))
	for key := range relations {
		ordered = append(ordered, key)
	}
	// Deterministic winner when two relations differ only by case.
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].schema != ordered[j].schema {
			return ordered[i].schema < ordered[j].schema
		}
		return ordered[i].name < ordered[j].name
	})
	for _, key := range ordered {
		name := strings.ToLower(key.name)
		r.byName[name] = append(r.byName[name], key)
		r.lower[relation{strings.ToLower(key.schema), name}] = key
	}
	return r
}

func (r *resolver) resolve(ident, schema string) (relation, bool) {
	parts := strings.Split(ident, ".")
	if len(parts) == 2 {
		found, ok := r.lower[relation{strings.ToLower(parts[0]), strings.ToLower(parts[1])}]
		return found, ok
	}
	for _, candidate := range append([]string{schema}, r.defaults...) {
		if found, ok := r.lower[relation{strings.ToLower(candidate), strings.ToLower(parts[0])}]; ok {
			return found, true
		}
	}
	if matches := r.byName[strings.ToLower(parts[0])]; len(matches) == 1 {
		return matches[0], true
	}
	return relation{}, false
}

type relationSet map[relation]bool

func (set relationSet) names(exclude ...relationSet) []string {
	names := []string{}
outer:
	for key := range set {
		for _, other := range exclude {
			if other[key] {
				continue outer
			}
		}
		names = append(names, key.schema+"."+key.name)
	}
	sort.Strings(names)
	return names
}

// Analyse assigns program IDs ("<engine>:<kind>:<schema>.<name>"), computes each
// program's impact and returns the reverse index per relation.
func Analyse(engine string, programs []*model.Program, tables []model.Table) map[string]*model.IndexEntry {
	relations := map[relation]bool{}
	for _, table := range tables {
		relations[relation{table.Schema, table.Name}] = true
	}
	for _, program := range programs {
		if program.Kind == "view" || program.Kind == "materialized_view" {
			relations[relation{program.Schema, program.Name}] = true
		}
	}
	defaults := []string{"public"}
	if engine == "mysql" {
		defaults = nil
	}
	resolver := newResolver(relations, defaults)
	routines := map[string][]*model.Program{}
	for _, program := range programs {
		if program.Kind == "function" || program.Kind == "procedure" || program.Kind == "trigger_function" {
			name := strings.ToLower(program.Name)
			routines[name] = append(routines[name], program)
		}
	}
	routineRef := func(ident, schema string, kinds ...string) string {
		parts := strings.Split(ident, ".")
		var candidates []*model.Program
		for _, program := range routines[strings.ToLower(parts[len(parts)-1])] {
			for _, kind := range kinds {
				if program.Kind == kind {
					candidates = append(candidates, program)
					break
				}
			}
		}
		if len(parts) == 2 {
			var filtered []*model.Program
			for _, program := range candidates {
				if strings.ToLower(program.Schema) == strings.ToLower(parts[0]) {
					filtered = append(filtered, program)
				}
			}
			candidates = filtered
		} else if len(candidates) > 1 {
			var filtered []*model.Program
			for _, program := range candidates {
				if program.Schema == schema {
					filtered = append(filtered, program)
				}
			}
			if len(filtered) == 0 {
				filtered = candidates[:1]
			}
			candidates = filtered
		}
		if len(candidates) == 0 {
			return ""
		}
		return candidates[0].ID
	}
	for _, program := range programs {
		program.ID = engine + ":" + program.Kind + ":" + program.Schema + "." + program.Name
	}
	relationsIn := func(text, schema string) (relationSet, relationSet) {
		writes, reads := relationSet{}, relationSet{}
		for _, pattern := range writePatterns {
			for _, name := range writeMatches(pattern, text) {
				if key, ok := resolver.resolve(name, schema); ok {
					writes[key] = true
				}
			}
		}
		for _, match := range readPattern.FindAllStringSubmatch(text, -1) {
			for _, item := range strings.Split(match[1], ",") {
				fields := strings.Fields(item)
				if len(fields) == 0 {
					continue
				}
				if key, ok := resolver.resolve(fields[0], schema); ok {
					reads[key] = true
				}
			}
		}
		return writes, reads
	}
	for _, program := range programs {
		text := StripSQL(program.Body, engine)
		if engine != "mysql" && program.Kind == "trigger" {
			text = "" // a PG trigger only points to its function; impact comes from the function
		}
		calls := map[string]bool{}
		writes, reads := relationsIn(text, program.Schema)
		for _, match := range callPattern.FindAllStringSubmatch(text, -1) {
			if ref := routineRef(match[1], program.Schema, "procedure"); ref != "" {
				calls[ref] = true
			}
		}
		for _, match := range functionCall.FindAllStringSubmatch(text, -1) {
			if ref := routineRef(match[1], program.Schema, "function"); ref != "" && ref != program.ID {
				calls[ref] = true
			}
		}
		if program.Kind == "trigger" && engine != "mysql" && program.Function != nil {
			if ref := routineRef(program.Function.Schema+"."+program.Function.Name, program.Schema, "trigger_function", "function"); ref != "" {
				calls[ref] = true
			}
		}
		if program.Table != nil {
			// The table a trigger fires on is not "read" by it in the impact sense.
			delete(reads, relation{program.Table.Schema, program.Table.Name})
		}
		dynamic := false
		if program.Kind != "view" && program.Kind != "materialized_view" {
			if engine == "mysql" {
				dynamic = dynamicMySQL.MatchString(text)
			} else {
				dynamic = isDynamicPG(text)
			}
		}
		probableWrites, probableReads := relationSet{}, relationSet{}
		if dynamic {
			// SQL assembled in strings then executed: names found there are probable, not certain.
			probableWrites, probableReads = relationsIn(StringLiterals(program.Body, engine), program.Schema)
		}
		program.Impact = &model.Impact{
			Writes:         writes.names(),
			Reads:          reads.names(writes),
			Calls:          sortedKeys(calls),
			DynamicSQL:     dynamic,
			ProbableWrites: probableWrites.names(writes),
			ProbableReads:  probableReads.names(reads, writes, probableWrites.without(writes)),
		}
	}
	return buildIndex(programs)
}

func (set relationSet) without(other relationSet) relationSet {
	result := relationSet{}
	for key := range set {
		if !other[key] {
			result[key] = true
		}
	}
	return result
}

func sortedKeys(values map[string]bool) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// buildIndex builds the reverse index per relation, including writes reached
// through the call graph.
func buildIndex(programs []*model.Program) map[string]*model.IndexEntry {
	byID := map[string]*model.Program{}
	callers := map[string]map[string]bool{}
	for _, program := range programs {
		byID[program.ID] = program
	}
	for _, program := range programs {
		for _, callee := range program.Impact.Calls {
			if callers[callee] == nil {
				callers[callee] = map[string]bool{}
			}
			callers[callee][program.ID] = true
		}
	}
	var transitive func(id string, seen map[string]bool) map[string]bool
	transitive = func(id string, seen map[string]bool) map[string]bool {
		result := map[string]bool{}
		if seen[id] {
			return result
		}
		seen[id] = true
		program := byID[id]
		for _, name := range program.Impact.Writes {
			result[name] = true
		}
		for _, callee := range program.Impact.Calls {
			if _, known := byID[callee]; known {
				for name := range transitive(callee, seen) {
					result[name] = true
				}
			}
		}
		return result
	}
	index := map[string]*model.IndexEntry{}
	entry := func(key string) *model.IndexEntry {
		if index[key] == nil {
			index[key] = &model.IndexEntry{Writes: []string{}, Reads: []string{}, Triggers: []string{}, IndirectWrites: []string{}, ProbableWrites: []string{}, ProbableReads: []string{}}
		}
		return index[key]
	}
	for _, program := range programs {
		for _, key := range program.Impact.Writes {
			entry(key).Writes = append(entry(key).Writes, program.ID)
		}
		for _, key := range program.Impact.Reads {
			entry(key).Reads = append(entry(key).Reads, program.ID)
		}
		for _, key := range program.Impact.ProbableWrites {
			entry(key).ProbableWrites = append(entry(key).ProbableWrites, program.ID)
		}
		for _, key := range program.Impact.ProbableReads {
			entry(key).ProbableReads = append(entry(key).ProbableReads, program.ID)
		}
		if program.Table != nil {
			key := program.Table.Schema + "." + program.Table.Name
			entry(key).Triggers = append(entry(key).Triggers, program.ID)
		}
		direct := map[string]bool{}
		for _, name := range program.Impact.Writes {
			direct[name] = true
		}
		indirect := map[string]bool{}
		for name := range transitive(program.ID, map[string]bool{}) {
			if !direct[name] {
				indirect[name] = true
			}
		}
		program.Impact.IndirectWrites = sortedKeys(indirect)
		for _, key := range program.Impact.IndirectWrites {
			entry(key).IndirectWrites = append(entry(key).IndirectWrites, program.ID)
		}
	}
	for _, program := range programs {
		calledBy := callers[program.ID]
		if calledBy == nil {
			calledBy = map[string]bool{}
		}
		program.Impact.CalledBy = sortedKeys(calledBy)
	}
	return index
}
