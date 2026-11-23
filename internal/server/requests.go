package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

// requestError lists the invalid locations, like FastAPI's validation errors.
type requestError struct{ fields []string }

func (e *requestError) Error() string {
	return "Requête invalide : " + strings.Join(e.fields, ", ")
}

// object decodes a JSON object body field by field, rejecting unknown fields.
type object struct {
	fields  map[string]json.RawMessage
	invalid []string
	known   map[string]bool
}

func parseObject(body []byte) (*object, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var fields map[string]json.RawMessage
	if err := decoder.Decode(&fields); err != nil || fields == nil {
		return nil, &requestError{[]string{"body"}}
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, &requestError{[]string{"body"}}
	}
	return &object{fields: fields, known: map[string]bool{}}, nil
}

func (o *object) fail(name string) { o.invalid = append(o.invalid, "body."+name) }

func (o *object) raw(name string) (json.RawMessage, bool) {
	o.known[name] = true
	value, present := o.fields[name]
	return value, present
}

func decodeStrict(raw json.RawMessage, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	return decoder.Decode(target)
}

func (o *object) str(name string, required bool, fallback string) string {
	raw, present := o.raw(name)
	if !present {
		if required {
			o.fail(name)
		}
		return fallback
	}
	var value string
	if raw == nil || string(raw) == "null" || decodeStrict(raw, &value) != nil {
		o.fail(name)
	}
	return value
}

func (o *object) integer(name string, fallback, minimum, maximum int64) int64 {
	raw, present := o.raw(name)
	if !present {
		return fallback
	}
	var number json.Number
	if decodeStrict(raw, &number) != nil {
		o.fail(name)
		return fallback
	}
	value, err := number.Int64()
	if err != nil || value < minimum || value > maximum {
		o.fail(name)
		return fallback
	}
	return value
}

func (o *object) boolean(name string, fallback bool) bool {
	raw, present := o.raw(name)
	if !present {
		return fallback
	}
	var value bool
	if decodeStrict(raw, &value) != nil {
		o.fail(name)
	}
	return value
}

func (o *object) into(name string, required, nullable bool, target any) bool {
	raw, present := o.raw(name)
	if !present {
		if required {
			o.fail(name)
		}
		return false
	}
	if string(raw) == "null" {
		if !nullable {
			o.fail(name)
		}
		return false
	}
	if decodeStrict(raw, target) != nil {
		o.fail(name)
		return false
	}
	return true
}

func (o *object) done() error {
	var extra []string
	for name := range o.fields {
		if !o.known[name] {
			extra = append(extra, "body."+name)
		}
	}
	sort.Strings(extra)
	if all := append(o.invalid, extra...); len(all) > 0 {
		return &requestError{all}
	}
	return nil
}

type engineRequest struct{ Engine string }

func parseEngine(body []byte) (engineRequest, error) {
	o, err := parseObject(body)
	if err != nil {
		return engineRequest{}, err
	}
	request := engineRequest{o.str("engine", true, "")}
	return request, o.done()
}

type sortRequest struct {
	Column string
	Dir    string
}

type sortBody struct {
	Column *string `json:"column"`
	Dir    *string `json:"dir"`
}

type rowsRequest struct {
	Engine, Schema, Table string
	Filters               map[string]string
	Sort                  *sortRequest
	PageSize, Page        int
}

func parseRows(body []byte) (rowsRequest, error) {
	o, err := parseObject(body)
	if err != nil {
		return rowsRequest{}, err
	}
	request := rowsRequest{Engine: o.str("engine", true, ""), Schema: o.str("schema", true, ""), Table: o.str("table", true, "")}
	o.into("filters", false, false, &request.Filters)
	var sort sortBody
	if o.into("sort", false, true, &sort) {
		order := sortRequest{Dir: "asc"}
		if sort.Column == nil {
			o.fail("sort.column")
		} else {
			order.Column = *sort.Column
		}
		if sort.Dir != nil {
			if *sort.Dir != "asc" && *sort.Dir != "desc" {
				o.fail("sort.dir")
			}
			order.Dir = *sort.Dir
		}
		request.Sort = &order
	}
	request.PageSize = int(o.integer("pageSize", 100, 1, 500))
	request.Page = int(o.integer("page", 0, 0, 1<<53))
	return request, o.done()
}

type refsRequest struct {
	Engine, Schema, Table string
	Row                   map[string]any
}

func parseRefs(body []byte) (refsRequest, error) {
	o, err := parseObject(body)
	if err != nil {
		return refsRequest{}, err
	}
	request := refsRequest{Engine: o.str("engine", true, ""), Schema: o.str("schema", true, ""), Table: o.str("table", true, "")}
	o.into("row", true, false, &request.Row)
	return request, o.done()
}

type sqlRequest struct {
	Engine    string
	SQL       string
	Params    []any
	Confirmed bool
}

func parseSQL(body []byte) (sqlRequest, error) {
	o, err := parseObject(body)
	if err != nil {
		return sqlRequest{}, err
	}
	request := sqlRequest{Engine: o.str("engine", true, "")}
	failures := len(o.invalid)
	request.SQL = o.str("sql", true, "")
	if length := utf8.RuneCountInString(request.SQL); len(o.invalid) == failures && (length < 1 || length > sqlMaxChars) {
		o.fail("sql")
	}
	var params []any
	if o.into("params", false, false, &params) {
		for index, value := range params {
			switch value.(type) {
			case nil, string, bool, json.Number:
			default:
				o.fail(fmt.Sprintf("params.%d", index))
			}
		}
		request.Params = params
	}
	request.Confirmed = o.boolean("confirmed", false)
	return request, o.done()
}

// RE2 caps repetitions at 1000: the 1..1024 length is checked separately.
var versionPattern = regexp.MustCompile(`^[A-Za-z0-9._+/=\-]+$`)

type applyRequest struct{ Engine, VersionID string }

func parseApply(body []byte) (applyRequest, error) {
	o, err := parseObject(body)
	if err != nil {
		return applyRequest{}, err
	}
	request := applyRequest{Engine: o.str("engine", true, ""), VersionID: o.str("versionId", true, "")}
	if _, present := o.fields["versionId"]; present && (len(request.VersionID) > 1024 || !versionPattern.MatchString(request.VersionID)) {
		o.fail("versionId")
	}
	return request, o.done()
}
