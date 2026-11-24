// Package db owns persistent driver connections, one per configured source and
// browser session, serialized by the caller.
package db

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/OlivierLB/Sulcata/internal/analysis"
	"github.com/OlivierLB/Sulcata/internal/config"
	"github.com/OlivierLB/Sulcata/internal/pyre"
	"github.com/OlivierLB/Sulcata/internal/sqlconsole"
)

// ErrValidation marks errors caused by the request rather than the database.
var ErrValidation = errors.New("validation")

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{ErrValidation}, args...)...)
}

// Statement is one classified console statement.
type Statement struct {
	SQL  string `json:"sql"`
	Kind string `json:"kind"`
}

// Filter is a validated data-grid filter.
type Filter struct {
	Column string  `json:"column"`
	Mode   string  `json:"mode"`
	Op     string  `json:"op,omitempty"`
	Value  *string `json:"value,omitempty"`
}

// Sort orders a data grid.
type Sort struct {
	Column string `json:"column"`
	Dir    string `json:"dir"`
}

// RowsQuery reads one page of a table.
type RowsQuery struct {
	Schema, Table string
	Filters       []Filter
	Sort          *Sort
	Limit, Offset int
}

// RowsResult is one page of a table.
type RowsResult struct {
	Rows       []map[string]any `json:"rows"`
	HasMore    bool             `json:"hasMore"`
	DurationMs int64            `json:"durationMs"`
	SQL        string           `json:"sql"`
}

// Ref asks how many rows of a table reference a value.
type Ref struct {
	Schema       string `json:"schema"`
	Table        string `json:"table"`
	Column       string `json:"column"`
	TargetColumn string `json:"targetColumn"`
	Value        any    `json:"value"`
}

// RefCount answers one Ref.
type RefCount struct {
	Count  *int    `json:"count,omitempty"`
	Capped *bool   `json:"capped,omitempty"`
	Error  *string `json:"error,omitempty"`
}

// SQLResult is the outcome of one console statement.
type SQLResult struct {
	Index      int       `json:"index"`
	Kind       string    `json:"kind"`
	Columns    *[]string `json:"columns,omitempty"`
	Rows       *[][]any  `json:"rows,omitempty"`
	HasMore    *bool     `json:"hasMore,omitempty"`
	Affected   *int64    `json:"affected,omitempty"`
	DurationMs int64     `json:"durationMs"`
}

// SQLFailure locates the statement that failed.
type SQLFailure struct {
	Index   int    `json:"index"`
	Message string `json:"message"`
}

// SQLOutcome is the outcome of a console script.
type SQLOutcome struct {
	Results        []SQLResult
	Error          *SQLFailure
	TxOpen         bool
	ImplicitCommit bool
}

// Database is one persistent connection. Callers must hold Mu.
type Database struct {
	Mu      sync.Mutex
	config  *config.Config
	options config.Connection
	conn    driverConn
	tx      atomic.Bool
}

// NewDatabase prepares a lazily connected database.
func NewDatabase(cfg *config.Config, options config.Connection) *Database {
	return &Database{config: cfg, options: options}
}

// Options returns the connection settings.
func (d *Database) Options() config.Connection { return d.options }

// InTransaction reports whether this console opened a transaction.
func (d *Database) InTransaction() bool { return d.tx.Load() }

// SetTransaction overrides the transaction flag (tests).
func (d *Database) SetTransaction(open bool) { d.tx.Store(open) }

func (d *Database) pg() bool { return d.options.IsPostgres() }

func (d *Database) connect() (driverConn, error) {
	if d.conn != nil {
		return d.conn, nil
	}
	var conn driverConn
	o := d.options
	if d.pg() {
		pg, err := connectPostgres(o)
		if err != nil {
			return nil, err
		}
		conn = pg
	} else {
		my, err := connectMySQL(o)
		if err != nil {
			return nil, err
		}
		conn = my
	}
	setup := func() error {
		if !o.AllowWrites {
			if err := conn.exec(d.readOnlyCommand()); err != nil {
				return err
			}
		}
		if err := conn.exec(d.timeoutCommand(15000)); err != nil {
			return err
		}
		if pg, ok := conn.(*postgres); ok {
			return pg.setSearchPath(d.Quote(o.DefaultSchema))
		}
		return nil
	}
	if err := setup(); err != nil {
		_ = conn.close()
		return nil, err
	}
	d.conn = conn
	return conn, nil
}

