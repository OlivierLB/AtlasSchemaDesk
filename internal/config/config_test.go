package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const base = `[[connections]]
id="library"
driver="postgresql"
host="127.0.0.1"
database="library"
user="reader"
password_env="DB_PASSWORD"
`

func load(t *testing.T, dir, text string, env map[string]string) (*Config, error) {
	t.Helper()
	path := filepath.Join(dir, "atlas.toml")
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return Load(path, env)
}

func secret() map[string]string { return map[string]string{"DB_PASSWORD": "unit-secret"} }

func TestDefaultsAndPublicRedaction(t *testing.T) {
	cfg, err := load(t, t.TempDir(), base, secret())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Connections[0].AllowWrites {
		t.Fatal("writes must be disabled by default")
	}
	for _, rendered := range []string{fmt.Sprintf("%v", cfg.Connections), fmt.Sprintf("%#v", cfg.Connections[0])} {
		if strings.Contains(rendered, "unit-secret") {
			t.Fatalf("password leaked in formatting: %s", rendered)
		}
	}
	public, _ := json.Marshal(cfg.Public())
	if strings.Contains(string(public), "unit-secret") || strings.Contains(string(public), "password") {
		t.Fatalf("public config leaks credentials: %s", public)
	}
	if got := cfg.Redact("bad unit-secret"); got != "bad [redacted]" {
		t.Fatalf("redact = %q", got)
	}
	if got := cfg.Redact("postgresql://user:pw@host/db"); got != "postgresql://[redacted]@host/db" {
		t.Fatalf("redact URL = %q", got)
	}
	if cfg.SessionIdleSeconds != 1800 || !cfg.RefreshDumpsOnStart || cfg.Connections[0].Port != 5432 {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
}

func TestLocalDotenvAndEnvironmentPrecedence(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("DB_PASSWORD=\"from-file\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := load(t, dir, base, map[string]string{})
	if err != nil || cfg.Connections[0].Password != "from-file" {
		t.Fatalf("dotenv not applied: %v", err)
	}
	cfg, err = load(t, dir, base, secret())
	if err != nil || cfg.Connections[0].Password != "unit-secret" {
		t.Fatalf("environment must win over .env: %v", err)
	}
}

func TestMissingPasswordHostAndDuplicate(t *testing.T) {
	cases := []struct {
		text string
		env  map[string]string
	}{
		{base, map[string]string{}},
		{strings.Replace(base, "127.0.0.1", "remote.invalid", 1), map[string]string{"DB_PASSWORD": "x"}},
		{base + base, map[string]string{"DB_PASSWORD": "x"}},
	}
	for _, c := range cases {
		if _, err := load(t, t.TempDir(), c.text, c.env); err == nil {
			t.Errorf("expected error for %q", c.text)
		}
	}
}

func TestTypesColorAndInlinePassword(t *testing.T) {
	for _, extra := range []string{`allow_writes="false"`, `color="red; display:none"`, `password="inline"`, `port=0`} {
		if _, err := load(t, t.TempDir(), base+"\n"+extra, secret()); err == nil {
			t.Errorf("expected error for %s", extra)
		}
	}
}

func TestPathsResolveRelativeToConfiguration(t *testing.T) {
	dir := t.TempDir()
	text := base + `
[[dump_sources]]
connection="library"
type="directory"
path="backups"
local_path="current.sql"
[routines_repository]
path="routines"
`
	cfg, err := load(t, dir, text, secret())
	if err != nil {
		t.Fatal(err)
	}
	real, _ := filepath.EvalSymlinks(dir)
	if cfg.DumpSources[0].Path != filepath.Join(real, "backups") || cfg.DumpSources[0].LocalPath != filepath.Join(real, "current.sql") {
		t.Fatalf("dump paths not resolved: %+v", cfg.DumpSources[0])
	}
	if cfg.DataDir != filepath.Join(real, ".local/data") || cfg.Routines.Path != filepath.Join(real, "routines") {
		t.Fatalf("paths not resolved: %s %s", cfg.DataDir, cfg.Routines.Path)
	}
	if cfg.DumpSources[0].Pattern != "*.sql*" || !cfg.RoutinesFor("library") {
		t.Fatalf("defaults not applied: %+v", cfg)
	}
}

func TestDetailsNeverExposePasswords(t *testing.T) {
	dir := t.TempDir()
	text := base + `
[[dump_sources]]
connection="library"
type="s3-versioned"
bucket="example-bucket"
key="library.sql.gz"
restore_command='restore.py library'
`
	cfg, err := load(t, dir, text, secret())
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(cfg.Details())
	if strings.Contains(string(encoded), "unit-secret") {
		t.Fatalf("details expose the password: %s", encoded)
	}
	details := cfg.Details()
	if details.Connections[0].Backup == nil || details.Connections[0].Backup.Connection != "library" || details.Connections[0].Backup.Bucket != "example-bucket" {
		t.Fatalf("backup must belong to its connection: %+v", details.Connections[0])
	}
	if details.Connections[0].PasswordEnv != "DB_PASSWORD" || details.Connections[0].Host != "127.0.0.1" ||
		details.DumpSources[0].Bucket != "example-bucket" || details.DumpSources[0].RestoreCommand != "restore.py library" {
		t.Fatalf("details = %+v", details)
	}
}

func TestBackupOwnership(t *testing.T) {
	for _, source := range []string{
		"[[dump_sources]]\nconnection=\"unknown\"\ntype=\"s3-versioned\"\nbucket=\"b\"\nkey=\"k\"\n",
		strings.Repeat("[[dump_sources]]\nconnection=\"library\"\ntype=\"s3-versioned\"\nbucket=\"b\"\nkey=\"k\"\n", 2),
	} {
		if _, err := load(t, t.TempDir(), base+source, secret()); err == nil {
			t.Fatal("orphan or duplicate backup accepted")
		}
	}
	cfg, err := load(t, t.TempDir(), base, secret())
	if err != nil || cfg.Details().Connections[0].Backup != nil {
		t.Fatalf("connection without backup: %v", err)
	}
}

func TestBackupAttachedOnlyToItsOwner(t *testing.T) {
	text := base + strings.ReplaceAll(base, "library", "store") + `
[[dump_sources]]
connection="store"
type="s3-versioned"
bucket="store-backups"
key="store.sql.gz"
`
	cfg, err := load(t, t.TempDir(), text, secret())
	if err != nil {
		t.Fatal(err)
	}
	details := cfg.Details()
	if details.Connections[0].Backup != nil || details.Connections[1].Backup == nil || details.Connections[1].Backup.Connection != "store" {
		t.Fatalf("backup attached to the wrong database: %+v", details.Connections)
	}
}
