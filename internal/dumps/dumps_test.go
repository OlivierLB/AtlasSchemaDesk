package dumps

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/OlivierLB/Sulcata/internal/config"
)

func setup(t *testing.T, restore string) (*config.Config, string) {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "backups"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "backups/a.sql"), []byte("public sample dump"), 0o644); err != nil {
		t.Fatal(err)
	}
	text := `[[connections]]
id="library"
driver="postgresql"
host="localhost"
database="library"
user="reader"
password_env="DB_PASSWORD"
[[dump_sources]]
connection="library"
type="directory"
path="backups"
pattern="*.sql"
local_path="current.sql"
restore_command=` + "'" + restore + "'\n"
	path := filepath.Join(root, "sulcata.toml")
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path, map[string]string{"DB_PASSWORD": "unit-secret"})
	if err != nil {
		t.Fatal(err)
	}
	return cfg, filepath.Dir(cfg.Path)
}

func noExport(t *testing.T) *int {
	calls := 0
	previous := Export
	Export = func(*config.Config) error { calls++; return nil }
	t.Cleanup(func() { Export = previous })
	return &calls
}

func TestListingDoesNotDiscloseFilesystemPaths(t *testing.T) {
	cfg, root := setup(t, "exit 1")
	if err := Listing(cfg); err != nil {
		t.Fatal(err)
	}
	payload, _ := os.ReadFile(filepath.Join(cfg.DataDir, "s3-dumps.json"))
	if strings.Contains(string(payload), root) {
		t.Fatalf("listing discloses paths: %s", payload)
	}
	var data struct{ Targets []struct{ Versions []any } }
	if err := json.Unmarshal(payload, &data); err != nil || len(data.Targets[0].Versions) != 1 {
		t.Fatalf("unexpected listing %s", payload)
	}
}

func TestFailedRestorePreservesPreviousDump(t *testing.T) {
	cfg, root := setup(t, "exit 1")
	calls := noExport(t)
	target := filepath.Join(root, "current.sql")
	if err := os.WriteFile(target, []byte("previous dump"), 0o644); err != nil {
		t.Fatal(err)
	}
	versions, _ := Versions(cfg.DumpSources[0])
	err := Apply(cfg, "library", versions[0].VersionID)
	if err == nil || !strings.Contains(err.Error(), "exit 1") {
		t.Fatalf("expected restore failure, got %v", err)
	}
	if data, _ := os.ReadFile(target); string(data) != "previous dump" {
		t.Fatalf("previous dump replaced: %q", data)
	}
	if leftovers, _ := filepath.Glob(filepath.Join(root, ".download-*")); len(leftovers) != 0 || *calls != 0 {
		t.Fatalf("leftovers %v exports %d", leftovers, *calls)
	}
}

func TestSuccessPromotesDumpAfterCommandAndExports(t *testing.T) {
	cfg, root := setup(t, `test -s "$SULCATA_DUMP_PATH" && test "$SULCATA_CONNECTION" = library`)
	calls := noExport(t)
	versions, _ := Versions(cfg.DumpSources[0])
	if err := Apply(cfg, "library", versions[0].VersionID); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(filepath.Join(root, "current.sql")); string(data) != "public sample dump" || *calls != 1 {
		t.Fatalf("dump %q exports %d", data, *calls)
	}
	if data, err := os.ReadFile(filepath.Join(cfg.DataDir, "applied-dumps.json")); err != nil || !strings.Contains(string(data), versions[0].VersionID) {
		t.Fatalf("applied record missing: %s %v", data, err)
	}
	if err := Apply(cfg, "library", "unknown"); err == nil {
		t.Fatal("unknown version must fail")
	}
}

func TestS3FiltersExactKeyAndDeleteMarkers(t *testing.T) {
	previous := AWS
	AWS = func(args ...string) ([]byte, error) {
		return []byte(`{"Versions":[{"Key":"library.sql","VersionId":"id+/=","LastModified":"2026-01-01","Size":10,"IsLatest":true},` +
			`{"Key":"library.sql.other","VersionId":"other","LastModified":"2026-01-01","Size":1}],"DeleteMarkers":[{"Key":"library.sql"}]}`), nil
	}
	t.Cleanup(func() { AWS = previous })
	versions, err := Versions(config.DumpSource{Type: "s3-versioned", Bucket: "public-example", Key: "library.sql"})
	if err != nil || len(versions) != 1 || versions[0].VersionID != "id+/=" || !*versions[0].IsLatest {
		t.Fatalf("versions = %+v, %v", versions, err)
	}
}
