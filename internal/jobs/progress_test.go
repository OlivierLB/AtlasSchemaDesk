package jobs

import (
	"strings"
	"testing"
)

func TestRestoreProgressSurvivesSplitChunksAndLogRotation(t *testing.T) {
	runner := New(testConfig(t), "restore", nil, 0)
	runner.appendBytes([]byte("[sulcata-prog"))
	runner.appendBytes([]byte("ress] downloading 512 1024\n"))
	status := runner.Status()
	if status.Context["phase"] != "downloading" || status.Context["bytes"] != "512" || status.Context["totalBytes"] != "1024" {
		t.Fatalf("progress lost: %+v", status.Context)
	}
	if strings.Contains(status.Log, "sulcata-progress") {
		t.Fatal("protocol record displayed as log")
	}
	runner.appendBytes([]byte(strings.Repeat("command output\n", 1000)))
	runner.appendBytes([]byte("[sulcata-progress] restoring 0 0\n\x1b[32mImport started\x1b[0m\nunit-secret\n"))
	status = runner.Status()
	if status.Context["phase"] != "restoring" || strings.Contains(status.Log, "\x1b") || strings.Contains(status.Log, "unit-secret") || !strings.Contains(status.Log, "[redacted]") {
		t.Fatalf("unexpected status: %+v", status)
	}
	status.Context["phase"] = "changed"
	if runner.Status().Context["phase"] != "restoring" {
		t.Fatal("status aliases runner context")
	}
	runner.appendBytes([]byte("[sulcata-progress] downloading 2048 1024\n"))
	if runner.Status().Context["phase"] != "restoring" {
		t.Fatal("invalid byte counts accepted")
	}
}