func (d *Database) readOnlyCommand() string {
	if d.pg() {
		return "SET SESSION CHARACTERISTICS AS TRANSACTION READ ONLY"
	}
	return "SET SESSION TRANSACTION READ ONLY"
}

func (d *Database) timeoutCommand(milliseconds int) string {
	if d.pg() {
		return "SET statement_timeout = " + strconv.Itoa(milliseconds)
	}
	return "SET SESSION max_execution_time = " + strconv.Itoa(milliseconds)
}

// Quote quotes an identifier for the connection's dialect.
func (d *Database) Quote(name string) string {
	mark := "`"
	if d.pg() {
		mark = `"`
	}
	return mark + strings.ReplaceAll(name, mark, mark+mark) + mark
}

func (d *Database) command(sql string) error {
	conn, err := d.connect()
	if err != nil {
		return err
	}
	return conn.exec(sql)
}

func (d *Database) query(sql string, params []any, limit int) (*resultSet, error) {
	conn, err := d.connect()
	if err != nil {
		return nil, err
	}
	return conn.query(sql, params, limit)
}

// Query runs a read and returns every row as text (catalog export, checks).
func (d *Database) Query(sql string) ([]analysis.Row, error) {
	conn, err := d.connect()
	if err != nil {
		return nil, err
	}
	return conn.catalogQuery(sql)
}

// IsPostgres reports the dialect.
func (d *Database) IsPostgres() bool { return d.pg() }

// enforceReadOnly restores the read-only default before every operation: read
// expressions can call functions that change session defaults.
func (d *Database) enforceReadOnly() error {
	if d.options.AllowWrites {
		return nil
	}
	return d.command(d.readOnlyCommand())
}

// guarded protects an open PostgreSQL transaction from a failing read.
func (d *Database) guarded(run func() error) error {
	if !d.pg() || !d.tx.Load() {
		return run()
	}
	if err := d.command("SAVEPOINT sulcata_read"); err != nil {
		return err
	}
	err := run()
	if err != nil {
		if rollback := d.command("ROLLBACK TO SAVEPOINT sulcata_read"); rollback != nil {
			return errors.Join(err, rollback)
		}
	}
	if release := d.command("RELEASE SAVEPOINT sulcata_read"); release != nil && err == nil {
		return release
	}
	return err
}

func elapsed(started time.Time) int64 {
	return int64(math.RoundToEven(float64(time.Since(started)) / float64(time.Millisecond)))
}

var comparisons = map[string]bool{"=": true, "<": true, ">": true, "<=": true, ">=": true, "!=": true}

