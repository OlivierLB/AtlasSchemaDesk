package db

import (
	"unicode/utf8"

	"github.com/OlivierLB/AtlasSchemaDesk/internal/analysis"
)

// maxCellRunes bounds the text returned for one cell.
const maxCellRunes = 2000

// BinaryCell replaces binary values, which are never sent to the browser.
type BinaryCell struct {
	Binary bool `json:"binary"`
	Length int  `json:"length"`
}

// TruncatedCell carries the beginning of a long value.
type TruncatedCell struct {
	Truncated bool   `json:"truncated"`
	Length    int    `json:"length"`
	Value     string `json:"value"`
}

// textCell returns the text, or its first 2000 characters when longer.
func textCell(text string) any {
	if length := utf8.RuneCountInString(text); length > maxCellRunes {
		runes := []rune(text)
		return TruncatedCell{Truncated: true, Length: length, Value: string(runes[:maxCellRunes])}
	}
	return text
}

// resultSet is the first result of a statement; later results are drained.
type resultSet struct {
	HasRows  bool
	Columns  []string
	Rows     [][]any
	HasMore  bool
	Affected int64
}

// driverConn is one persistent driver connection.
type driverConn interface {
	// exec runs a command and discards its results.
	exec(sql string) error
	// query runs one statement with optional "?" parameters and keeps at most
	// limit rows of its first result (limit < 0 keeps every row).
	query(sql string, params []any, limit int) (*resultSet, error)
	// catalogQuery returns every row as text values (nil for NULL).
	catalogQuery(sql string) ([]analysis.Row, error)
	// inTransaction reports the server-side transaction state.
	inTransaction() (bool, error)
	// broken reports whether the connection can no longer be used.
	broken() bool
	close() error
}
