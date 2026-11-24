package catalog

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/OlivierLB/Sulcata/internal/config"
	"github.com/OlivierLB/Sulcata/internal/model"
)

func latin1(text string) []byte {
	var out []byte
	for _, r := range text {
		out = append(out, byte(r))
	}
	return out
}

func writeFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLatin1RepositoryFileMatches(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "public/functions/greet.sql"), latin1("CREATE FUNCTION greet() RETURNS text AS 'SELECT café';"))
	program := &model.Program{Schema: "public", Name: "greet", Kind: "function", Body: "SELECT café"}
	LinkSources([]*model.Program{program}, config.RoutinesRepository{Path: root})
	if program.Source.Status != "same" {
		t.Fatalf("status = %s", program.Source.Status)
	}
}

func TestMultipleDefinitionsAndNonMatchingFilenames(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "public/functions/shared.sql")
	writeFile(t, file, latin1("-- shared source\nCREATE FUNCTION greet() RETURNS text RETURN 'café';\nCREATE PROCEDURE other() SELECT 1;\nCREATE VIEW greeting AS SELECT 1;"))
	programs := []*model.Program{
		{Schema: "public", Name: "greet", Kind: "function", Body: "RETURN 'café'"},
		{Schema: "public", Name: "other", Kind: "procedure", Body: "SELECT 1"},
		{Schema: "public", Name: "greeting", Kind: "view", Body: "SELECT 1"},
		{Schema: "public", Name: "missing", Kind: "function", Body: "SELECT 2"},
	}
	result := LinkSources(programs, config.RoutinesRepository{Path: root, ScanDefinitions: true})
	if want := map[string]int{"same": 2, "not_comparable": 1, "absent": 1}; !reflect.DeepEqual(result.Counts, want) {
		t.Fatalf("counts = %v, want %v", result.Counts, want)
	}
	if programs[1].Source.Line != 3 {
		t.Fatalf("line = %d", programs[1].Source.Line)
	}
	real, _ := filepath.EvalSymlinks(file)
	if programs[0].Source.AbsolutePath != real || programs[0].Source.Path != "public/functions/shared.sql" {
		t.Fatalf("source = %+v", programs[0].Source)
	}
	if programs[3].Source.Path != "" || programs[3].Source.Status != "absent" {
		t.Fatalf("absent source must carry only its status: %+v", programs[3].Source)
	}
	if !result.Available {
		t.Fatal("repository must be available")
	}
}

func TestEscapingPatternIsIgnored(t *testing.T) {
	root := t.TempDir()
	program := &model.Program{Schema: "..", Name: "x", Kind: "function", Body: "1"}
	result := LinkSources([]*model.Program{program}, config.RoutinesRepository{Path: root, Pattern: "{schema}/../../{name}.sql"})
	if program.Source != nil || len(result.Counts) != 0 {
		t.Fatalf("a path outside the repository must be skipped: %+v %v", program.Source, result.Counts)
	}
}
