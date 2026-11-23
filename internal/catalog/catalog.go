// Package catalog exports schemas and stored programs (catalog only, never
// table data) and links programs to an optional routines repository.
package catalog

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/OlivierLB/AtlasSchemaDesk/internal/analysis"
	"github.com/OlivierLB/AtlasSchemaDesk/internal/config"
	"github.com/OlivierLB/AtlasSchemaDesk/internal/db"
	"github.com/OlivierLB/AtlasSchemaDesk/internal/model"
	"github.com/OlivierLB/AtlasSchemaDesk/internal/pyre"
	"github.com/OlivierLB/AtlasSchemaDesk/internal/snapshot"
)

// Now returns the export timestamp format ("2026-11-20T15:40:35+00:00").
func Now() string { return time.Now().UTC().Format("2006-01-02T15:04:05+00:00") }

// Schema reads tables, columns, unique constraints and foreign keys.
func Schema(database *db.Database) ([]model.Table, error) {
	columns, unique, foreignKeys := mysqlColumns, mysqlUnique, mysqlFKs
	if database.IsPostgres() {
		columns, unique, foreignKeys = pgColumns, pgUnique, pgFKs
	}
	rows, err := database.Query(columns)
	if err != nil {
		return nil, err
	}
	type key struct{ schema, table string }
	var order []key
	tables := map[key]*model.Table{}
	for _, row := range rows {
		k := key{row.Text("schema_name"), row.Text("table_name")}
		table := tables[k]
		if table == nil {
			table = &model.Table{Schema: k.schema, Name: k.table, Columns: []model.Column{}, ForeignKeys: []model.ForeignKey{}, UniqueConstraints: [][]string{}}
			tables[k] = table
			order = append(order, k)
		}
		table.Columns = append(table.Columns, model.Column{
			Name: row.Text("column_name"), Type: row.Text("data_type"), Nullable: !row.Bool("not_null"),
			Default: row.Str("default_value"), Primary: row.Bool("is_primary"),
		})
	}
	if rows, err = database.Query(unique); err != nil {
		return nil, err
	}
	groups := map[key][]string{}
	members := map[key]map[string][]string{}
	for _, row := range rows {
		k := key{row.Text("schema_name"), row.Text("table_name")}
		name := row.Text("constraint_name")
		if members[k] == nil {
			members[k] = map[string][]string{}
		}
		if _, seen := members[k][name]; !seen {
			groups[k] = append(groups[k], name)
		}
		members[k][name] = append(members[k][name], row.Text("column_name"))
	}
	for k, names := range groups {
		if table := tables[k]; table != nil {
			for _, name := range names {
				table.UniqueConstraints = append(table.UniqueConstraints, members[k][name])
			}
		}
	}
	if rows, err = database.Query(foreignKeys); err != nil {
		return nil, err
	}
	for _, row := range rows {
		if table := tables[key{row.Text("schema_name"), row.Text("table_name")}]; table != nil {
			table.ForeignKeys = append(table.ForeignKeys, model.ForeignKey{
				Column: row.Text("column_name"), TargetSchema: row.Text("ref_schema"),
				TargetTable: row.Text("ref_table"), TargetColumn: row.Text("ref_column"),
			})
		}
	}
	result := make([]model.Table, 0, len(order))
	for _, k := range order {
		result = append(result, *tables[k])
	}
	return result, nil
}

