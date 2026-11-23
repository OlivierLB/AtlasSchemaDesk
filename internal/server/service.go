package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/OlivierLB/AtlasSchemaDesk/internal/config"
	"github.com/OlivierLB/AtlasSchemaDesk/internal/db"
	"github.com/OlivierLB/AtlasSchemaDesk/internal/model"
	"github.com/OlivierLB/AtlasSchemaDesk/internal/pyre"
	"github.com/OlivierLB/AtlasSchemaDesk/internal/sqlconsole"
)

const (
	refsCap     = 1000
	sqlMaxChars = 200_000
)

// badRequest is answered with HTTP 400.
type badRequest struct{ message string }

func (e *badRequest) Error() string { return e.message }

func invalidf(format string, args ...any) error { return &badRequest{fmt.Sprintf(format, args...)} }

// Catalog is the exported schema, reloaded when schema.json changes.
type Catalog struct {
	path     string
	mu       sync.Mutex
	modified time.Time
	tables   map[string][]model.Table
}

// NewCatalog reads schema.json lazily.
func NewCatalog(path string) *Catalog { return &Catalog{path: path} }

func (c *Catalog) refresh() error {
	info, err := os.Stat(c.path)
	if err != nil {
		return err
	}
	if c.tables != nil && info.ModTime().Equal(c.modified) {
		return nil
	}
	data, err := os.ReadFile(c.path)
	if err != nil {
		return err
	}
	var file struct {
		Sources map[string][]model.Table `json:"sources"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		return fmt.Errorf("%s: %w", c.path, err)
	}
	if file.Sources == nil {
		file.Sources = map[string][]model.Table{}
	}
	c.tables, c.modified = file.Sources, info.ModTime()
	return nil
}

// Table returns a catalog table or a 400 error.
func (c *Catalog) Table(engine, schema, name string) (model.Table, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.refresh(); err != nil {
		return model.Table{}, err
	}
	for _, table := range c.tables[engine] {
		if table.Schema == schema && table.Name == name {
			return table, nil
		}
	}
	return model.Table{}, invalidf("table inconnue du catalogue : %s %s.%s", engine, schema, name)
}

type incoming struct {
	source model.Table
	key    model.ForeignKey
}

// Incoming lists the foreign keys of other tables pointing to this table.
func (c *Catalog) Incoming(engine, schema, name string) ([]incoming, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.refresh(); err != nil {
		return nil, err
	}
	var result []incoming
	for _, table := range c.tables[engine] {
		for _, key := range table.ForeignKeys {
			if key.TargetSchema == schema && key.TargetTable == name {
				result = append(result, incoming{table, key})
			}
		}
	}
	return result, nil
}

var (
	binaryType     = regexp.MustCompile(`(?i)blob|binary|bytea`)
	numericType    = regexp.MustCompile(`(?i)^(tiny|small|medium|big)?int|^integer|^decimal|^numeric|^float|^double|^real|^serial|^bigserial|^smallserial|^bit\b|^boolean|^bool\b|^money`)
	filterOperator = pyre.MustCompile(`(?s)^(<=|>=|!=|<>|<|>|=)` + pyre.S + `*(.*)$`)
)

// parseFilter turns UI filter text into a filter: null / !null, comparison
// operators, otherwise contains (text) or equals (numbers).
func parseFilter(column model.Column, text string) *db.Filter {
	value := strings.TrimSpace(text)
	if value == "" {
		return nil
	}
	switch strings.ToLower(value) {
	case "null", "∅":
		return &db.Filter{Column: column.Name, Mode: "null"}
	case "!null", "not null", "non null":
		return &db.Filter{Column: column.Name, Mode: "notnull"}
	}
	if match := filterOperator.FindStringSubmatch(value); match != nil {
		operator, operand := match[1], match[2]
		if operator == "<>" {
			operator = "!="
		}
		if operator == "=" {
			return &db.Filter{Column: column.Name, Mode: "eq", Value: &operand}
		}
		return &db.Filter{Column: column.Name, Mode: "cmp", Op: operator, Value: &operand}
	}
	if numericType.MatchString(column.Type) {
		return &db.Filter{Column: column.Name, Mode: "eq", Value: &value}
	}
	return &db.Filter{Column: column.Name, Mode: "like", Value: &value}
}

// upstream wraps database errors answered with HTTP 502.
type upstream struct {
	message string
	txOpen  *bool
}

func (e *upstream) Error() string { return e.message }

// executor is the database surface used by the service (mocked in tests).
type executor interface {
	Transactions() map[string]bool
	Validate(engine string, statements []db.Statement) error
	Rows(engine string, query db.RowsQuery) (*db.RowsResult, error)
	Refs(engine string, refs []db.Ref, cap int) ([]db.RefCount, error)
	SQL(engine string, statements []db.Statement, params []any) (*db.SQLOutcome, error)
	End(engine, action string) (bool, error)
}

type workerExecutor struct{ worker *db.Worker }

func (w workerExecutor) Transactions() map[string]bool { return w.worker.Transactions() }

func (w workerExecutor) Validate(engine string, statements []db.Statement) error {
	database, err := w.worker.Database(engine)
	if err != nil {
		return err
	}
	return database.ValidateSQL(statements)
}

func (w workerExecutor) Rows(engine string, query db.RowsQuery) (result *db.RowsResult, err error) {
	err = w.worker.Do(engine, func(d *db.Database) error { result, err = d.Rows(query); return err })
	return result, err
}

func (w workerExecutor) Refs(engine string, refs []db.Ref, cap int) (counts []db.RefCount, err error) {
	err = w.worker.Do(engine, func(d *db.Database) error { counts, err = d.Refs(refs, cap); return err })
	return counts, err
}

func (w workerExecutor) SQL(engine string, statements []db.Statement, params []any) (outcome *db.SQLOutcome, err error) {
	err = w.worker.Do(engine, func(d *db.Database) error { outcome, err = d.SQL(statements, params); return err })
	return outcome, err
}

func (w workerExecutor) End(engine, action string) (done bool, err error) {
	err = w.worker.Do(engine, func(d *db.Database) error { done, err = d.End(action); return err })
	return done, err
}

// service implements the data and SQL endpoints over one session's executor.
type service struct {
	config  *config.Config
	catalog *Catalog
	exec    executor
}

func (s *service) fail(err error, engine string, withTx bool) error {
	failure := &upstream{message: s.config.Redact(err)}
	if withTx {
		open := s.exec.Transactions()[engine]
		failure.txOpen = &open
	}
	return failure
}

type rowsResponse struct {
	*db.RowsResult
	Filters []db.Filter `json:"filters"`
}

func (s *service) rows(request rowsRequest) (any, error) {
	table, err := s.catalog.Table(request.Engine, request.Schema, request.Table)
	if err != nil {
		return nil, err
	}
	columns := map[string]model.Column{}
	for _, column := range table.Columns {
		columns[column.Name] = column
	}
	filters := []db.Filter{}
	for name, text := range request.Filters {
		column, ok := columns[name]
		if !ok {
			return nil, invalidf("colonne inconnue : %s", name)
		}
		if filter := parseFilter(column, text); filter != nil {
			filters = append(filters, *filter)
		}
	}
	var order *db.Sort
	if request.Sort != nil {
		if _, ok := columns[request.Sort.Column]; !ok {
			return nil, invalidf("colonne de tri inconnue : %s", request.Sort.Column)
		}
		order = &db.Sort{Column: request.Sort.Column, Dir: request.Sort.Dir}
	}
	result, err := s.exec.Rows(request.Engine, db.RowsQuery{
		Schema: request.Schema, Table: request.Table, Filters: filters, Sort: order,
		Limit: request.PageSize, Offset: request.Page * request.PageSize,
	})
	if err != nil {
		return nil, s.fail(err, request.Engine, false)
	}
	return rowsResponse{result, filters}, nil
}

type refEntry struct {
	db.Ref
	db.RefCount
}

func (s *service) refs(request refsRequest) (any, error) {
	if _, err := s.catalog.Table(request.Engine, request.Schema, request.Table); err != nil {
		return nil, err
	}
	links, err := s.catalog.Incoming(request.Engine, request.Schema, request.Table)
	if err != nil {
		return nil, err
	}
	refs := []db.Ref{}
	for _, link := range links {
		value, present := request.Row[link.key.TargetColumn]
		if _, isObject := value.(map[string]any); !present || value == nil || isObject {
			continue
		}
		refs = append(refs, db.Ref{Schema: link.source.Schema, Table: link.source.Name, Column: link.key.Column, TargetColumn: link.key.TargetColumn, Value: value})
	}
	if len(refs) == 0 {
		return map[string]any{"refs": []refEntry{}}, nil
	}
	counts, err := s.exec.Refs(request.Engine, refs, refsCap)
	if err != nil {
		return nil, s.fail(err, request.Engine, false)
	}
	entries := make([]refEntry, len(refs))
	for index, ref := range refs {
		entries[index] = refEntry{ref, counts[index]}
	}
	return map[string]any{"refs": entries}, nil
}

type confirmation struct {
	Index   int      `json:"index"`
	SQL     string   `json:"sql"`
	Reasons []string `json:"reasons"`
}

type statementPreview struct {
	Kind string `json:"kind"`
	SQL  string `json:"sql"`
}

type sqlResponse struct {
	NeedsConfirmation bool           `json:"needsConfirmation"`
	Confirmations     []confirmation `json:"confirmations"`
	Results           []db.SQLResult `json:"results"`
	Error             *db.SQLFailure `json:"error,omitempty"`
	TxOpen            bool           `json:"txOpen"`
	ImplicitCommit    bool           `json:"implicitCommit"`
	Statements        any            `json:"statements"`
}

func prefix(text string, n int) string {
	runes := []rune(text)
	if len(runes) > n {
		runes = runes[:n]
	}
	return string(runes)
}

// sql runs free SQL; writes stay in an open transaction until commit/rollback.
func (s *service) sql(request sqlRequest) (any, error) {
	options, err := s.config.Connection(request.Engine)
	if err != nil {
		return nil, invalidf("base inconnue : %s", request.Engine)
	}
	if strings.TrimSpace(request.SQL) == "" {
		return nil, invalidf("requête vide")
	}
	if n := len([]rune(request.SQL)); n > sqlMaxChars {
		return nil, invalidf("requête trop longue (%d caractères, maximum %d)", n, sqlMaxChars)
	}
	var statements []db.Statement
	for _, text := range sqlconsole.Split(request.SQL, options.Driver) {
		statements = append(statements, db.Statement{SQL: text, Kind: sqlconsole.Classify(text, options.Driver)})
	}
	if len(statements) == 0 {
		return nil, invalidf("aucune instruction (seulement des commentaires ?)")
	}
	if len(request.Params) > 0 && len(statements) > 1 {
		return nil, invalidf("les paramètres ne sont acceptés que pour une seule instruction")
	}
	if err := s.exec.Validate(request.Engine, statements); err != nil {
		return nil, invalidf("%s", strings.TrimPrefix(err.Error(), db.ErrValidation.Error()+": "))
	}
	for _, statement := range statements {
		if statement.Kind == sqlconsole.Control {
			return nil, invalidf("« %s » : les transactions se gèrent avec les boutons Valider / Annuler de la console", prefix(statement.SQL, 60))
		}
	}
	open := s.exec.Transactions()[request.Engine]
	confirmations := []confirmation{}
	for index, statement := range statements {
		if reasons := sqlconsole.ConfirmationReasons(statement.SQL, options.Driver, statement.Kind, open); len(reasons) > 0 {
			confirmations = append(confirmations, confirmation{index, prefix(statement.SQL, 200), reasons})
		}
	}
	if len(confirmations) > 0 && !request.Confirmed {
		return sqlResponse{NeedsConfirmation: true, Confirmations: confirmations, Results: []db.SQLResult{}, TxOpen: open, Statements: len(statements)}, nil
	}
	outcome, err := s.exec.SQL(request.Engine, statements, request.Params)
	if err != nil {
		return nil, s.fail(err, request.Engine, true)
	}
	kinds := make([]string, len(statements))
	previews := make([]statementPreview, len(statements))
	for index, statement := range statements {
		kinds[index] = statement.Kind
		previews[index] = statementPreview{statement.Kind, prefix(statement.SQL, 500)}
	}
	state := "fermée"
	if outcome.TxOpen {
		state = "ouverte"
	}
	log.Printf("[DB Atlas] SQL %s : %d instruction(s) (%s), transaction %s", request.Engine, len(statements), strings.Join(kinds, ", "), state)
	return sqlResponse{Confirmations: []confirmation{}, Results: outcome.Results, Error: outcome.Error, TxOpen: outcome.TxOpen, ImplicitCommit: outcome.ImplicitCommit, Statements: previews}, nil
}

func (s *service) end(request engineRequest, action string) (any, error) {
	if _, err := s.config.Connection(request.Engine); err != nil {
		return nil, invalidf("base inconnue : %s", request.Engine)
	}
	done, err := s.exec.End(request.Engine, action)
	if err != nil {
		return nil, s.fail(err, request.Engine, false)
	}
	verb := "annulée"
	if action == "commit" {
		verb = "validée"
	}
	suffix := ""
	if !done {
		suffix = " (aucune transaction ouverte)"
	}
	log.Printf("[DB Atlas] SQL %s : transaction %s%s", request.Engine, verb, suffix)
	return map[string]bool{"txOpen": false, "done": done}, nil
}

// statusOf maps errors to HTTP status codes and error bodies.
func statusOf(cfg *config.Config, err error) (int, map[string]any) {
	var bad *badRequest
	var request *requestError
	var failure *upstream
	switch {
	case errors.As(err, &request):
		return http.StatusBadRequest, map[string]any{"error": request.Error()}
	case errors.As(err, &bad), errors.Is(err, db.ErrValidation):
		return http.StatusBadRequest, map[string]any{"error": cfg.Redact(err)}
	case errors.As(err, &failure):
		body := map[string]any{"error": failure.message}
		if failure.txOpen != nil {
			body["txOpen"] = *failure.txOpen
		}
		return http.StatusBadGateway, body
	}
	return http.StatusBadGateway, map[string]any{"error": cfg.Redact(err)}
}
