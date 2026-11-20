#!/usr/bin/env python3
"""Unit tests of scripts/sql_console.py (statement splitting, classification, confirmations)."""

import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent.parent / "scripts"))
from atlas.sql_console import classify, confirmation_reasons, split_statements  # noqa: E402


class SplitTests(unittest.TestCase):
    def test_simple_split(self):
        self.assertEqual(split_statements("SELECT 1; SELECT 2;", "mysql"), ["SELECT 1", "SELECT 2"])

    def test_semicolons_in_strings_and_comments(self):
        sql = "SELECT 'a;b', \"c;d\", `e;f` FROM t; -- x; y\nSELECT 2 /* ; */"
        self.assertEqual(split_statements(sql, "mysql"), ["SELECT 'a;b', \"c;d\", `e;f` FROM t", "-- x; y\nSELECT 2 /* ; */"])

    def test_mysql_escaped_quote(self):
        self.assertEqual(len(split_statements(r"SELECT 'it\'s;'; SELECT 1", "mysql")), 2)

    def test_pg_dollar_quotes(self):
        sql = "DO $$ BEGIN PERFORM 1; END $$; SELECT 1"
        self.assertEqual(split_statements(sql, "postgresql"), ["DO $$ BEGIN PERFORM 1; END $$", "SELECT 1"])

    def test_mysql_routine_kept_whole(self):
        sql = "CREATE PROCEDURE p() BEGIN SELECT 1; SELECT 2; END;"
        self.assertEqual(split_statements(sql, "mysql"), ["CREATE PROCEDURE p() BEGIN SELECT 1; SELECT 2; END"])

    def test_empty_and_comment_only(self):
        self.assertEqual(split_statements(" ; -- rien\n;", "mysql"), [])


class ClassifyTests(unittest.TestCase):
    def test_pg_select_into_and_explain_options_are_not_reads(self):
        self.assertEqual(classify('SELECT 1 INTO created_table', 'postgresql'), 'ddl')
        self.assertEqual(classify('EXPLAIN (FORMAT JSON, ANALYZE true) UPDATE actor SET id=1', 'postgresql'), 'write')

    def test_kinds(self):
        cases = {
            "select * from t": "read", "  (SELECT 1)": "read", "SHOW TABLES": "read", "explain select 1": "read",
            "WITH x AS (SELECT 1) SELECT * FROM x": "read", "WITH x AS (DELETE FROM t RETURNING *) SELECT * FROM x": "write",
            "EXPLAIN ANALYZE UPDATE t SET a = 1": "write", "insert into t values (1)": "write", "CALL p()": "write",
            "SELECT 1 INTO OUTFILE '/tmp/x'": "write", "alter table t add c int": "ddl", "TRUNCATE t": "ddl",
            "SET @a = 1": "session", "begin": "control", "COMMIT": "control", "rollback": "control", "frobnicate": "write",
            "-- commentaire\nSELECT 1": "read",
        }
        for sql, kind in cases.items():
            with self.subTest(sql=sql):
                self.assertEqual(classify(sql, "mysql"), kind)


class ConfirmationTests(unittest.TestCase):
    def reasons(self, sql, engine="mysql", open_tx=False):
        return confirmation_reasons(sql, engine, classify(sql, engine), open_tx)

    def test_update_delete_without_where(self):
        self.assertTrue(self.reasons("UPDATE t SET a = 1"))
        self.assertTrue(self.reasons("DELETE FROM t"))
        self.assertFalse(self.reasons("UPDATE t SET a = 1 WHERE id = 2"))
        self.assertTrue(self.reasons("UPDATE t SET a = 'WHERE'"), "WHERE inside a string does not count")

    def test_drop_truncate_ddl(self):
        self.assertTrue(self.reasons("DROP TABLE t", "postgresql"))
        self.assertTrue(self.reasons("TRUNCATE t", "postgresql"))
        self.assertFalse(self.reasons("ALTER TABLE t ADD c int", "postgresql"), "PG structure changes are transactional")
        self.assertTrue(self.reasons("ALTER TABLE t ADD c int", "mysql"), "MySQL structure changes commit implicitly")
        self.assertIn("transaction en cours", " ".join(self.reasons("CREATE INDEX i ON t (a)", "mysql", open_tx=True)))

    def test_plain_statements_run_directly(self):
        for sql in ("SELECT * FROM t", "INSERT INTO t VALUES (1)", "DELETE FROM t WHERE id = 1"):
            with self.subTest(sql=sql):
                self.assertEqual(self.reasons(sql), [])


if __name__ == "__main__":
    unittest.main(verbosity=1)