// Programs reads the raw stored-program catalog.
func Programs(database *db.Database) (map[string][]analysis.Row, error) {
	catalog := map[string][]analysis.Row{}
	if !database.IsPostgres() {
		for _, query := range mysqlPrograms {
			rows, err := database.Query(query.sql)
			if err != nil {
				return nil, err
			}
			catalog[query.name] = rows
		}
		return catalog, nil
	}
	rows, err := database.Query("SHOW server_version_num")
	if err != nil {
		return nil, err
	}
	if len(rows) != 1 {
		return nil, errors.New("server_version_num: unexpected result")
	}
	version, err := strconv.Atoi(rows[0].Text("server_version_num"))
	if err != nil {
		return nil, fmt.Errorf("server_version_num: %w", err)
	}
	query := pgPrograms
	if version >= 110000 {
		query = strings.Replace(query, "NOT p.proisagg", "p.prokind IN ('f','p')", 1)
		query = strings.Replace(query, "p.prosrc AS body", "p.prosrc AS body, p.prokind = 'p' AS is_procedure", 1)
		// pg_get_function_result() is not defined for procedures.
		query = strings.Replace(query, "pg_get_function_result(p.oid)", "CASE WHEN p.prokind = 'p' THEN NULL ELSE pg_get_function_result(p.oid) END", 1)
	}
	if rows, err = database.Query(query); err != nil {
		return nil, err
	}
	if len(rows) != 1 || len(rows[0]) != 1 {
		return nil, errors.New("programs catalog: unexpected result")
	}
	var text string
	for _, value := range rows[0] {
		text, _ = value.(string)
	}
	decoder := json.NewDecoder(strings.NewReader(text))
	decoder.UseNumber()
	var raw map[string][]map[string]any
	if err := decoder.Decode(&raw); err != nil {
		return nil, fmt.Errorf("programs catalog: %w", err)
	}
	for name, items := range raw {
		for _, item := range items {
			catalog[name] = append(catalog[name], analysis.Row(item))
		}
	}
	return catalog, nil
}

var (
	createDefinition = pyre.MustCompile(`(?i)\bCREATE` + pyre.S + `+(?:OR` + pyre.S + `+REPLACE` + pyre.S + `+)?(?:ALGORITHM` + pyre.S + `*=` + pyre.S + `*` + pyre.W + `+` + pyre.S + `+)?(?:DEFINER` + pyre.S + `*=` + pyre.S + `*` + pyre.NS + `+` + pyre.S + `+)?(?:SQL` + pyre.S + `+SECURITY` + pyre.S + `+` + pyre.W + `+` + pyre.S + `+)?` +
		`(FUNCTION|PROCEDURE|EVENT|TRIGGER|VIEW)` + pyre.S + "+(?:IF" + pyre.S + "+NOT" + pyre.S + "+EXISTS" + pyre.S + "+)?`?(" + pyre.W + "+)`?(?:\\.`?(" + pyre.W + "+)`?)?")
	sourceComments = pyre.MustCompile(`(?s)--[^\n]*|/\*.*?\*/|#[^\n]*`)
	sourceNoise    = pyre.MustCompile(`[` + pyre.Space + `'"` + "`" + `\\]+`)
)

func normalize(text string) string {
	text = sourceComments.ReplaceAllString(text, "")
	text = strings.NewReplacer(`\n`, "", `\t`, "").Replace(text)
	return strings.ToLower(sourceNoise.ReplaceAllString(text, ""))
}

// readSource decodes UTF-8, falling back to Latin-1.
func readSource(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if utf8.Valid(raw) {
		return string(raw), nil
	}
	runes := make([]rune, len(raw))
	for index, b := range raw {
		runes[index] = rune(b)
	}
	return string(runes), nil
}

