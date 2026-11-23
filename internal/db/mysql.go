package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/go-mysql-org/go-mysql/client"
	"github.com/go-mysql-org/go-mysql/mysql"

	"github.com/OlivierLB/AtlasSchemaDesk/internal/analysis"
	"github.com/OlivierLB/AtlasSchemaDesk/internal/config"
)

const binaryCharset = 63

type mysqlConn struct {
	conn   *client.Conn
	failed bool
}

func connectMySQL(options config.Connection) (*mysqlConn, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, err := client.ConnectWithContext(ctx, net.JoinHostPort(options.Host, strconv.Itoa(options.Port)),
		options.User, options.Password, options.Database, 5*time.Second,
		func(c *client.Conn) error {
			c.ReadTimeout, c.WriteTimeout = 125*time.Second, 125*time.Second
			// CALL of a procedure returning rows needs multi-results; multi
			// statements stay disabled: the console splits scripts itself.
			for _, capability := range []uint32{mysql.CLIENT_MULTI_RESULTS, mysql.CLIENT_PS_MULTI_RESULTS} {
				if err := c.SetCapability(capability); err != nil {
					return err
				}
			}
			c.UnsetCapability(mysql.CLIENT_MULTI_STATEMENTS)
			return c.SetCollation("utf8mb4_general_ci")
		})
	if err != nil {
		return nil, err
	}
	m := &mysqlConn{conn: conn}
	if err := m.exec("SET autocommit = 1"); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return m, nil
}

// track marks the connection broken on any error that is not a server error.
func (m *mysqlConn) track(err error) error {
	var serverError *mysql.MyError
	if err != nil && !errors.As(err, &serverError) {
		m.failed = true
	}
	return err
}

func (m *mysqlConn) exec(sql string) error {
	_, err := m.query(sql, nil, 0)
	return err
}

func isBinaryField(field *mysql.Field) bool {
	if field.Charset != binaryCharset {
		return false
	}
	switch field.Type {
	case mysql.MYSQL_TYPE_TINY_BLOB, mysql.MYSQL_TYPE_MEDIUM_BLOB, mysql.MYSQL_TYPE_LONG_BLOB, mysql.MYSQL_TYPE_BLOB,
		mysql.MYSQL_TYPE_VAR_STRING, mysql.MYSQL_TYPE_STRING, mysql.MYSQL_TYPE_VARCHAR, mysql.MYSQL_TYPE_BIT, mysql.MYSQL_TYPE_GEOMETRY:
		return true
	}
	return false
}

func mysqlText(value *mysql.FieldValue) *string {
	var text string
	switch value.Type {
	case mysql.FieldValueTypeNull:
		return nil
	case mysql.FieldValueTypeUnsigned:
		text = strconv.FormatUint(value.AsUint64(), 10)
	case mysql.FieldValueTypeSigned:
		text = strconv.FormatInt(value.AsInt64(), 10)
	case mysql.FieldValueTypeFloat:
		text = strconv.FormatFloat(value.AsFloat64(), 'g', -1, 64)
	default:
		text = string(value.AsString())
	}
	return &text
}

func mysqlCell(value *mysql.FieldValue, field *mysql.Field) any {
	if value.Type == mysql.FieldValueTypeString && isBinaryField(field) {
		return BinaryCell{Binary: true, Length: len(value.AsString())}
	}
	text := mysqlText(value)
	if text == nil {
		return nil
	}
	return textCell(*text)
}

// collector converts the first result; an error in any result fails the
// statement (like reading the next result set with a DB-API cursor).
type collector struct {
	limit   int
	first   *resultSet
	failure error
}

func (c *collector) add(result *mysql.Result, err error) {
	if err != nil {
		if c.failure == nil {
			c.failure = err
		}
		return
	}
	if c.first != nil {
		return
	}
	set := &resultSet{Rows: [][]any{}}
	if result != nil {
		set.Affected = int64(result.AffectedRows)
		if result.Resultset != nil && len(result.Fields) > 0 {
			set.HasRows = true
			for _, field := range result.Fields {
				set.Columns = append(set.Columns, string(field.Name))
			}
			for _, values := range result.Values {
				if c.limit >= 0 && len(set.Rows) >= c.limit {
					set.HasMore = true
					break
				}
				row := make([]any, len(values))
				for index := range values {
					row[index] = mysqlCell(&values[index], result.Fields[index])
				}
				set.Rows = append(set.Rows, row)
			}
		}
	}
	c.first = set
}

func mysqlParams(params []any) ([]any, error) {
	values := make([]any, len(params))
	for index, param := range params {
		switch value := param.(type) {
		case nil, string, bool, int64, float64:
			values[index] = value
		case int:
			values[index] = int64(value)
		case json.Number:
			if integer, err := value.Int64(); err == nil {
				values[index] = integer
			} else if float, err := value.Float64(); err == nil {
				values[index] = float
			} else {
				values[index] = value.String()
			}
		default:
			return nil, fmt.Errorf("unsupported parameter type %T", param)
		}
	}
	return values, nil
}

func (m *mysqlConn) query(sql string, params []any, limit int) (*resultSet, error) {
	collect := &collector{limit: limit}
	if len(params) == 0 {
		if _, err := m.conn.ExecuteMultiple(sql, collect.add); err != nil {
			return nil, m.track(err)
		}
	} else {
		values, err := mysqlParams(params)
		if err != nil {
			return nil, err
		}
		statement, err := m.conn.Prepare(sql)
		if err != nil {
			return nil, m.track(err)
		}
		err = statement.ExecuteProcedureMultiResults(func(result *mysql.Result, err error) error {
			collect.add(result, err)
			return nil
		}, values...)
		closeErr := statement.Close()
		if err != nil {
			return nil, m.track(err)
		}
		if closeErr != nil {
			return nil, m.track(closeErr)
		}
	}
	if collect.failure != nil {
		return nil, m.track(collect.failure)
	}
	if collect.first == nil {
		collect.first = &resultSet{Rows: [][]any{}}
	}
	return collect.first, nil
}

func (m *mysqlConn) catalogQuery(sql string) ([]analysis.Row, error) {
	result, err := m.conn.Execute(sql)
	if err != nil {
		return nil, m.track(err)
	}
	var rows []analysis.Row
	for _, values := range result.Values {
		row := analysis.Row{}
		for index := range values {
			if text := mysqlText(&values[index]); text != nil {
				row[string(result.Fields[index].Name)] = *text
			} else {
				row[string(result.Fields[index].Name)] = nil
			}
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// inTransaction refreshes the server status first: an error packet does not
// carry it, and a failed DDL can still have committed the previous transaction.
func (m *mysqlConn) inTransaction() (bool, error) {
	if _, err := m.conn.Execute("DO 0"); err != nil {
		return false, m.track(err)
	}
	return m.conn.IsInTransaction(), nil
}

func (m *mysqlConn) broken() bool { return m.failed }

func (m *mysqlConn) close() error { return m.conn.Close() }