// Rows reads one page of a table with validated filters and sort.
func (d *Database) Rows(query RowsQuery) (*RowsResult, error) {
	if err := d.enforceReadOnly(); err != nil {
		return nil, err
	}
	q := d.Quote
	sql := "SELECT * FROM " + q(query.Schema) + "." + q(query.Table)
	var clauses []string
	var params []any
	for _, filter := range query.Filters {
		column := q(filter.Column)
		switch filter.Mode {
		case "null":
			clauses = append(clauses, column+" IS NULL")
			continue
		case "notnull":
			clauses = append(clauses, column+" IS NOT NULL")
			continue
		case "like":
			// An explicit portable escape character, independent of the SQL mode.
			if d.pg() {
				clauses = append(clauses, "CAST("+column+" AS TEXT) ILIKE ? ESCAPE '!'")
			} else {
				clauses = append(clauses, column+" LIKE ? ESCAPE '!'")
			}
			escaped := strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(value(filter))
			params = append(params, "%"+escaped+"%")
		default:
			op := filter.Op
			if op == "" {
				op = "="
			}
			if !comparisons[op] {
				return nil, invalid("invalid comparison operator")
			}
			clauses = append(clauses, column+" "+op+" ?")
			params = append(params, value(filter))
		}
	}
	if len(clauses) > 0 {
		sql += " WHERE " + strings.Join(clauses, " AND ")
	}
	if query.Sort != nil {
		direction := " ASC"
		if query.Sort.Dir == "desc" {
			direction = " DESC"
		}
		sql += " ORDER BY " + q(query.Sort.Column) + direction
	}
	limit := max(1, min(500, query.Limit))
	sql += fmt.Sprintf(" LIMIT %d OFFSET %d", limit+1, max(0, query.Offset))
	started := time.Now()
	var result *resultSet
	err := d.guarded(func() error {
		var err error
		result, err = d.query(sql, params, limit+1)
		return err
	})
	if err != nil {
		return nil, err
	}
	rows := make([]map[string]any, 0, min(limit, len(result.Rows)))
	for index, values := range result.Rows {
		if index >= limit {
			break
		}
		row := make(map[string]any, len(values))
		for column, cell := range values {
			row[result.Columns[column]] = cell
		}
		rows = append(rows, row)
	}
	return &RowsResult{Rows: rows, HasMore: len(result.Rows) > limit, DurationMs: elapsed(started), SQL: sql}, nil
}

func value(filter Filter) string {
	if filter.Value == nil {
		return ""
	}
	return *filter.Value
}

// Refs counts referencing rows, capped; one failing reference does not fail the others.
func (d *Database) Refs(refs []Ref, cap int) ([]RefCount, error) {
	if err := d.enforceReadOnly(); err != nil {
		return nil, err
	}
	cap = max(1, min(1000, cap))
	counts := make([]RefCount, 0, len(refs))
	for _, ref := range refs {
		sql := fmt.Sprintf("SELECT COUNT(*) AS n FROM (SELECT 1 FROM %s.%s WHERE %s = ? LIMIT %d) counted",
			d.Quote(ref.Schema), d.Quote(ref.Table), d.Quote(ref.Column), cap+1)
		var n int
		err := d.guarded(func() error {
			result, err := d.query(sql, []any{ref.Value}, -1)
			if err != nil {
				return err
			}
			if len(result.Rows) != 1 || len(result.Rows[0]) != 1 {
				return errors.New("unexpected COUNT result")
			}
			text, _ := result.Rows[0][0].(string)
			n, err = strconv.Atoi(text)
			return err
		})
		if err != nil {
			if d.conn != nil && d.conn.broken() {
				return nil, err
			}
			message := d.config.Redact(err)
			counts = append(counts, RefCount{Error: &message})
			continue
		}
		count, capped := min(n, cap), n > cap
		counts = append(counts, RefCount{Count: &count, Capped: &capped})
	}
	return counts, nil
}

var (
	useStatement     = pyre.MustCompile(`(?i)^` + pyre.S + `*USE` + pyre.S + "+[`\"" + pyre.Word + `]+` + pyre.S + `*;?` + pyre.S + `*$`)
	executableNotice = pyre.MustCompile(`/\*` + pyre.S + `*!`)
	intoKeyword      = regexp.MustCompile(`(?i)\bINTO\b`)
)

// ValidateSQL refuses transaction control and, on read-only connections, any
// statement that could write or change the session.
func (d *Database) ValidateSQL(statements []Statement) error {
	for _, item := range statements {
		kind := sqlconsole.Classify(item.SQL, d.options.Driver)
		if kind == sqlconsole.Control {
			return invalid("use the commit and rollback buttons for transactions")
		}
		// Session commands can disable read-only mode, invoke prepared writes or
		// change connection state. Only schema selection is allowed in read-only mode.
		if d.options.AllowWrites {
			continue
		}
		masked := sqlconsole.MaskLiterals(item.SQL, d.options.Driver)
		if kind != sqlconsole.Read && kind != sqlconsole.Session {
			return invalid("this connection is read-only")
		}
		if kind == sqlconsole.Session && !useStatement.MatchString(item.SQL) {
			return invalid("session changes are disabled on a read-only connection")
		}
		// MySQL executable comments can contain hidden writes or SET commands.
		if executableNotice.MatchString(item.SQL) || intoKeyword.MatchString(masked) {
			return invalid("this statement is not allowed on a read-only connection")
		}
	}
	return nil
}