func within(root, target string) bool {
	relative, err := filepath.Rel(root, target)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

// resolvePath follows symlinks of the existing prefix (Python Path.resolve).
func resolvePath(path string) string {
	existing, rest := path, ""
	for {
		if real, err := filepath.EvalSymlinks(existing); err == nil {
			return filepath.Join(real, rest)
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			return path
		}
		rest = filepath.Join(filepath.Base(existing), rest)
		existing = parent
	}
}

type definition struct {
	source     model.Source
	normalized *string
}

// LinkSources compares programs with the SQL files of a routines repository.
func LinkSources(programs []*model.Program, options config.RoutinesRepository) *model.Repository {
	root := options.Path
	realRoot := resolvePath(root)
	pattern := options.Pattern
	if pattern == "" {
		pattern = "{schema}/{kind}s/{name}.sql"
	}
	defaultSchema := options.DefaultSchema
	if defaultSchema == "" {
		defaultSchema = "public"
	}
	definitions := map[[3]string]definition{}
	if options.ScanDefinitions {
		var files []string
		_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err == nil && !entry.IsDir() && strings.HasSuffix(entry.Name(), ".sql") {
				files = append(files, path)
			}
			return nil
		})
		sort.Slice(files, func(i, j int) bool { return pathLess(files[i], files[j]) })
		for _, file := range files {
			target := resolvePath(file)
			if !within(realRoot, target) {
				continue
			}
			text, err := readSource(target)
			if err != nil {
				continue
			}
			relative, _ := filepath.Rel(root, file)
			parts := strings.Split(relative, string(filepath.Separator))
			schema := defaultSchema
			if len(parts) > 1 {
				schema = parts[0]
			}
			normalized := normalize(text)
			for _, match := range createDefinition.FindAllStringSubmatchIndex(text, -1) {
				kind := text[match[2]:match[3]]
				objectSchema, name := schema, text[match[4]:match[5]]
				if match[6] >= 0 {
					objectSchema, name = name, text[match[6]:match[7]]
				}
				key := [3]string{strings.ToLower(kind), strings.ToLower(objectSchema), strings.ToLower(name)}
				if _, exists := definitions[key]; !exists {
					definitions[key] = definition{
						source:     model.Source{Path: relative, AbsolutePath: target, Line: strings.Count(text[:match[0]], "\n") + 1},
						normalized: &normalized,
					}
				}
			}
		}
	}
	counts := map[string]int{}
	for _, program := range programs {
		relative := strings.NewReplacer("{schema}", program.Schema, "{kind}", program.Kind, "{name}", program.Name).Replace(pattern)
		target := resolvePath(filepath.Join(root, relative))
		if !within(realRoot, target) {
			continue
		}
		state := "absent"
		source := model.Source{Path: relative, AbsolutePath: target, Line: 1}
		if found, ok := definitions[[3]string{program.Kind, strings.ToLower(program.Schema), strings.ToLower(program.Name)}]; ok {
			source = found.source
			switch {
			case program.Kind == "view":
				state = "not_comparable"
			case strings.Contains(*found.normalized, normalize(program.Body)):
				state = "same"
			default:
				state = "different"
			}
		} else if info, err := os.Stat(target); err == nil && info.Mode().IsRegular() {
			if text, err := readSource(target); err == nil {
				state = "different"
				if strings.Contains(normalize(text), normalize(program.Body)) {
					state = "same"
				}
			}
		}
		counts[state]++
		if state == "absent" {
			program.Source = &model.Source{Status: state}
		} else {
			source.Status = state
			program.Source = &source
		}
	}
	info, err := os.Stat(root)
	return &model.Repository{Available: err == nil && info.IsDir(), Counts: counts}
}

// pathLess orders paths component by component, like sorting Python Paths.
func pathLess(a, b string) bool {
	left, right := strings.Split(a, string(filepath.Separator)), strings.Split(b, string(filepath.Separator))
	for index := 0; index < len(left) && index < len(right); index++ {
		if left[index] != right[index] {
			return left[index] < right[index]
		}
	}
	return len(left) < len(right)
}

// exportSource reads one connection's catalog and analysis.
func exportSource(cfg *config.Config, options config.Connection, now string) ([]model.Table, *model.Engine, error) {
	database := db.NewDatabase(cfg, options)
	defer database.Close()
	tables, err := Schema(database)
	if err != nil {
		return nil, nil, err
	}
	raw, err := Programs(database)
	if err != nil {
		return nil, nil, err
	}
	var programs []*model.Program
	if database.IsPostgres() {
		programs = analysis.PGPrograms(raw)
		for index, row := range raw["functions"] {
			if row.Bool("is_procedure") {
				programs[index].Kind = "procedure"
			}
		}
	} else {
		programs = analysis.MySQLPrograms(raw)
	}
	// Analysis needs the driver dialect; stable IDs use the configured source.
	index := analysis.Analyse(options.Driver, programs, tables)
	oldPrefix, newPrefix := options.Driver+":", options.ID+":"
	rename := func(ids []string) []string {
		for i, id := range ids {
			ids[i] = newPrefix + strings.TrimPrefix(id, oldPrefix)
		}
		return ids
	}
	for _, program := range programs {
		program.ID = newPrefix + strings.TrimPrefix(program.ID, oldPrefix)
		rename(program.Impact.Calls)
		rename(program.Impact.CalledBy)
	}
	for _, entry := range index {
		for _, ids := range [][]string{entry.Writes, entry.Reads, entry.Triggers, entry.IndirectWrites, entry.ProbableWrites, entry.ProbableReads} {
			rename(ids)
		}
	}
	stats, err := analysis.TableStats(raw)
	if err != nil {
		return nil, nil, err
	}
	if programs == nil {
		programs = []*model.Program{}
	}
	engine := &model.Engine{GeneratedAt: now, Programs: programs, TableImpacts: index, TableStats: stats}
	if cfg.RoutinesFor(options.ID) {
		engine.SourceRepository = LinkSources(programs, *cfg.Routines)
	}
	return tables, engine, nil
}

