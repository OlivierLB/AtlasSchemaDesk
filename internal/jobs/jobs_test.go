package jobs

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/OlivierLB/AtlasSchemaDesk/internal/config"
)

func testConfig(t *testing.T) *config.Config {
	root := t.TempDir()
	return &config.Config{
		Path:        filepath.Join(root, "atlas.toml"),
		Connections: []config.Connection{{ID: "library", Driver: "postgresql", Password: "unit-secret"}},
		DataDir:     filepath.Join(root, "data"),
	}
}

func job(t *testing.T, cfg *config.Config, script string, timeout time.Duration) *Runner {
	runner := New(cfg, "test", []string{"/bin/sh", "-c", script}, timeout)
	t.Cleanup(runner.Close)
	return runner
}

func finished(t *testing.T, runner *Runner) Status {
	t.Helper()
	if !runner.Wait(12 * time.Second) {
		t.Fatalf("job still running: %+v", runner.Status())
	}
	return runner.Status()
}

func waitForLog(runner *Runner, text string) {
	deadline := time.Now().Add(3 * time.Second)
	for !strings.Contains(runner.Status().Log, text) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
}

func TestStreamingIsBoundedAndSecretsAreRedacted(t *testing.T) {
	runner := job(t, testConfig(t), `head -c 100000 /dev/zero | tr '\0' x; echo; echo unit-secret; printf 'caf\303\251'`, 5*time.Second)
	if !runner.Start(nil, nil, nil) {
		t.Fatal("start refused")
	}
	state := finished(t, runner)
	if state.State != "success" || *state.ExitCode != 0 {
		t.Fatalf("state = %+v", state)
	}
	if n := len([]rune(state.Log)); n > 4000 {
		t.Fatalf("log too long: %d", n)
	}
	if strings.Contains(state.Log, "unit-secret") || !strings.Contains(state.Log, "[redacted]") || !strings.HasSuffix(state.Log, "café") {
		t.Fatalf("log not redacted or decoded: %q", state.Log[len(state.Log)-40:])
	}
}

func TestTimeoutAndExclusionRelease(t *testing.T) {
	completed := 0
	runner := job(t, testConfig(t), "sleep 60", 100*time.Millisecond)
	if !runner.Start(nil, nil, func() { completed++ }) {
		t.Fatal("start refused")
	}
	if runner.Start(nil, nil, nil) {
		t.Fatal("a second job must be refused while one runs")
	}
	if state := finished(t, runner); state.State != "timed_out" {
		t.Fatalf("state = %s", state.State)
	}
	if completed != 1 {
		t.Fatalf("onDone called %d times", completed)
	}
}

func TestCancelStopsDescendantsNotJustParent(t *testing.T) {
	cfg := testConfig(t)
	marker := filepath.Join(filepath.Dir(cfg.Path), "orphan-marker")
	runner := job(t, cfg, `(sleep 2; touch '`+marker+`'; sleep 60) & echo started........................; sleep 60`, 5*time.Second)
	runner.Start(nil, nil, nil)
	waitForLog(runner, "started")
	if !runner.Cancel() {
		t.Fatal("cancel refused")
	}
	if state := finished(t, runner); state.State != "cancelled" || *state.ExitCode >= 0 {
		t.Fatalf("state = %+v", state)
	}
	time.Sleep(2500 * time.Millisecond)
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("descendant survived cancellation")
	}
}

func TestShutdownJoinsJob(t *testing.T) {
	runner := job(t, testConfig(t), "sleep 60", 5*time.Second)
	runner.Start(nil, nil, nil)
	runner.Close()
	if runner.IsRunning() {
		t.Fatal("job still running after Close")
	}
}

func TestCleanupFailureStillReleasesReservation(t *testing.T) {
	completed := 0
	runner := job(t, testConfig(t), "echo done", 5*time.Second)
	runner.cleanupHook = func() error { return errors.New("fixture cleanup error") }
	runner.Start(nil, nil, func() { completed++ })
	if state := finished(t, runner); state.State != "error" || !strings.Contains(state.Log, "fixture cleanup error") {
		t.Fatalf("state = %+v", state)
	}
	if completed != 1 {
		t.Fatalf("onDone called %d times", completed)
	}
}

func TestRestoreCancellationCleansOnlyItsDownload(t *testing.T) {
	cfg := testConfig(t)
	root := filepath.Dir(cfg.Path)
	other := filepath.Join(root, ".download-unrelated")
	if err := os.WriteFile(other, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg.DumpSources = []config.DumpSource{{Connection: "library", Type: "directory", LocalPath: filepath.Join(root, "current.sql")}}
	runner := New(cfg, "restore", []string{"/bin/sh", "-c", `echo partial > ".download-$$-owned"; echo started........................; exec sleep 60`}, 5*time.Second)
	t.Cleanup(runner.Close)
	runner.Start(nil, map[string]string{"engine": "library"}, nil)
	waitForLog(runner, "started")
	runner.Cancel()
	state := finished(t, runner)
	if state.State != "cancelled" || !strings.Contains(state.Log, "partially restored") {
		t.Fatalf("state = %+v", state)
	}
	if matches, _ := filepath.Glob(filepath.Join(root, ".download-*")); len(matches) != 1 || matches[0] != other {
		t.Fatalf("downloads left: %v", matches)
	}
}
