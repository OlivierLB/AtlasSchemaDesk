package db

// Integration tests against the disposable sample databases of
// docker-compose.test.yml (see tests/run.sh). Skipped without SULCATA_TEST_CONFIG.

import (
	"os"
	"testing"

	"github.com/OlivierLB/Sulcata/internal/config"
	"github.com/OlivierLB/Sulcata/internal/sqlconsole"
)

func integrationConfig(t *testing.T) *config.Config {
	path := os.Getenv("SULCATA_TEST_CONFIG")
	if path == "" {
		t.Skip("requires disposable sample databases (tests/run.sh)")
	}
	cfg, err := config.Load(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func open(t *testing.T, cfg *config.Config, name string) *Database {
	options, err := cfg.Connection(name)
	if err != nil {
		t.Fatal(err)
	}
	database := NewDatabase(cfg, options)
	t.Cleanup(database.Close)
	return database
}

func run(t *testing.T, database *Database, sql string, params ...any) *SQLOutcome {
	t.Helper()
	statements := []Statement{}
	for _, text := range sqlconsole.Split(sql, database.options.Driver) {
		statements = append(statements, Statement{SQL: text, Kind: sqlconsole.Classify(text, database.options.Driver)})
	}
	outcome, err := database.SQL(statements, params)
	if err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	return outcome
}

func scalar(t *testing.T, database *Database, sql string) string {
	t.Helper()
	rows, err := database.Query(sql)
	if err != nil || len(rows) == 0 {
		t.Fatalf("%s: %v %v", sql, rows, err)
	}
	for _, v := range rows[0] {
		if text, ok := v.(string); ok {
			return text
		}
	}
	return ""
}

func reset(t *testing.T, cfg *config.Config) {
	for _, name := range []string{"library-write", "store-write", "older-write"} {
		database := open(t, cfg, name)
		for _, sql := range []string{"UPDATE sulcata_probe SET value='original' WHERE id=1", "DELETE FROM sulcata_audit"} {
			if err := database.command(sql); err != nil {
				t.Fatal(err)
			}
		}
		if !database.pg() {
			_ = database.command("DROP TABLE IF EXISTS sulcata_structure_probe")
		}
	}
}

func TestIntegrationReadOnlyEnforcedByDatabaseAndServer(t *testing.T) {
	cfg := integrationConfig(t)
	reset(t, cfg)
	for _, name := range []string{"library", "store", "older"} {
		database := open(t, cfg, name)
		if _, err := database.Query("SELECT * FROM sulcata_probe"); err != nil {
			t.Fatal(err)
		}
		if err := database.command("UPDATE sulcata_probe SET value='forbidden' WHERE id=1"); err == nil {
			t.Errorf("%s: the database accepted a write on a read-only session", name)
		}
		for _, sql := range []string{"UPDATE sulcata_probe SET value='forbidden' WHERE id=1", "SET SESSION TRANSACTION READ WRITE"} {
			if _, err := database.SQL([]Statement{{SQL: sql}}, nil); err == nil {
				t.Errorf("%s: %s accepted", name, sql)
			}
		}
		if got := scalar(t, database, "SELECT value FROM sulcata_probe WHERE id=1"); got != "original" {
			t.Errorf("%s: value = %s", name, got)
		}
	}
}

func TestIntegrationReadExpressionCannotDisableNextStatementProtection(t *testing.T) {
	cfg := integrationConfig(t)
	reset(t, cfg)
	writer := open(t, cfg, "library-write")
	if err := writer.command("CREATE OR REPLACE FUNCTION public.sulcata_try_write() RETURNS void LANGUAGE sql AS $$ UPDATE public.sulcata_probe SET value='forbidden' WHERE id=1 $$"); err != nil {
		t.Fatal(err)
	}
	database := open(t, cfg, "library")
	if outcome := run(t, database, "SELECT set_config('default_transaction_read_only','off',false)"); outcome.Error != nil {
		t.Fatal(outcome.Error.Message)
	}
	if outcome := run(t, database, "SELECT public.sulcata_try_write()"); outcome.Error == nil {
		t.Fatal("write through a function succeeded on a read-only session")
	}
	if got := scalar(t, database, "SELECT value FROM sulcata_probe WHERE id=1"); got != "original" {
		t.Fatalf("value = %s", got)
	}
}

func TestIntegrationTransactionsRollbackSavepointsAndGrid(t *testing.T) {
	cfg := integrationConfig(t)
	reset(t, cfg)
	for _, name := range []string{"library-write", "store-write", "older-write"} {
		database := open(t, cfg, name)
		if outcome := run(t, database, "UPDATE sulcata_probe SET value=? WHERE id=?", "pending", "1"); !outcome.TxOpen || outcome.Error != nil {
			t.Fatalf("%s: %+v", name, outcome)
		}
		if got := scalar(t, database, "SELECT value FROM sulcata_probe WHERE id=1"); got != "pending" {
			t.Fatalf("%s: value = %s", name, got)
		}
		if outcome := run(t, database, "SELECT * FROM missing_public_sample_table"); outcome.Error == nil || !outcome.TxOpen {
			t.Fatalf("%s: failing statement must keep the transaction: %+v", name, outcome)
		}
		rows, err := database.Rows(RowsQuery{Schema: database.options.DefaultSchema, Table: "sulcata_probe", Limit: 100})
		if err != nil || rows.Rows[0]["value"] != "pending" {
			t.Fatalf("%s: grid %+v %v", name, rows, err)
		}
		if database.pg() {
			if _, err := database.Rows(RowsQuery{Schema: "public", Table: "missing_public_sample_table", Limit: 100}); err == nil {
				t.Fatal("missing table read succeeded")
			}
			if got := scalar(t, database, "SELECT value FROM sulcata_probe"); got != "pending" {
				t.Fatalf("%s: failed read aborted the transaction (value %s)", name, got)
			}
		}
		if done, err := database.End("rollback"); err != nil || !done {
			t.Fatalf("%s: rollback %v %v", name, done, err)
		}
		if got := scalar(t, database, "SELECT value FROM sulcata_probe"); got != "original" {
			t.Fatalf("%s: value after rollback = %s", name, got)
		}
		if got := scalar(t, database, "SELECT count(*) AS n FROM sulcata_audit"); got != "0" {
			t.Fatalf("%s: trigger writes survived rollback: %s", name, got)
		}
	}
}

func TestIntegrationCommit(t *testing.T) {
	cfg := integrationConfig(t)
	reset(t, cfg)
	database := open(t, cfg, "library-write")
	run(t, database, "UPDATE sulcata_probe SET value='committed' WHERE id=1")
	if _, err := database.End("commit"); err != nil {
		t.Fatal(err)
	}
	if got := scalar(t, open(t, cfg, "library"), "SELECT value FROM sulcata_probe"); got != "committed" {
		t.Fatalf("value = %s", got)
	}
	reset(t, cfg)
}

func TestIntegrationMySQLStructureCommitsEvenOnDDLError(t *testing.T) {
	cfg := integrationConfig(t)
	reset(t, cfg)
	database := open(t, cfg, "store-write")
	for _, ddl := range []string{"CREATE TABLE sulcata_structure_probe (id integer)", "CREATE TABLE sulcata_structure_probe (id integer)"} {
		run(t, database, "UPDATE sulcata_probe SET value='implicit' WHERE id=1")
		if outcome := run(t, database, ddl); !outcome.ImplicitCommit || outcome.TxOpen {
			t.Fatalf("%s: %+v", ddl, outcome)
		}
	}
	reset(t, cfg)
}

func TestIntegrationFiltersRefsLimitsAndCells(t *testing.T) {
	cfg := integrationConfig(t)
	for _, name := range []string{"library", "store", "older"} {
		database := open(t, cfg, name)
		one := "1"
		rows, err := database.Rows(RowsQuery{Schema: database.options.DefaultSchema, Table: "actor",
			Filters: []Filter{{Column: "actor_id", Mode: "cmp", Op: ">=", Value: &one}},
			Sort:    &Sort{Column: "actor_id", Dir: "asc"}, Limit: 2})
		if err != nil || len(rows.Rows) == 0 || len(rows.Rows) > 2 {
			t.Fatalf("%s: rows %+v %v", name, rows, err)
		}
		like := "a"
		if _, err := database.Rows(RowsQuery{Schema: database.options.DefaultSchema, Table: "actor",
			Filters: []Filter{{Column: "first_name", Mode: "like", Value: &like}}, Limit: 5}); err != nil {
			t.Fatalf("%s: like filter %v", name, err)
		}
		counts, err := database.Refs([]Ref{{Schema: database.options.DefaultSchema, Table: "actor", Column: "actor_id", Value: "1"}}, 1)
		if err != nil || counts[0].Count == nil || *counts[0].Count != 1 {
			t.Fatalf("%s: refs %+v %v", name, counts, err)
		}
	}
	database := open(t, cfg, "library")
	outcome := run(t, database, "SELECT true AS b, '\\x0102'::bytea AS raw, repeat('é', 2100) AS long, NULL AS nothing, '{\"a\": 1}'::jsonb AS doc")
	row := (*outcome.Results[0].Rows)[0]
	if row[0] != "true" || row[1] != (BinaryCell{true, 2}) || row[3] != nil || row[4] != `{"a": 1}` {
		t.Fatalf("cells = %#v", row)
	}
	if long, ok := row[2].(TruncatedCell); !ok || long.Length != 2100 || len([]rune(long.Value)) != 2000 {
		t.Fatalf("truncated cell = %#v", row[2])
	}
	store := open(t, cfg, "store")
	outcome = run(t, store, "SELECT CAST('ab' AS BINARY) AS raw, 1.5 AS n, CAST(NULL AS CHAR) AS nothing")
	if row := (*outcome.Results[0].Rows)[0]; row[0] != (BinaryCell{true, 2}) || row[1] != "1.5" || row[2] != nil {
		t.Fatalf("mysql cells = %#v", row)
	}
	outcome = run(t, open(t, cfg, "store-write"), "CALL sulcata_probe_read()")
	if outcome.Error != nil || outcome.Results[0].Rows == nil {
		t.Fatalf("CALL with a result set: %+v %+v", outcome, outcome.Error)
	}
}
