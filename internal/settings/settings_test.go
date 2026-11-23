package settings

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/OlivierLB/AtlasSchemaDesk/internal/config"
)

const initial = `[server]
allowed_db_hosts = ["127.0.0.1", "db.local"]

[[connections]]
id = "library"
driver = "postgresql"
host = "127.0.0.1"
database = "library"
user = "reader"
password_env = "LIBRARY_PASSWORD"

[[dump_sources]]
connection = "library"
type = "s3-versioned"
bucket = "backups"
key = "library.sql.gz"
local_path = "dumps/library.sql.gz"
restore_command = "restore-library"

[routines_repository]
path = "routines"
connections = ["library"]
`

func store(t *testing.T) (*Store, string) {
	dir := t.TempDir()
	path := filepath.Join(dir, "atlas.toml")
	if err := os.WriteFile(path, []byte(initial), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("# local secrets\nLIBRARY_PASSWORD=old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := NewStore(path)
	s.environ = func() map[string]string { return map[string]string{} }
	return s, dir
}

func loaded(t *testing.T, s *Store) *config.Config {
	t.Helper()
	cfg, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestAddEditAndDeleteConnection(t *testing.T) {
	s, dir := store(t)
	added := Connection{ID: "store", Label: "Store", Driver: "mysql", Host: "db.local", Port: 3306, Database: "sakila",
		User: "reader", PasswordEnv: "STORE_PASSWORD", Password: "it's #secret ", Color: "#e5a85e"}
	if _, err := s.SaveConnection(added, true); err != nil {
		t.Fatal(err)
	}
	cfg := loaded(t, s)
	store, _ := cfg.Connection("store")
	if store.Password != "it's #secret " || store.DefaultSchema != "sakila" || store.Port != 3306 {
		t.Fatalf("added connection = %#v / %q", store, store.Password)
	}
	env, _ := os.ReadFile(filepath.Join(dir, ".env"))
	if !strings.Contains(string(env), "# local secrets") || !strings.Contains(string(env), "LIBRARY_PASSWORD=old") {
		t.Fatalf(".env lines lost: %s", env)
	}
	if info, _ := os.Stat(filepath.Join(dir, ".env")); info.Mode().Perm() != 0o600 {
		t.Fatalf(".env mode %v", info.Mode().Perm())
	}
	if _, err := s.SaveConnection(added, true); !errors.Is(err, ErrInvalid) {
		t.Fatalf("duplicate id accepted: %v", err)
	}

	edited := added
	edited.Label, edited.AllowWrites, edited.Password = "Store RW", true, ""
	if _, err := s.SaveConnection(edited, false); err != nil {
		t.Fatal(err)
	}
	store, _ = loaded(t, s).Connection("store")
	if store.Label != "Store RW" || !store.AllowWrites || store.Password != "it's #secret " {
		t.Fatalf("edit lost data or password: %#v", store)
	}
	if _, err := os.Stat(filepath.Join(dir, "atlas.toml.previous")); err != nil {
		t.Fatal("previous configuration not kept")
	}

	if err := s.DeleteConnection("library"); err != nil {
		t.Fatal(err)
	}
	cfg = loaded(t, s)
	if len(cfg.Connections) != 1 || len(cfg.DumpSources) != 0 || cfg.RoutinesFor("library") {
		t.Fatalf("cascade delete incomplete: %+v", cfg)
	}
	// The last connection can go too: the application starts empty.
	if err := s.DeleteConnection("store"); err != nil {
		t.Fatal(err)
	}
	if cfg = loaded(t, s); len(cfg.Connections) != 0 {
		t.Fatalf("last connection kept: %+v", cfg.Connections)
	}
}

func TestInvalidChangesLeaveFilesUntouched(t *testing.T) {
	s, dir := store(t)
	before, _ := os.ReadFile(filepath.Join(dir, "atlas.toml"))
	cases := []Connection{
		{ID: "remote", Driver: "postgresql", Host: "evil.example", Database: "x", User: "u", PasswordEnv: "REMOTE_PASSWORD", Password: "p"},
		{ID: "Bad Id", Driver: "postgresql", Host: "127.0.0.1", Database: "x", User: "u", PasswordEnv: "X_PASSWORD", Password: "p"},
		{ID: "nopass", Driver: "postgresql", Host: "127.0.0.1", Database: "x", User: "u", PasswordEnv: "UNSET_PASSWORD"},
		{ID: "badenv", Driver: "postgresql", Host: "127.0.0.1", Database: "x", User: "u", PasswordEnv: "BAD-NAME", Password: "p"},
		{ID: "newline", Driver: "postgresql", Host: "127.0.0.1", Database: "x", User: "u", PasswordEnv: "NL_PASSWORD", Password: "a\nb"},
	}
	for _, c := range cases {
		if _, err := s.SaveConnection(c, true); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s accepted: %v", c.ID, err)
		}
	}
	after, _ := os.ReadFile(filepath.Join(dir, "atlas.toml"))
	env, _ := os.ReadFile(filepath.Join(dir, ".env"))
	if string(before) != string(after) || strings.Contains(string(env), "REMOTE_PASSWORD") {
		t.Fatal("a rejected change modified the files")
	}
	if leftovers, _ := filepath.Glob(filepath.Join(dir, ".atlas.toml.candidate-*")); len(leftovers) != 0 {
		t.Fatalf("candidate files left: %v", leftovers)
	}
}

func TestDumpSourceEditingKeepsFileOnlySettings(t *testing.T) {
	s, _ := store(t)
	if err := s.SaveDumpSource(DumpSource{Connection: "library", Type: "s3-versioned", Bucket: "archive", Key: "new.sql.gz"}); err != nil {
		t.Fatal(err)
	}
	source := loaded(t, s).DumpSources[0]
	if source.Bucket != "archive" || source.Key != "new.sql.gz" || source.RestoreCommand != "restore-library" || !strings.HasSuffix(source.LocalPath, "dumps/library.sql.gz") {
		t.Fatalf("source = %+v", source)
	}
	if err := s.SaveDumpSource(DumpSource{Connection: "library", Type: "directory", Path: "backups", Pattern: "*.gz"}); err != nil {
		t.Fatal(err)
	}
	source = loaded(t, s).DumpSources[0]
	if source.Type != "directory" || source.Bucket != "" || source.Pattern != "*.gz" || source.RestoreCommand != "restore-library" {
		t.Fatalf("source = %+v", source)
	}
	if err := s.SaveDumpSource(DumpSource{Connection: "unknown", Type: "s3-versioned", Bucket: "b", Key: "k"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("source for an unknown connection accepted: %v", err)
	}
	if err := s.SaveDumpSource(DumpSource{Connection: "library", Type: "s3-versioned"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("S3 source without bucket accepted: %v", err)
	}
	if err := s.DeleteDumpSource("library"); err != nil || len(loaded(t, s).DumpSources) != 0 {
		t.Fatalf("delete: %v", err)
	}
}
