package analysis

import (
	"encoding/json"
	"reflect"
	"slices"
	"testing"

	"github.com/OlivierLB/AtlasSchemaDesk/internal/model"
)

func program(kind, name, body string) *model.Program {
	return &model.Program{Kind: kind, Schema: "public", Name: name, Body: body}
}

func TestDirectIndirectAndDynamicWrites(t *testing.T) {
	tables := []model.Table{{Schema: "public", Name: "actor"}, {Schema: "public", Name: "audit"}}
	programs := []*model.Program{
		program("procedure", "log", "INSERT INTO public.audit VALUES(1)"),
		program("procedure", "change_actor", "UPDATE public.actor SET id=1; CALL public.log()"),
		program("function", "dynamic_actor", "EXECUTE 'DELETE FROM public.actor'"),
	}
	index := Analyse("postgresql", programs, tables)
	if !slices.Contains(index["public.audit"].IndirectWrites, "postgresql:procedure:public.change_actor") {
		t.Fatalf("indirect write missing: %+v", index["public.audit"])
	}
	if got := programs[2].Impact.ProbableWrites; !reflect.DeepEqual(got, []string{"public.actor"}) {
		t.Fatalf("probable writes = %v", got)
	}
	if !programs[2].Impact.DynamicSQL || programs[1].Impact.DynamicSQL {
		t.Fatal("dynamic SQL detection is wrong")
	}
	if got := programs[0].Impact.CalledBy; !reflect.DeepEqual(got, []string{"postgresql:procedure:public.change_actor"}) {
		t.Fatalf("calledBy = %v", got)
	}
}

func TestUpdateAfterKeyOrDoIsNotAWrite(t *testing.T) {
	tables := []model.Table{{Schema: "app", Name: "t"}, {Schema: "app", Name: "counter"}}
	programs := []*model.Program{
		{Kind: "procedure", Schema: "app", Name: "p", Body: "INSERT INTO t VALUES(1) ON DUPLICATE KEY UPDATE counter = 1"},
		{Kind: "procedure", Schema: "app", Name: "q", Body: "INSERT INTO t VALUES(1) ON CONFLICT DO UPDATE counter SET x=1"},
		{Kind: "procedure", Schema: "app", Name: "r", Body: "UPDATE counter SET x=1"},
	}
	Analyse("mysql", programs, tables)
	if got := programs[0].Impact.Writes; !reflect.DeepEqual(got, []string{"app.t"}) {
		t.Errorf("ON DUPLICATE KEY UPDATE writes = %v", got)
	}
	if got := programs[1].Impact.Writes; !reflect.DeepEqual(got, []string{"app.t"}) {
		t.Errorf("DO UPDATE writes = %v", got)
	}
	if got := programs[2].Impact.Writes; !reflect.DeepEqual(got, []string{"app.counter"}) {
		t.Errorf("plain UPDATE writes = %v", got)
	}
}

func TestExecuteProcedureIsNotDynamic(t *testing.T) {
	if isDynamicPG("EXECUTE PROCEDURE audit()") || isDynamicPG("execute  function f()") {
		t.Error("EXECUTE PROCEDURE/FUNCTION must not be dynamic SQL")
	}
	if !isDynamicPG("EXECUTE format('x')") || !isDynamicPG("EXECUTE sql_text") || !isDynamicPG("EXECUTE PROCEDUREX") {
		t.Error("dynamic EXECUTE not detected")
	}
}

func TestStripSQL(t *testing.T) {
	got := StripSQL("SELECT 'a;b' /* c */ FROM \"Weird\" -- x\n", "postgresql")
	if want := "SELECT ''   FROM Weird  \n"; got != want {
		t.Errorf("StripSQL pg = %q, want %q", got, want)
	}
	got = StripSQL("SELECT 'it\\'s', \"q\" FROM `t` # c\n", "mysql")
	if want := "SELECT '', '' FROM t  \n"; got != want {
		t.Errorf("StripSQL mysql = %q, want %q", got, want)
	}
}

func TestUnanalyzedNonEmptyTableIsUnknown(t *testing.T) {
	stats, err := TableStats(map[string][]Row{"table_stats": {{
		"schema_name": "public", "name": "actor", "estimated_rows": json.Number("0"),
		"total_bytes": json.Number("8192"), "heap_bytes": json.Number("8192"),
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if stats["public.actor"].Rows != nil || *stats["public.actor"].Bytes != 8192 {
		t.Fatalf("stats = %+v", stats["public.actor"])
	}
	stats, _ = TableStats(map[string][]Row{"table_stats": {{"schema_name": "s", "name": "t", "estimated_rows": "-1", "total_bytes": nil}}})
	if stats["s.t"].Rows != nil || stats["s.t"].Bytes != nil {
		t.Fatalf("negative estimate must be unknown: %+v", stats["s.t"])
	}
}

func TestProgramJSONKeepsHistoricalKeys(t *testing.T) {
	programs := MySQLPrograms(map[string][]Row{
		"routines": {{"schema_name": "s", "name": "f", "type": "FUNCTION", "returns": "int", "body": "RETURN 1", "last_altered": "2026-01-01 10:00:00", "comment": ""}},
		"events":   {{"schema_name": "s", "name": "e", "status": "ENABLED", "event_type": "RECURRING", "interval_value": "1", "interval_field": "DAY", "body": "DO 1"}},
	})
	encoded, err := model.Marshal(programs)
	if err != nil {
		t.Fatal(err)
	}
	want := `[{"kind":"function","schema":"s","name":"f","signature":"f()","returns":"int","lastAltered":"2026-01-01 10:00:00","comment":null,"body":"RETURN 1"},` +
		`{"kind":"event","schema":"s","name":"e","status":"ENABLED","schedule":"toutes les 1 DAY","starts":null,"lastExecuted":null,"body":"DO 1"}]`
	if string(encoded) != want {
		t.Fatalf("JSON =\n%s\nwant\n%s", encoded, want)
	}
}

func TestEventTimingPreservesStructuredMetadata(t *testing.T) {
	programs := MySQLPrograms(map[string][]Row{"events": {{"schema_name": "public", "name": "tick", "status": "ENABLED", "event_type": "RECURRING", "time_zone": "SYSTEM", "system_time_zone": "UTC", "event_scheduler": "ON", "interval_value": "15", "interval_field": "MINUTE", "starts": "2026-01-01 00:00:00", "ends": "2027-01-01 00:00:00", "body": "SELECT 1"}}})
	timing := programs[0].EventSchedule
	if timing == nil || timing.TimeZone != "SYSTEM" || timing.SystemTimeZone != "UTC" || timing.Scheduler != "ON" || timing.Ends == nil || *timing.IntervalValue != "15" {
		t.Fatalf("event timing lost: %+v", timing)
	}
}
