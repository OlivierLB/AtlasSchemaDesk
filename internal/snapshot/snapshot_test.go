package snapshot

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/OlivierLB/AtlasSchemaDesk/internal/model"
)

func TestSourceHashMatchesPythonFingerprint(t *testing.T) {
	// Reference computed with the original Python source_hash on the same tables.
	tables := []model.Table{{
		Schema: "public", Name: "été",
		Columns:           []model.Column{{Name: "a\"b\\c\n\t\x01 <&>", Type: "int", Nullable: true}},
		ForeignKeys:       []model.ForeignKey{},
		UniqueConstraints: [][]string{{"a"}},
	}}
	got, err := SourceHash(tables)
	if err != nil {
		t.Fatal(err)
	}
	if got != "55979740f5bea177" {
		t.Fatalf("hash = %s, want 55979740f5bea177", got)
	}
}

func TestWriteAtomicReplacesWithPrivateFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "schema.json")
	if err := WriteAtomic(path, []byte("one")); err != nil {
		t.Fatal(err)
	}
	if err := WriteAtomic(path, []byte("two")); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	info, _ := os.Stat(path)
	if string(data) != "two" || info.Mode().Perm() != 0o600 {
		t.Fatalf("content %q mode %v", data, info.Mode().Perm())
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Fatalf("temporary files left: %v", entries)
	}
}
