package db

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/OlivierLB/AtlasSchemaDesk/internal/analysis"
	"github.com/OlivierLB/AtlasSchemaDesk/internal/config"
	"github.com/OlivierLB/AtlasSchemaDesk/internal/sqlconsole"
)

const (
	oidBool  = 16
	oidBytea = 17
)

type postgres struct {
	conn *pgconn.PgConn
}

func quoteKeyword(value string) string {
	return "'" + strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(value) + "'"
}

func connectPostgres(options config.Connection) (*postgres, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	dsn := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s connect_timeout=5",
		quoteKeyword(options.Host), options.Port, quoteKeyword(options.User), quoteKeyword(options.Password), quoteKeyword(options.Database))
	conn, err := pgconn.Connect(ctx, dsn)
	if err != nil {
		return nil, err
	}
	return &postgres{conn}, nil
}

func (p *postgres) exec(sql string) error {
	return p.conn.Exec(context.Background(), sql).Close()
}

func textParams(params []any) ([][]byte, error) {
	values := make([][]byte, len(params))
	for index, param := range params {
		switch value := param.(type) {
		case nil:
			values[index] = nil
		case string:
			values[index] = []byte(value)
		case bool:
			values[index] = []byte(strconv.FormatBool(value))
		case json.Number:
			values[index] = []byte(value.String())
		case int64:
			values[index] = []byte(strconv.FormatInt(value, 10))
		case int:
			values[index] = []byte(strconv.Itoa(value))
		case float64:
			values[index] = []byte(strconv.FormatFloat(value, 'g', -1, 64))
		default:
			return nil, fmt.Errorf("unsupported parameter type %T", param)
		}
	}
	return values, nil
}

// rowsAffected mirrors DB-API rowcount: -1 when the command reports no count.
func rowsAffected(tag pgconn.CommandTag) int64 {
	fields := strings.Fields(tag.String())
	if len(fields) == 0 {
		return -1
	}
	count, err := strconv.ParseInt(fields[len(fields)-1], 10, 64)
	if err != nil {
		return -1
	}
	return count
}

func pgCell(value []byte, oid uint32) any {
	if value == nil {
		return nil
	}
	switch oid {
	case oidBool:
		if string(value) == "t" {
			return "true"
		}
		return "false"
	case oidBytea:
		length := len(value)
		if strings.HasPrefix(string(value), `\x`) {
			length = (len(value) - 2) / 2
		}
		return BinaryCell{Binary: true, Length: length}
	}
	return textCell(string(value))
}

// read consumes one result: keeps up to limit rows (all when limit < 0), drains the rest.
func readResult(reader *pgconn.ResultReader, limit int) (*resultSet, error) {
	fields := reader.FieldDescriptions()
	result := &resultSet{HasRows: len(fields) > 0, Rows: [][]any{}}
	for _, field := range fields {
		result.Columns = append(result.Columns, field.Name)
	}
	for reader.NextRow() {
		if limit >= 0 && len(result.Rows) >= limit {
			result.HasMore = true
			continue
		}
		raw := reader.Values()
		row := make([]any, len(raw))
		for index, value := range raw {
			row[index] = pgCell(value, fields[index].DataTypeOID)
		}
		result.Rows = append(result.Rows, row)
	}
	tag, err := reader.Close()
	if err != nil {
		return nil, err
	}
	result.Affected = rowsAffected(tag)
	return result, nil
}

func (p *postgres) query(sql string, params []any, limit int) (*resultSet, error) {
	ctx := context.Background()
	if len(params) > 0 {
		values, err := textParams(params)
		if err != nil {
			return nil, err
		}
		return readResult(p.conn.ExecParams(ctx, sqlconsole.Placeholders(sql, "postgresql"), values, nil, nil, nil), limit)
	}
	multi := p.conn.Exec(ctx, sql)
	var first *resultSet
	var failure error
	for multi.NextResult() {
		result, err := readResult(multi.ResultReader(), limit)
		if err != nil && failure == nil {
			failure = err
		}
		if first == nil && err == nil {
			first = result
		}
	}
	if err := multi.Close(); err != nil {
		return nil, err
	}
	if failure != nil {
		return nil, failure
	}
	if first == nil {
		first = &resultSet{Rows: [][]any{}, Affected: -1}
	}
	return first, nil
}

func (p *postgres) catalogQuery(sql string) ([]analysis.Row, error) {
	reader := p.conn.ExecParams(context.Background(), sql, nil, nil, nil, nil)
	fields := reader.FieldDescriptions()
	var rows []analysis.Row
	for reader.NextRow() {
		row := analysis.Row{}
		for index, value := range reader.Values() {
			if value == nil {
				row[fields[index].Name] = nil
			} else {
				row[fields[index].Name] = string(value)
			}
		}
		rows = append(rows, row)
	}
	_, err := reader.Close()
	return rows, err
}

func (p *postgres) setSearchPath(schema string) error {
	_, err := p.conn.ExecParams(context.Background(), "SELECT set_config('search_path', $1, false)",
		[][]byte{[]byte(schema)}, nil, nil, nil).Close()
	return err
}

func (p *postgres) inTransaction() (bool, error) { return p.conn.TxStatus() != 'I', nil }

func (p *postgres) broken() bool { return p.conn.IsClosed() }

func (p *postgres) close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return p.conn.Close(ctx)
}
