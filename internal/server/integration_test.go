package server

// HTTP ownership tests against the disposable sample databases (tests/run.sh).

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/OlivierLB/Sulcata/internal/catalog"
	"github.com/OlivierLB/Sulcata/internal/config"
	"github.com/OlivierLB/Sulcata/internal/db"
	"github.com/OlivierLB/Sulcata/internal/eventplan"
	"github.com/OlivierLB/Sulcata/internal/model"
	"github.com/OlivierLB/Sulcata/internal/sessions"
)

func TestIntegrationEventScheduleMetadata(t *testing.T) {
	h, _ := integration(t)
	status, body := h.do("GET", "/data/programs.json", "", map[string]string{"Host": fmt.Sprintf("127.0.0.1:%d", h.app.Config.Port)})
	if status != http.StatusOK {
		t.Fatalf("program catalog: %d", status)
	}
	var data struct {
		Engines map[string]*model.Engine `json:"engines"`
	}
	if err := json.Unmarshal([]byte(body), &data); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range data.Engines["store"].Programs {
		if p.Kind == "event" && p.Name == "sulcata_probe_tick" {
			s := p.EventSchedule
			if s == nil || s.TimeZone == "" || s.SystemTimeZone == "" || s.Scheduler == "" || s.IntervalValue == nil || s.Starts == nil || s.Created == nil {
				t.Fatalf("incomplete event metadata: %+v", s)
			}
			found = true
		}
	}
	if !found {
		t.Fatal("disposable test event was not exported")
	}
	for _, test := range []struct{ name, from, to string }{{"sulcata_probe_week", "2026-01-12T00:00:00Z", "2026-01-13T00:00:00Z"}, {"sulcata_probe_quarter", "2026-04-01T00:00:00Z", "2026-04-02T00:00:00Z"}} {
		found = false
		for _, p := range data.Engines["store"].Programs {
			if p.Name != test.name {
				continue
			}
			found = true
			p.Status = "ENABLED"
			p.EventSchedule.Scheduler = "ON"
			from, _ := time.Parse(time.RFC3339, test.from)
			to, _ := time.Parse(time.RFC3339, test.to)
			result := eventplan.Build(map[string]*model.Engine{"store": {Programs: []*model.Program{p}}}, "", from, to, 0)
			if len(result.Events) != 1 || result.Events[0].Starts != 1 {
				t.Fatalf("MySQL interval normalization mismatch for %s: value=%v field=%v result=%+v", test.name, p.EventSchedule.IntervalValue, p.EventSchedule.IntervalField, result)
			}
		}
		if !found {
			t.Fatalf("missing disposable schedule: %s", test.name)
		}
	}
}

type clock struct {
	mu  sync.Mutex
	now float64
}

func (c *clock) get() float64  { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *clock) set(v float64) { c.mu.Lock(); c.now = v; c.mu.Unlock() }

