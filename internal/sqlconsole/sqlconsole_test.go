package sqlconsole

import (
	"reflect"
	"strings"
	"testing"
)

func TestSplit(t *testing.T) {
	cases := []struct {
		sql, engine string
		want        []string
	}{
		{"SELECT 1; SELECT 2;", "mysql", []string{"SELECT 1", "SELECT 2"}},
		{"SELECT 'a;b', \"c;d\", `e;f` FROM t; -- x; y\nSELECT 2 /* ; */", "mysql",
			[]string{"SELECT 'a;b', \"c;d\", `e;f` FROM t", "-- x; y\nSELECT 2 /* ; */"}},
		{"DO $$ BEGIN PERFORM 1; END $$; SELECT 1", "postgresql", []string{"DO $$ BEGIN PERFORM 1; END $$", "SELECT 1"}},
		{"CREATE PROCEDURE p() BEGIN SELECT 1; SELECT 2; END;", "mysql", []string{"CREATE PROCEDURE p() BEGIN SELECT 1; SELECT 2; END"}},
		{" ; -- rien\n;", "mysql", []string{}},
		{"SELECT 'été;'; SELECT 'ok'", "postgresql", []string{"SELECT 'été;'", "SELECT 'ok'"}},
	}
	for _, c := range cases {
		if got := Split(c.sql, c.engine); !reflect.DeepEqual(got, c.want) {
			t.Errorf("Split(%q) = %q, want %q", c.sql, got, c.want)
		}
	}
	if got := Split(`SELECT 'it\'s;'; SELECT 1`, "mysql"); len(got) != 2 {
		t.Errorf("MySQL escaped quote: %q", got)
	}
}

func TestClassify(t *testing.T) {
	if got := Classify("SELECT 1 INTO created_table", "postgresql"); got != DDL {
		t.Errorf("PG SELECT INTO = %s", got)
	}
	if got := Classify("EXPLAIN (FORMAT JSON, ANALYZE true) UPDATE actor SET id=1", "postgresql"); got != Write {
		t.Errorf("EXPLAIN ANALYZE options = %s", got)
	}
	cases := map[string]string{
		"select * from t": Read, "  (SELECT 1)": Read, "SHOW TABLES": Read, "explain select 1": Read,
		"WITH x AS (SELECT 1) SELECT * FROM x": Read, "WITH x AS (DELETE FROM t RETURNING *) SELECT * FROM x": Write,
		"EXPLAIN ANALYZE UPDATE t SET a = 1": Write, "insert into t values (1)": Write, "CALL p()": Write,
		"SELECT 1 INTO OUTFILE '/tmp/x'": Write, "alter table t add c int": DDL, "TRUNCATE t": DDL,
		"SET @a = 1": Session, "begin": Control, "COMMIT": Control, "rollback": Control, "frobnicate": Write,
		"-- commentaire\nSELECT 1": Read,
	}
	for sql, want := range cases {
		if got := Classify(sql, "mysql"); got != want {
			t.Errorf("Classify(%q) = %s, want %s", sql, got, want)
		}
	}
}

func reasons(sql, engine string, open bool) []string {
	return ConfirmationReasons(sql, engine, Classify(sql, engine), open)
}

func TestConfirmations(t *testing.T) {
	for _, sql := range []string{"UPDATE t SET a = 1", "DELETE FROM t", "UPDATE t SET a = 'WHERE'"} {
		if len(reasons(sql, "mysql", false)) == 0 {
			t.Errorf("%q must need confirmation", sql)
		}
	}
	if len(reasons("UPDATE t SET a = 1 WHERE id = 2", "mysql", false)) != 0 {
		t.Error("UPDATE with WHERE must run directly")
	}
	for _, sql := range []string{"DROP TABLE t", "TRUNCATE t"} {
		if len(reasons(sql, "postgresql", false)) == 0 {
			t.Errorf("%q must need confirmation", sql)
		}
	}
	if len(reasons("ALTER TABLE t ADD c int", "postgresql", false)) != 0 {
		t.Error("PG structure changes are transactional")
	}
	if len(reasons("ALTER TABLE t ADD c int", "mysql", false)) == 0 {
		t.Error("MySQL structure changes commit implicitly")
	}
	if !strings.Contains(strings.Join(reasons("CREATE INDEX i ON t (a)", "mysql", true), " "), "transaction en cours") {
		t.Error("open transaction must be mentioned")
	}
	for _, sql := range []string{"SELECT * FROM t", "INSERT INTO t VALUES (1)", "DELETE FROM t WHERE id = 1"} {
		if got := reasons(sql, "mysql", false); len(got) != 0 {
			t.Errorf("%q: unexpected reasons %q", sql, got)
		}
	}
}

func TestPlaceholdersPreserveLiterals(t *testing.T) {
	got := Placeholders("SELECT '?' AS literal, ? AS value, '100%', ? -- ?", "postgresql")
	if want := "SELECT '?' AS literal, $1 AS value, '100%', $2 -- ?"; got != want {
		t.Errorf("Placeholders = %q, want %q", got, want)
	}
}
