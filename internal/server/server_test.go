package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/OlivierLB/AtlasSchemaDesk/internal/config"
	"github.com/OlivierLB/AtlasSchemaDesk/internal/db"
	"github.com/OlivierLB/AtlasSchemaDesk/internal/model"
)

func configuration(root string) *config.Config {
	return &config.Config{
		Path: filepath.Join(root, "atlas.toml"),
		Connections: []config.Connection{{ID: "library", Label: "Library", Driver: "postgresql", Host: "127.0.0.1", Port: 5432,
			Database: "sample", User: "reader", Password: "unit-secret", DefaultSchema: "public", Color: "#8ab4f8"}},
		Bind: "127.0.0.1", Port: 8765, DataDir: filepath.Join(root, "data"), SessionIdleSeconds: 1800,
		ExportTimeoutSeconds: 300, DumpListTimeoutSeconds: 120, RestoreTimeoutSeconds: 3600,
	}
}

// fakeExecutor replaces database access.
type fakeExecutor struct {
	transactions map[string]bool
	validate     error
	sql          func() (*db.SQLOutcome, error)
}

func (f *fakeExecutor) Transactions() map[string]bool { return f.transactions }
func (f *fakeExecutor) Validate(string, []db.Statement) error {
	return f.validate
}
func (f *fakeExecutor) Rows(string, db.RowsQuery) (*db.RowsResult, error) {
	return nil, errors.New("unexpected")
}
func (f *fakeExecutor) Refs(string, []db.Ref, int) ([]db.RefCount, error) {
	return nil, errors.New("unexpected")
}
func (f *fakeExecutor) SQL(string, []db.Statement, []any) (*db.SQLOutcome, error) { return f.sql() }
func (f *fakeExecutor) End(string, string) (bool, error)                          { return false, nil }

