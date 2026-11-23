// Package model defines the catalog files shared with the frontend
// (schema.json, programs.json). Field order matches the historical output.
package model

import (
	"bytes"
	"encoding/json"
)

// Column of a table.
type Column struct {
	Name     string  `json:"name"`
	Type     string  `json:"type"`
	Nullable bool    `json:"nullable"`
	Default  *string `json:"default"`
	Primary  bool    `json:"primary"`
}

// ForeignKey is one column of a foreign key constraint.
type ForeignKey struct {
	Column       string `json:"column"`
	TargetSchema string `json:"targetSchema"`
	TargetTable  string `json:"targetTable"`
	TargetColumn string `json:"targetColumn"`
}

// Table of the exported schema.
type Table struct {
	Schema            string       `json:"schema"`
	Name              string       `json:"name"`
	Columns           []Column     `json:"columns"`
	ForeignKeys       []ForeignKey `json:"foreignKeys"`
	UniqueConstraints [][]string   `json:"uniqueConstraints"`
}

// Ref names a relation or a function.
type Ref struct {
	Schema string `json:"schema"`
	Name   string `json:"name"`
}

// Impact summarizes what a stored program reads, writes and calls.
type Impact struct {
	Writes         []string `json:"writes"`
	Reads          []string `json:"reads"`
	Calls          []string `json:"calls"`
	DynamicSQL     bool     `json:"dynamicSql"`
	ProbableWrites []string `json:"probableWrites"`
	ProbableReads  []string `json:"probableReads"`
	IndirectWrites []string `json:"indirectWrites"`
	CalledBy       []string `json:"calledBy"`
}

// Source links a program to a file of the routines repository.
type Source struct {
	Status       string `json:"status"`
	Path         string `json:"path,omitempty"`
	AbsolutePath string `json:"absolutePath,omitempty"`
	Line         int    `json:"line,omitempty"`
}

// Layout records which catalog produced a program, which fixes its JSON keys.
type Layout int

// Program layouts.
const (
	PGFunction Layout = iota
	PGTrigger
	View
	MySQLRoutine
	MySQLTrigger
	MySQLEvent
)

// Program is a stored function, procedure, trigger, view or event.
type Program struct {
	Layout        Layout
	Kind          string
	Schema        string
	Name          string
	Signature     string
	Returns       *string
	Language      string
	LastAltered   *string
	Comment       *string
	Table         *Ref
	Timing        *string
	Events        []string
	Function      *Ref
	Status        string
	Schedule      string
	Starts        *string
	LastExecuted  *string
	EventSchedule *EventSchedule
	Body          string
	ID            string
	Impact        *Impact
	Source        *Source
}

// EventSchedule preserves machine-readable timing from INFORMATION_SCHEMA.EVENTS.
// Datetimes without an offset are expressed in the event's own time zone.
type EventSchedule struct {
	Type           string  `json:"type"`
	TimeZone       string  `json:"timeZone"`
	SystemTimeZone string  `json:"systemTimeZone"`
	Scheduler      string  `json:"scheduler"`
	ExecuteAt      *string `json:"executeAt"`
	Starts         *string `json:"starts"`
	Ends           *string `json:"ends"`
	Created        *string `json:"created"`
	IntervalValue  *string `json:"intervalValue"`
	IntervalField  *string `json:"intervalField"`
}

// MarshalJSON writes the keys each layout historically produced, in order.
func (p *Program) MarshalJSON() ([]byte, error) {
	object := &orderedObject{}
	object.add("kind", p.Kind)
	object.add("schema", p.Schema)
	object.add("name", p.Name)
	switch p.Layout {
	case PGFunction:
		object.add("signature", p.Signature)
		object.add("returns", p.Returns)
		object.add("language", p.Language)
	case MySQLRoutine:
		object.add("signature", p.Signature)
		object.add("returns", p.Returns)
		object.add("lastAltered", p.LastAltered)
		object.add("comment", p.Comment)
	case PGTrigger, MySQLTrigger:
		object.add("table", p.Table)
		object.add("timing", p.Timing)
		object.add("events", nonNil(p.Events))
		if p.Layout == PGTrigger {
			object.add("function", p.Function)
		}
	case MySQLEvent:
		object.add("status", p.Status)
		object.add("schedule", p.Schedule)
		object.add("starts", p.Starts)
		object.add("lastExecuted", p.LastExecuted)
		if p.EventSchedule != nil {
			object.add("eventSchedule", p.EventSchedule)
		}
	}
	object.add("body", p.Body)
	if p.ID != "" {
		object.add("id", p.ID)
	}
	if p.Impact != nil {
		object.add("impact", p.Impact)
	}
	if p.Source != nil {
		object.add("source", p.Source)
	}
	return object.bytes()
}

// IndexEntry lists the programs touching one relation.
type IndexEntry struct {
	Writes         []string `json:"writes"`
	Reads          []string `json:"reads"`
	Triggers       []string `json:"triggers"`
	IndirectWrites []string `json:"indirectWrites"`
	ProbableWrites []string `json:"probableWrites"`
	ProbableReads  []string `json:"probableReads"`
}

// TableStat holds catalog estimates (no table data is read).
type TableStat struct {
	Rows  *int64 `json:"rows"`
	Bytes *int64 `json:"bytes"`
}

// Repository summarizes the routines repository match.
type Repository struct {
	Available bool           `json:"available"`
	Counts    map[string]int `json:"counts"`
}

// ErrorMessage is a redacted failure.
type ErrorMessage struct {
	Message string `json:"message"`
}

// Engine is the programs.json entry of one connection.
type Engine struct {
	GeneratedAt      string                 `json:"generatedAt"`
	Programs         []*Program             `json:"programs"`
	TableImpacts     map[string]*IndexEntry `json:"tableImpacts"`
	TableStats       map[string]TableStat   `json:"tableStats"`
	Error            *ErrorMessage          `json:"error"`
	SourceRepository *Repository            `json:"sourceRepository,omitempty"`
}

type orderedObject struct {
	buffer bytes.Buffer
	err    error
}

func (o *orderedObject) add(key string, value any) {
	if o.err != nil {
		return
	}
	if o.buffer.Len() == 0 {
		o.buffer.WriteByte('{')
	} else {
		o.buffer.WriteByte(',')
	}
	encoded, err := Marshal(key)
	if err == nil {
		o.buffer.Write(encoded)
		o.buffer.WriteByte(':')
		encoded, err = Marshal(value)
		o.buffer.Write(encoded)
	}
	o.err = err
}

func (o *orderedObject) bytes() ([]byte, error) {
	if o.buffer.Len() == 0 {
		o.buffer.WriteByte('{')
	}
	o.buffer.WriteByte('}')
	return o.buffer.Bytes(), o.err
}

func nonNil(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

// Marshal encodes compact JSON without HTML escaping (like Python's json.dumps
// with ensure_ascii=False).
func Marshal(value any) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buffer.Bytes(), "\n"), nil
}
