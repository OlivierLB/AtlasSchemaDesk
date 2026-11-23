package server

// The frontend consumes frontend/openapi.json (types generated into
// src/api.generated.ts). These tests keep the Go transport faithful to it.

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/OlivierLB/AtlasSchemaDesk/internal/db"
)

type openAPI struct {
	Paths      map[string]map[string]any `json:"paths"`
	Components struct {
		Schemas map[string]schema `json:"schemas"`
	} `json:"components"`
}

type schema struct {
	Properties map[string]json.RawMessage `json:"properties"`
	Required   []string                   `json:"required"`
}

func loadContract(t *testing.T) openAPI {
	data, err := os.ReadFile(filepath.Join("..", "..", "frontend", "openapi.json"))
	if err != nil {
		t.Fatal(err)
	}
	var contract openAPI
	if err := json.Unmarshal(data, &contract); err != nil {
		t.Fatal(err)
	}
	return contract
}

func TestEveryContractRouteIsServed(t *testing.T) {
	contract := loadContract(t)
	h := newHarness(t)
	for path, methods := range contract.Paths {
		for method := range methods {
			concrete := strings.ReplaceAll(path, "{job_id}", "unknown")
			status, body := h.api(strings.ToUpper(method), concrete, "{}")
			if status == http.StatusMethodNotAllowed || body == `{"error":"Not Found"}` {
				t.Errorf("%s %s is not served: %d %s", method, path, status, body)
			}
		}
	}
}

// checkShape verifies that a response has every required field and no
// undeclared one.
func checkShape(t *testing.T, contract openAPI, name, body string) {
	t.Helper()
	declared, ok := contract.Components.Schemas[name]
	if !ok {
		t.Fatalf("schema %s missing from the contract", name)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &object); err != nil {
		t.Fatalf("%s: %v (%s)", name, err, body)
	}
	for _, field := range declared.Required {
		if _, present := object[field]; !present {
			t.Errorf("%s: required field %q missing in %s", name, field, body)
		}
	}
	var extra []string
	for field := range object {
		if _, known := declared.Properties[field]; !known {
			extra = append(extra, field)
		}
	}
	sort.Strings(extra)
	if len(extra) > 0 {
		t.Errorf("%s: undeclared fields %v", name, extra)
	}
}

func TestResponsesMatchContractSchemas(t *testing.T) {
	contract := loadContract(t)
	h := newHarness(t)
	_, body := h.do("GET", "/api/config", "", nil)
	checkShape(t, contract, "PublicConfig", body)
	_, body = h.do("GET", "/api/connections", "", nil)
	checkShape(t, contract, "ConnectionsOverview", body)
	var overview struct {
		Connections []json.RawMessage `json:"connections"`
	}
	_ = json.Unmarshal([]byte(body), &overview)
	checkShape(t, contract, "ConnectionDetail", string(overview.Connections[0]))
	if strings.Contains(body, "unit-secret") {
		t.Fatalf("connections overview leaks the password: %s", body)
	}
	_, body = h.do("POST", "/api/sessions", "{}", map[string]string{"X-DB-Atlas": "1"})
	checkShape(t, contract, "NewSession", body)
	_, body = h.api("GET", "/api/sessions/current", "")
	checkShape(t, contract, "SessionStatus", body)
	_, body = h.api("GET", "/api/status", "")
	checkShape(t, contract, "Status", body)
	var status map[string]json.RawMessage
	_ = json.Unmarshal([]byte(body), &status)
	checkShape(t, contract, "JobStatus", string(status["s3"]))

	fake := &fakeExecutor{transactions: map[string]bool{"library": false}}
	h.app.newExecutor = func(*db.Worker) executor { return fake }
	columns, rows, hasMore, affected := []string{"n"}, [][]any{{"1"}}, false, int64(2)
	fake.sql = func() (*db.SQLOutcome, error) {
		return &db.SQLOutcome{Results: []db.SQLResult{
			{Index: 0, Kind: "read", Columns: &columns, Rows: &rows, HasMore: &hasMore, DurationMs: 1},
			{Index: 1, Kind: "read", Affected: &affected, DurationMs: 1},
		}, Error: &db.SQLFailure{Index: 1, Message: "x"}}, nil
	}
	_, body = h.api("POST", "/api/sql", `{"engine":"library","sql":"SELECT 1"}`)
	checkShape(t, contract, "SqlResponse", body)
	var response struct {
		Results []json.RawMessage `json:"results"`
		Error   json.RawMessage   `json:"error"`
	}
	_ = json.Unmarshal([]byte(body), &response)
	for _, result := range response.Results {
		checkShape(t, contract, "SqlResult", string(result))
	}
	checkShape(t, contract, "SqlError", string(response.Error))
	_, body = h.api("POST", "/api/sql", `{"engine":"library","sql":"DELETE FROM t"}`)
	checkShape(t, contract, "SqlResponse", body)
	_, body = h.api("POST", "/api/sql/commit", `{"engine":"library"}`)
	checkShape(t, contract, "EndResponse", body)
	_, body = h.api("POST", "/api/sql", `{"engine":"library"}`)
	checkShape(t, contract, "ErrorResponse", body)
}