type orderedSources struct {
	names  []string
	tables map[string][]model.Table
}

func (o orderedSources) MarshalJSON() ([]byte, error) {
	var buffer bytes.Buffer
	buffer.WriteByte('{')
	for index, name := range o.names {
		if index > 0 {
			buffer.WriteByte(',')
		}
		key, _ := model.Marshal(name)
		value, err := model.Marshal(o.tables[name])
		if err != nil {
			return nil, err
		}
		buffer.Write(key)
		buffer.WriteByte(':')
		buffer.Write(value)
	}
	buffer.WriteByte('}')
	return buffer.Bytes(), nil
}

// Export writes programs.json, schema.json and a schema snapshot when it changed.
// A failing source aborts the export and preserves the previous complete files.
func Export(cfg *config.Config) error {
	now := Now()
	sources := orderedSources{tables: map[string][]model.Table{}}
	engines := map[string]*model.Engine{}
	for _, options := range cfg.Connections {
		tables, engine, err := exportSource(cfg, options, now)
		if err != nil {
			return fmt.Errorf("catalog export failed for %s: %s", options.ID, cfg.Redact(err))
		}
		sources.names = append(sources.names, options.ID)
		sources.tables[options.ID] = tables
		engines[options.ID] = engine
	}
	if err := snapshot.WriteJSON(filepath.Join(cfg.DataDir, "programs.json"), map[string]any{"generatedAt": now, "engines": engines}); err != nil {
		return err
	}
	if err := snapshot.WriteJSON(filepath.Join(cfg.DataDir, "schema.json"), struct {
		GeneratedAt string         `json:"generatedAt"`
		Sources     orderedSources `json:"sources"`
	}{now, sources}); err != nil {
		return err
	}
	return recordSnapshot(cfg, now, sources)
}

func recordSnapshot(cfg *config.Config, now string, sources orderedSources) error {
	indexFile := filepath.Join(cfg.DataDir, "snapshots", "index.json")
	index := map[string]any{"versions": []any{}}
	if data, err := os.ReadFile(indexFile); err == nil {
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.UseNumber()
		if err := decoder.Decode(&index); err != nil {
			return fmt.Errorf("%s: %w", indexFile, err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	versions, _ := index["versions"].([]any)
	previous := map[string]any{}
	if len(versions) > 0 {
		if last, ok := versions[len(versions)-1].(map[string]any); ok {
			previous, _ = last["sources"].(map[string]any)
		}
	}
	snapshots := map[string]any{}
	changed := []string{}
	for _, name := range sources.names {
		tables := sources.tables[name]
		digest, err := snapshot.SourceHash(tables)
		if err != nil {
			return err
		}
		if err := snapshot.WriteJSON(filepath.Join(cfg.DataDir, "snapshots", name, digest+".json"), tables); err != nil {
			return err
		}
		snapshots[name] = map[string]any{"hash": digest, "tables": len(tables)}
		if old, _ := previous[name].(map[string]any); old == nil || old["hash"] != digest {
			changed = append(changed, name)
		}
	}
	if len(changed) > 0 || len(versions) == 0 {
		index["versions"] = append(versions, map[string]any{"generatedAt": now, "origin": "export", "sources": snapshots, "changed": changed})
		return snapshot.WriteJSON(indexFile, index)
	}
	return nil
}