// writeConfig saves the in-memory test configuration so settings can be edited.
func writeConfig(t *testing.T, cfg *config.Config) {
	t.Helper()
	text := "[server]\nport = 8765\n\n[[connections]]\nid = \"library\"\nlabel = \"Library\"\ndriver = \"postgresql\"\n" +
		"host = \"127.0.0.1\"\nport = 5432\ndatabase = \"sample\"\nuser = \"reader\"\npassword_env = \"ATLAS_UNIT_PASSWORD\"\n"
	if err := os.WriteFile(cfg.Path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(cfg.Path), ".env"), []byte("ATLAS_UNIT_PASSWORD=unit-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg.Connections[0].PasswordEnv = "ATLAS_UNIT_PASSWORD"
	cfg.AllowedDBHosts = []string{"localhost", "127.0.0.1"}
}

type harness struct {
	t       *testing.T
	app     *App
	handler http.Handler
	token   string
}

func newHarness(t *testing.T) *harness {
	cfg := configuration(t.TempDir())
	writeConfig(t, cfg)
	app, err := New(cfg, nil, "/bin/false", fstest.MapFS{"index.html": {Data: []byte("<html></html>")}, "assets/app.js": {Data: []byte("x")}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.Close)
	h := &harness{t: t, app: app, handler: app.Handler()}
	status, body := h.do("POST", "/api/sessions", "{}", map[string]string{"X-DB-Atlas": "1"})
	if status != http.StatusCreated {
		t.Fatalf("session: %d %s", status, body)
	}
	var created map[string]any
	_ = json.Unmarshal([]byte(body), &created)
	h.token = created["id"].(string)
	return h
}

func (h *harness) do(method, path, body string, headers map[string]string) (int, string) {
	request := httptest.NewRequest(method, "http://127.0.0.1:8765"+path, strings.NewReader(body))
	for key, value := range headers {
		if key == "Host" {
			request.Host = value
		} else {
			request.Header.Set(key, value)
		}
	}
	recorder := httptest.NewRecorder()
	h.handler.ServeHTTP(recorder, request)
	data, _ := io.ReadAll(recorder.Result().Body)
	return recorder.Code, string(data)
}

func (h *harness) api(method, path, body string) (int, string) {
	return h.do(method, path, body, map[string]string{"X-DB-Atlas": "1", "X-Atlas-Session": h.token})
}

func TestPublicConfigAndNetworkBoundary(t *testing.T) {
	h := newHarness(t)
	request := httptest.NewRequest("GET", "http://127.0.0.1:8765/api/config", nil)
	recorder := httptest.NewRecorder()
	h.handler.ServeHTTP(recorder, request)
	if recorder.Code != 200 || recorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("config: %d %v", recorder.Code, recorder.Header())
	}
	if body := recorder.Body.String(); strings.Contains(body, "password") || strings.Contains(body, "unit-secret") {
		t.Fatalf("config leaks credentials: %s", body)
	}
	cases := []struct {
		method, path string
		headers      map[string]string
	}{
		{"GET", "/api/config", map[string]string{"Host": "evil.invalid"}},
		{"POST", "/api/sessions", map[string]string{}},
		{"POST", "/api/sessions", map[string]string{"X-DB-Atlas": "1", "Origin": "https://evil.invalid"}},
	}
	for _, c := range cases {
		if status, body := h.do(c.method, c.path, "{}", c.headers); status != 403 {
			t.Errorf("%s %s %v: %d %s", c.method, c.path, c.headers, status, body)
		}
	}
	if status, _ := h.do("POST", "/api/sessions", "{}", map[string]string{"X-DB-Atlas": "1", "Origin": "http://127.0.0.1:8765"}); status != 201 {
		t.Errorf("same-origin request refused: %d", status)
	}
}

func TestValidationAndReadOnlyContract(t *testing.T) {
	h := newHarness(t)
	for _, body := range []string{
		`{"engine":"library","sql":""}`,
		`{"engine":"unknown","sql":"SELECT 1"}`,
		`{"engine":"library","sql":"UPDATE sample SET value=1"}`,
		`{"engine":"library","sql":"SET default_transaction_read_only=off"}`,
		`{"engine":"library","sql":"SELECT 1","extra":true}`,
		`{"engine":"library","sql":"SELECT 1","params":[[1]]}`,
		`[]`,
	} {
		if status, response := h.api("POST", "/api/sql", body); status != 400 {
			t.Errorf("%s: %d %s", body, status, response)
		}
	}
	if status, response := h.api("POST", "/api/sql", strings.Repeat("x", 1_048_577)); status != 400 || !strings.Contains(response, "JSON") {
		t.Errorf("oversized body: %d %s", status, response)
	}
	if status, _ := h.do("POST", "/api/sql", `{"engine":"library","sql":"SELECT 1"}`, map[string]string{"X-DB-Atlas": "1"}); status != 400 {
		t.Errorf("missing session header: %d", status)
	}
}

func TestSQLResponseAndErrorRedaction(t *testing.T) {
	h := newHarness(t)
	fake := &fakeExecutor{transactions: map[string]bool{"library": false}}
	h.app.newExecutor = func(*db.Worker) executor { return fake }
	hasMore, columns := false, []string{"n"}
	rows := [][]any{{"1"}}
	fake.sql = func() (*db.SQLOutcome, error) {
		return &db.SQLOutcome{Results: []db.SQLResult{{Index: 0, Kind: "read", Columns: &columns, Rows: &rows, HasMore: &hasMore, DurationMs: 1}}}, nil
	}
	status, body := h.api("POST", "/api/sql", `{"engine":"library","sql":"SELECT 1"}`)
	var response map[string]any
	_ = json.Unmarshal([]byte(body), &response)
	if status != 200 || response["results"].([]any)[0].(map[string]any)["rows"].([]any)[0].([]any)[0] != "1" {
		t.Fatalf("sql: %d %s", status, body)
	}
	if _, present := response["error"]; present {
		t.Fatalf("null error must be omitted: %s", body)
	}
	fake.sql = func() (*db.SQLOutcome, error) { return nil, errors.New("unit-secret connection failed") }
	status, body = h.api("POST", "/api/sql", `{"engine":"library","sql":"SELECT 1"}`)
	if status != 502 || strings.Contains(body, "unit-secret") || !strings.Contains(body, `"txOpen":false`) {
		t.Fatalf("upstream failure: %d %s", status, body)
	}
}

func TestConfirmationResponse(t *testing.T) {
	h := newHarness(t)
	h.app.newExecutor = func(*db.Worker) executor {
		return &fakeExecutor{transactions: map[string]bool{"library": false}, sql: func() (*db.SQLOutcome, error) {
			t.Error("must not execute before confirmation")
			return nil, nil
		}}
	}
	status, body := h.api("POST", "/api/sql", `{"engine":"library","sql":"DELETE FROM sample"}`)
	var response map[string]any
	_ = json.Unmarshal([]byte(body), &response)
	if status != 200 || response["needsConfirmation"] != true || response["statements"] != float64(1) || response["txOpen"] != false {
		t.Fatalf("confirmation: %d %s", status, body)
	}
}

func TestSessionCloseAndExpiryAreExplicit(t *testing.T) {
	h := newHarness(t)
	if status, _ := h.api("POST", "/api/sessions/close", ""); status != 204 {
		t.Fatalf("close: %d", status)
	}
	status, body := h.api("GET", "/api/sessions/current", "")
	if status != 409 || !strings.Contains(body, `"code":"session_expired"`) {
		t.Fatalf("expired: %d %s", status, body)
	}
}

func TestDataFileAllowlist(t *testing.T) {
	h := newHarness(t)
	dir := h.app.Config.DataDir
	_ = os.WriteFile(filepath.Join(dir, "private.json"), []byte(`{"secret":true}`), 0o600)
	if status, _ := h.do("GET", "/data/private.json", "", nil); status != 404 {
		t.Fatalf("private file served: %d", status)
	}
	if status, _ := h.do("GET", "/data/../atlas.toml", "", nil); status == 200 {
		t.Fatal("path traversal served")
	}
	_ = os.WriteFile(filepath.Join(dir, "schema.json"), []byte("{}"), 0o600)
	request := httptest.NewRequest("GET", "http://127.0.0.1:8765/data/schema.json", nil)
	recorder := httptest.NewRecorder()
	h.handler.ServeHTTP(recorder, request)
	if recorder.Code != 200 || recorder.Body.String() != "{}" || recorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("schema.json: %d %q %v", recorder.Code, recorder.Body.String(), recorder.Header())
	}
}

