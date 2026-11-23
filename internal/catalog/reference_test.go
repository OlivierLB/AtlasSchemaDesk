package catalog

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/OlivierLB/AtlasSchemaDesk/internal/config"
	"github.com/OlivierLB/AtlasSchemaDesk/internal/eventplan"
	"github.com/OlivierLB/AtlasSchemaDesk/internal/model"
)

func TestReferenceSchedulesWithoutDatabase(t *testing.T) {
	root := t.TempDir()
	sql := `-- CREATE EVENT fake ON SCHEDULE EVERY 1 SECOND DO SELECT 1;
SET time_zone = 'UTC';
USE library;
CREATE TABLE inventory (event VARCHAR(20));
DELIMITER $$
CREATE PROCEDURE refresh_inventory() BEGIN UPDATE inventory SET quantity = 0; END $$
CREATE DEFINER = 'demo'@'localhost' EVENT nightly
ON SCHEDULE EVERY 1 DAY STARTS '2026-01-01 02:00:00' ENDS '2027-01-01 02:00:00'
ON COMPLETION PRESERVE DISABLE COMMENT 'CREATE EVENT ghost' DO CALL refresh_inventory() $$
CREATE EVENT unanchored ON SCHEDULE EVERY 1 HOUR DO SELECT 1 $$
CREATE EVENT calculated ON SCHEDULE EVERY 1 DAY STARTS CURRENT_TIMESTAMP + INTERVAL 2 HOUR DO SELECT 1 $$
CREATE EVENT once ON SCHEDULE AT '2026-11-23 02:30:00' DO SELECT 'CREATE EVENT string_fake' $$
DELIMITER ;`
	if err := os.WriteFile(filepath.Join(root, "jobs.sql"), []byte(sql), 0600); err != nil {
		t.Fatal(err)
	}
	ref, err := Reference(&config.RoutinesRepository{Path: root, DefaultSchema: "fallback"}, "Europe/Paris")
	if err != nil {
		t.Fatal(err)
	}
	ps := ref.Engines["reference"].Programs
	if len(ps) != 5 {
		t.Fatalf("got %d definitions", len(ps))
	}
	nightly := ps[1]
	if nightly.Name != "nightly" || nightly.Schema != "library" || nightly.Status != "DISABLED" || nightly.EventSchedule.TimeZone != "UTC" {
		t.Fatalf("unexpected event metadata")
	}
	if nightly.EventSchedule.Starts == nil || *nightly.EventSchedule.Starts != "2026-01-01 02:00:00" || nightly.EventSchedule.Ends == nil {
		t.Fatal("missing literal dates")
	}
	if len(nightly.Impact.Calls) != 1 || nightly.Impact.Calls[0] != "reference:procedure:library.refresh_inventory" {
		t.Fatalf("calls: %v", nightly.Impact.Calls)
	}
	if len(nightly.Impact.IndirectWrites) != 1 || nightly.Impact.IndirectWrites[0] != "library.inventory" {
		t.Fatalf("indirect writes: %v", nightly.Impact.IndirectWrites)
	}
	nightly.Status = "ENABLED"
	from := time.Date(2026, 11, 23, 2, 0, 0, 0, time.UTC)
	report := eventplan.Build(ref.Engines, ref.GeneratedAt, from, from.Add(time.Hour), 0)
	if len(report.Events) != 2 || len(report.Unknown) != 2 {
		t.Fatalf("matches=%d unknown=%d", len(report.Events), len(report.Unknown))
	}
}

func TestReferenceVersionCommentsAndUnknownExpressions(t *testing.T) {
	ps := sourcePrograms("/*!50106 CREATE*/ /*!50117 DEFINER=`demo`@`localhost`*/ /*!50106 EVENT `library`.`tick` ON SCHEDULE EVERY 5 MINUTE STARTS '2026-01-01 00:00:00' DO SELECT 1 */;", "jobs.sql", "public", "UTC")
	if len(ps) != 1 || ps[0].Schema != "library" || ps[0].EventSchedule.Starts == nil {
		t.Fatal("version-comment event not parsed")
	}
	for _, sql := range []string{
		"CREATE EVENT tick ON SCHEDULE EVERY 1 DAY STARTS '2026-01-01' + INTERVAL 2 HOUR DO SELECT 1;",
		"CREATE EVENT tick ON SCHEDULE EVERY 1 DAY STARTS '2026-01-01' ENDS CURRENT_TIMESTAMP DO SELECT 1;",
	} {
		p := sourcePrograms(sql, "jobs.sql", "library", "UTC")[0]
		from := time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC)
		report := eventplan.Build(map[string]*model.Engine{"reference": {Programs: []*model.Program{p}}}, "", from, from.Add(time.Hour), 0)
		if len(report.Unknown) != 1 {
			t.Fatal("expression silently guessed")
		}
	}
}

func TestReferenceRejectsOutsideSymlink(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "private.sql")
	if err := os.WriteFile(outside, []byte("CREATE EVENT forbidden ON SCHEDULE EVERY 1 SECOND DO SELECT 1"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "linked.sql")); err != nil {
		t.Fatal(err)
	}
	ref, err := Reference(&config.RoutinesRepository{Path: root}, "UTC")
	if err != nil {
		t.Fatal(err)
	}
	if ref.FilesRead != 0 || len(ref.Engines["reference"].Programs) != 0 || len(ref.Warnings) == 0 {
		t.Fatal("outside symlink read")
	}
	if ref.Engines["reference"].Programs == nil {
		t.Fatal("empty reference must serialize programs as an array")
	}
}
