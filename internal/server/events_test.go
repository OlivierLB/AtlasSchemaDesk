package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEventWindowUsesOnlyCachedCatalog(t *testing.T) {
	h := newHarness(t)
	fixture := `{"generatedAt":"2026-11-23T09:00:00Z","engines":{"library":{"programs":[{"id":"library:event:tick","kind":"event","name":"tick","schema":"public","status":"ENABLED","eventSchedule":{"type":"RECURRING","timeZone":"UTC","scheduler":"ON","starts":"2026-01-01 10:00:00","intervalValue":"15","intervalField":"MINUTE"}}]},"unknown":{"programs":[{"id":"unknown:event:hidden","kind":"event","status":"ENABLED"}]}}}`
	if err := os.WriteFile(filepath.Join(h.app.Config.DataDir, "programs.json"), []byte(fixture), 0600); err != nil {
		t.Fatal(err)
	}
	status, body := h.do("POST", "/api/events/window", `{"from":"2026-11-23T10:00:00Z","to":"2026-11-23T11:00:00Z","lookbackMinutes":30}`, map[string]string{"X-DB-Atlas": "1"})
	if status != http.StatusOK || !strings.Contains(body, `"starts":4`) || strings.Contains(body, "unknown:event") {
		t.Fatalf("projection: %d %s", status, body)
	}
	contract := loadContract(t)
	checkShape(t, contract, "EventWindowReport", body)
	var report struct {
		Events  []json.RawMessage `json:"events"`
		Buckets []json.RawMessage `json:"buckets"`
	}
	if err := json.Unmarshal([]byte(body), &report); err != nil {
		t.Fatal(err)
	}
	checkShape(t, contract, "EventWindowMatch", string(report.Events[0]))
	checkShape(t, contract, "EventWindowBucket", string(report.Buckets[0]))
}
func TestEventWindowRejectsInvalidInputAndMissingCatalog(t *testing.T) {
	h := newHarness(t)
	for _, body := range []string{`{}`, `{"from":"2026-01-01","to":"2026-01-02"}`, `{"from":"2026-11-23T10:00:00Z","to":"2026-11-23T09:00:00Z"}`, `{"from":"2026-11-01T10:00:00Z","to":"2026-11-23T10:00:00Z"}`, `{"from":"2026-11-23T10:00:00Z","to":"2026-11-23T11:00:00Z","lookbackMinutes":-1}`, `{"from":"2026-11-23T10:00:00Z","to":"2026-11-23T11:00:00Z","extra":true}`} {
		status, _ := h.api("POST", "/api/events/window", body)
		if status < 400 {
			t.Fatalf("invalid window accepted: %s", body)
		}
	}
}