func TestIndexAndAssets(t *testing.T) {
	h := newHarness(t)
	if status, body := h.do("GET", "/", "", nil); status != 200 || body != "<html></html>" {
		t.Fatalf("index: %d %s", status, body)
	}
	if status, body := h.do("GET", "/assets/app.js", "", nil); status != 200 || body != "x" {
		t.Fatalf("asset: %d %s", status, body)
	}
}

func TestFilterValidationAndCatalogContract(t *testing.T) {
	h := newHarness(t)
	schema := `{"sources":{"library":[{"schema":"public","name":"sample","columns":[{"name":"id","type":"integer"}],"foreignKeys":[]}]}}`
	_ = os.WriteFile(filepath.Join(h.app.Config.DataDir, "schema.json"), []byte(schema), 0o600)
	status, body := h.api("POST", "/api/data/rows", `{"engine":"library","schema":"public","table":"sample","filters":{"bad":"1"}}`)
	if status != 400 || !strings.Contains(body, "colonne inconnue") {
		t.Fatalf("unknown filter column: %d %s", status, body)
	}
	if status, body = h.api("POST", "/api/data/rows", `{"engine":"library","schema":"public","table":"missing"}`); status != 400 {
		t.Fatalf("unknown table: %d %s", status, body)
	}
	if status, _ = h.api("POST", "/api/data/rows", `{"engine":"library","schema":"public","table":"sample","pageSize":501}`); status != 400 {
		t.Fatalf("page size bound: %d", status)
	}
}

func TestStatusAndJobs(t *testing.T) {
	h := newHarness(t)
	status, body := h.api("GET", "/api/status", "")
	if status != 200 || !strings.Contains(body, `"state":"idle"`) || !strings.Contains(body, `"transactions":{"library":false}`) {
		t.Fatalf("status: %d %s", status, body)
	}
	if status, _ = h.api("POST", "/api/s3/refresh", "{}"); status != 404 {
		t.Fatalf("disabled dumps module: %d", status)
	}
	if status, _ = h.api("POST", "/api/jobs/unknown/cancel", "{}"); status != 404 {
		t.Fatalf("unknown job: %d", status)
	}
}