// SQL runs a console script. Writes stay in an open transaction until End.
func (d *Database) SQL(statements []Statement, params []any) (outcome *SQLOutcome, err error) {
	if err := d.ValidateSQL(statements); err != nil {
		return nil, err
	}
	outcome = &SQLOutcome{Results: []SQLResult{}}
	if err := d.command(d.timeoutCommand(120000)); err != nil {
		return nil, err
	}
	defer func() {
		if reset := d.command(d.timeoutCommand(15000)); reset != nil && err == nil {
			outcome, err = nil, reset
		}
	}()
	for index, item := range statements {
		if err := d.enforceReadOnly(); err != nil {
			return nil, err
		}
		kind := sqlconsole.Classify(item.SQL, d.options.Driver)
		mysqlDDL := !d.pg() && kind == sqlconsole.DDL
		wasOpen := d.tx.Load()
		if !d.tx.Load() && !mysqlDDL && (kind == sqlconsole.Write || kind == sqlconsole.DDL) {
			begin := "START TRANSACTION"
			if d.pg() {
				begin = "BEGIN"
			}
			if err := d.command(begin); err != nil {
				return nil, err
			}
			d.tx.Store(true)
		}
		save := d.pg() && d.tx.Load()
		if save {
			if err := d.command("SAVEPOINT sulcata_statement"); err != nil {
				return nil, err
			}
		}
		var statementParams []any
		if len(statements) == 1 {
			statementParams = params
		}
		started := time.Now()
		result, runErr := d.query(item.SQL, statementParams, 500)
		if runErr == nil {
			entry := SQLResult{Index: index, Kind: kind, DurationMs: elapsed(started)}
			if result.HasRows {
				hasMore := result.HasMore
				columns := result.Columns
				if columns == nil {
					columns = []string{}
				}
				entry.Columns, entry.Rows, entry.HasMore = &columns, &result.Rows, &hasMore
			} else {
				affected := result.Affected
				entry.Affected = &affected
			}
			outcome.Results = append(outcome.Results, entry)
			if save {
				if err := d.command("RELEASE SAVEPOINT sulcata_statement"); err != nil {
					return nil, err
				}
			}
		} else {
			if d.conn != nil && d.conn.broken() {
				return nil, runErr
			}
			if save {
				if err := d.command("ROLLBACK TO SAVEPOINT sulcata_statement"); err != nil {
					return nil, err
				}
				if err := d.command("RELEASE SAVEPOINT sulcata_statement"); err != nil {
					return nil, err
				}
			}
			outcome.Error = &SQLFailure{Index: index, Message: d.config.Redact(runErr)}
		}
		if !d.pg() {
			actual, err := d.conn.inTransaction()
			if err != nil {
				return nil, err
			}
			outcome.ImplicitCommit = outcome.ImplicitCommit || (wasOpen && !actual)
			d.tx.Store(actual)
		}
		if outcome.Error != nil {
			break
		}
	}
	outcome.TxOpen = d.tx.Load()
	return outcome, nil
}

// End commits or rolls back the console transaction; done is false when none was open.
func (d *Database) End(action string) (done bool, err error) {
	if !d.tx.Load() {
		return false, nil
	}
	command := "ROLLBACK"
	if action == "commit" {
		command = "COMMIT"
	}
	if err := d.command(command); err != nil {
		return false, err
	}
	d.tx.Store(false)
	return true, nil
}

// Broken reports whether the driver connection was lost.
func (d *Database) Broken() bool { return d.conn != nil && d.conn.broken() }

// Close closes the connection; an uncommitted transaction is rolled back by the server.
func (d *Database) Close() {
	if d.conn != nil {
		_ = d.conn.close()
	}
	d.conn = nil
	d.tx.Store(false)
}