func integration(t *testing.T) (*harness, *clock) {
	path := os.Getenv("SULCATA_TEST_CONFIG")
	if path == "" {
		t.Skip("requires disposable sample databases (tests/run.sh)")
	}
	cfg, err := config.Load(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	cfg.DataDir = t.TempDir()
	if err := catalog.Export(cfg); err != nil {
		t.Fatal(err)
	}
	c := &clock{}
	app, err := New(cfg, sessions.NewManager(cfg, c.get), "/bin/false", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.Close)
	return &harness{t: t, app: app, handler: app.Handler()}, c
}

func (h *harness) session() map[string]string {
	h.t.Helper()
	request := map[string]string{"X-Sulcata": "1", "Host": fmt.Sprintf("127.0.0.1:%d", h.app.Config.Port)}
	status, body := h.do("POST", "/api/sessions", "{}", request)
	if status != http.StatusCreated {
		h.t.Fatalf("session: %d %s", status, body)
	}
	var created map[string]any
	_ = json.Unmarshal([]byte(body), &created)
	request["X-Sulcata-Session"] = created["id"].(string)
	return request
}

func (h *harness) sqlAs(headers map[string]string, engine, sql string) map[string]any {
	h.t.Helper()
	body, _ := json.Marshal(map[string]string{"engine": engine, "sql": sql})
	status, response := h.do("POST", "/api/sql", string(body), headers)
	if status != http.StatusOK {
		h.t.Fatalf("%s: %d %s", sql, status, response)
	}
	var decoded map[string]any
	_ = json.Unmarshal([]byte(response), &decoded)
	return decoded
}

func firstCell(response map[string]any) any {
	return response["results"].([]any)[0].(map[string]any)["rows"].([]any)[0].([]any)[0]
}

func TestIntegrationIndependentSessionsGridErrorsAndRollback(t *testing.T) {
	h, _ := integration(t)
	for _, engine := range []string{"library-write", "store-write", "older-write"} {
		a, b := h.session(), h.session()
		if h.sqlAs(a, engine, "UPDATE sulcata_probe SET value='http-pending' WHERE id=1")["txOpen"] != true {
			t.Fatalf("%s: transaction not open", engine)
		}
		if got := firstCell(h.sqlAs(a, engine, "SELECT value FROM sulcata_probe WHERE id=1")); got != "http-pending" {
			t.Fatalf("%s: own session sees %v", engine, got)
		}
		if got := firstCell(h.sqlAs(b, engine, "SELECT value FROM sulcata_probe WHERE id=1")); got != "original" {
			t.Fatalf("%s: other session sees %v", engine, got)
		}
		if status, body := h.do("POST", "/api/sql/commit", `{"engine":"`+engine+`"}`, b); status != 200 || !strings.Contains(body, `"done":false`) {
			t.Fatalf("%s: commit of another session: %d %s", engine, status, body)
		}
		if h.sqlAs(a, engine, "SELECT * FROM sulcata_missing_table")["error"] == nil {
			t.Fatalf("%s: missing table without error", engine)
		}
		options, _ := h.app.Config.Connection(engine)
		status, body := h.do("POST", "/api/data/rows", fmt.Sprintf(`{"engine":%q,"schema":%q,"table":"sulcata_probe"}`, engine, options.DefaultSchema), a)
		if status != 200 || !strings.Contains(body, `"value":"http-pending"`) {
			t.Fatalf("%s: grid %d %s", engine, status, body)
		}
		h.do("POST", "/api/sql/rollback", `{"engine":"`+engine+`"}`, a)
		if got := firstCell(h.sqlAs(b, engine, "SELECT value FROM sulcata_probe WHERE id=1")); got != "original" {
			t.Fatalf("%s: after rollback %v", engine, got)
		}
	}
}

func TestIntegrationExpiryClosesRealUncommittedTransaction(t *testing.T) {
	h, c := integration(t)
	headers := h.session()
	h.sqlAs(headers, "library-write", "UPDATE sulcata_probe SET value='must-expire' WHERE id=1")
	c.set(float64(h.app.Config.SessionIdleSeconds))
	if status, _ := h.do("GET", "/api/sessions/current", "", headers); status != http.StatusConflict {
		t.Fatalf("expired session answered %d", status)
	}
	options, _ := h.app.Config.Connection("library")
	observer := db.NewDatabase(h.app.Config, options)
	defer observer.Close()
	rows, err := observer.Query("SELECT value FROM sulcata_probe WHERE id=1")
	if err != nil || rows[0]["value"] != "original" {
		t.Fatalf("uncommitted change survived expiry: %v %v", rows, err)
	}
}

func TestIntegrationRefsThroughCatalog(t *testing.T) {
	h, _ := integration(t)
	headers := h.session()
	status, body := h.do("POST", "/api/data/refs", `{"engine":"store","schema":"sakila","table":"actor","row":{"actor_id":1}}`, headers)
	if status != 200 || !strings.Contains(body, `"table":"film_actor"`) || !strings.Contains(body, `"count":`) {
		t.Fatalf("refs: %d %s", status, body)
	}
}