func TestParseFilter(t *testing.T) {
	cases := map[string]string{
		"null": "null", "!null": "notnull", ">= 5": "cmp >= 5", "<> x": "cmp != x", "=abc": "eq  abc", "12": "eq  12",
	}
	for text, want := range cases {
		filter := parseFilter(columnOf("integer"), text)
		got := filter.Mode
		if filter.Value != nil {
			got += " " + filter.Op + " " + *filter.Value
		}
		if got != want {
			t.Errorf("parseFilter(%q) = %q, want %q", text, got, want)
		}
	}
	if filter := parseFilter(columnOf("text"), " abc "); filter.Mode != "like" || *filter.Value != "abc" {
		t.Errorf("text filter = %+v", filter)
	}
	if parseFilter(columnOf("text"), "  ") != nil {
		t.Error("blank filter must be ignored")
	}
}

func columnOf(kind string) model.Column { return model.Column{Name: "c", Type: kind} }

func TestSettingsEditingAndRestart(t *testing.T) {
	h := newHarness(t)
	status, body := h.api("GET", "/api/connections", "")
	if status != 200 || !strings.Contains(body, `"passwordEnv":"ATLAS_UNIT_PASSWORD"`) || strings.Contains(body, "unit-secret") || !strings.Contains(body, `"pendingRestart":false`) {
		t.Fatalf("overview: %d %s", status, body)
	}
	add := `{"isNew":true,"connection":{"id":"store","label":"Store","driver":"mysql","host":"127.0.0.1","port":3306,"database":"sakila","user":"reader","defaultSchema":"","color":"#e5a85e","allowWrites":false,"passwordEnv":"STORE_PASSWORD","password":"store-secret"}}`
	if status, body = h.api("POST", "/api/connections/save", add); status != 200 {
		t.Fatalf("add: %d %s", status, body)
	}
	status, body = h.api("GET", "/api/connections", "")
	if !strings.Contains(body, `"id":"store"`) || !strings.Contains(body, `"pendingRestart":true`) || strings.Contains(body, "store-secret") {
		t.Fatalf("overview after add: %s", body)
	}
	if status, body = h.api("POST", "/api/connections/save", strings.Replace(add, "127.0.0.1", "evil.example", 1)); status != 400 {
		t.Fatalf("host outside allowed_db_hosts: %d %s", status, body)
	}
	source := `{"source":{"connection":"store","type":"s3-versioned","bucket":"b","key":"k.sql.gz"}}`
	if status, body = h.api("POST", "/api/dump-sources/save", source); status != 200 {
		t.Fatalf("source: %d %s", status, body)
	}
	if status, body = h.api("POST", "/api/connections/delete", `{"id":"store"}`); status != 200 {
		t.Fatalf("delete: %d %s", status, body)
	}
	if _, body = h.api("GET", "/api/connections", ""); strings.Contains(body, `"store"`) {
		t.Fatalf("deleted connection still listed: %s", body)
	}
	if status, _ = h.do("POST", "/api/connections/delete", `{"id":"library"}`, map[string]string{}); status != 403 {
		t.Fatalf("CSRF header not required: %d", status)
	}

	if status, _ = h.api("POST", "/api/restart", "{}"); status != 409 {
		t.Fatalf("restart without launcher support: %d", status)
	}
	h.app.Restart = make(chan struct{}, 1)
	h.app.Sessions.Worker(h.token).Databases["library"].SetTransaction(true)
	if status, body = h.api("POST", "/api/restart", "{}"); status != 409 || !strings.Contains(body, "restarting") {
		t.Fatalf("restart with an open transaction: %d %s", status, body)
	}
	h.app.Sessions.Worker(h.token).Databases["library"].SetTransaction(false)
	if status, _ = h.api("POST", "/api/restart", "{}"); status != 202 || len(h.app.Restart) != 1 {
		t.Fatalf("restart not requested: %d", status)
	}
}
