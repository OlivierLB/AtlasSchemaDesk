import json
import os
import unittest
from pathlib import Path
from atlas.config import load_config
from atlas.db import Database
from atlas.catalog import export


@unittest.skipUnless(os.environ.get('ATLAS_TEST_CONFIG'),'Run tests/run.sh for disposable sample databases')
class DatabaseIntegration(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.config=load_config(os.environ['ATLAS_TEST_CONFIG'])

    def database(self,name):
        db=Database(self.config,self.config.connection(name))
        self.addCleanup(db.close)
        return db

    def setUp(self):
        for name in ('library-write','store-write','older-write'):
            db=self.database(name)
            db.command("UPDATE atlas_probe SET value='original' WHERE id=1")
            db.command('DELETE FROM atlas_audit')
            if not db.pg:
                db.command('DROP TABLE IF EXISTS atlas_structure_probe')

    def sql(self,db,text,params=None):
        return db.sql(dict(statements=[dict(sql=text)],params=params or []))

    def test_read_only_enforced_by_database_and_server(self):
        for name in ('library','store','older'):
            with self.subTest(name=name):
                db=self.database(name)
                self.assertTrue(db.query('SELECT * FROM atlas_probe'))
                with self.assertRaises(Exception):
                    db.command("UPDATE atlas_probe SET value='forbidden' WHERE id=1")
                with self.assertRaises(ValueError):
                    self.sql(db,"UPDATE atlas_probe SET value='forbidden' WHERE id=1")
                with self.assertRaises(ValueError):
                    self.sql(db,'SET SESSION TRANSACTION READ WRITE')
                self.assertEqual(db.query('SELECT value FROM atlas_probe WHERE id=1')[0]['value'],'original')

    def test_read_expression_cannot_disable_next_statement_protection(self):
        writer=self.database('library-write')
        writer.command("CREATE OR REPLACE FUNCTION public.atlas_try_write() RETURNS void LANGUAGE sql AS $$ UPDATE public.atlas_probe SET value='forbidden' WHERE id=1 $$")
        db=self.database('library')
        result=self.sql(db,"SELECT set_config('default_transaction_read_only','off',false)")
        self.assertIsNone(result['error'])
        result=self.sql(db,'SELECT public.atlas_try_write()')
        self.assertIsNotNone(result['error'])
        self.assertEqual(db.query('SELECT value FROM atlas_probe WHERE id=1')[0]['value'],'original')

    def test_transactions_rollback_savepoints_and_grid(self):
        for name in ('library-write','store-write','older-write'):
            with self.subTest(name=name):
                db=self.database(name)
                self.assertTrue(self.sql(db,"UPDATE atlas_probe SET value=? WHERE id=?",['pending',1])['txOpen'])
                self.assertEqual(db.query('SELECT value FROM atlas_probe WHERE id=1')[0]['value'],'pending')
                self.assertIsNotNone(self.sql(db,'SELECT * FROM missing_public_sample_table')['error'])
                rows=db.rows(dict(schema=db.options.default_schema,table='atlas_probe',filters=[]))
                self.assertEqual(rows['rows'][0]['value'],'pending')
                if db.pg:
                    with self.assertRaises(Exception):
                        db.rows(dict(schema='public',table='missing_public_sample_table',filters=[]))
                    self.assertEqual(db.query('SELECT value FROM atlas_probe')[0]['value'],'pending')
                db.end('rollback')
                self.assertEqual(db.query('SELECT value FROM atlas_probe')[0]['value'],'original')
                self.assertEqual(db.query('SELECT count(*) AS n FROM atlas_audit')[0]['n'],0)

    def test_commit_and_restore_original(self):
        db=self.database('library-write')
        self.sql(db,"UPDATE atlas_probe SET value='committed' WHERE id=1")
        db.end('commit')
        self.assertEqual(self.database('library').query('SELECT value FROM atlas_probe')[0]['value'],'committed')
        try:
            self.sql(db,"UPDATE atlas_probe SET value='original' WHERE id=1")
            self.sql(db,'DELETE FROM atlas_audit')
            db.end('commit')
        finally:
            db.end('rollback')

    def test_mysql_structure_commits_even_on_ddl_error(self):
        db=self.database('store-write')
        for ddl in ['CREATE TABLE atlas_structure_probe (id integer)', 'CREATE TABLE atlas_structure_probe (id integer)']:
            self.sql(db,"UPDATE atlas_probe SET value='implicit' WHERE id=1")
            result=self.sql(db,ddl)
            self.assertTrue(result['implicitCommit'])
            self.assertFalse(result['txOpen'])
        self.sql(db,'DROP TABLE atlas_structure_probe')
        self.sql(db,"UPDATE atlas_probe SET value='original' WHERE id=1")
        self.sql(db,'DELETE FROM atlas_audit')
        db.end('commit')

    def test_filters_refs_and_limits(self):
        for name in ('library','store','older'):
            db=self.database(name)
            result=db.rows(dict(schema=db.options.default_schema,table='actor',filters=[dict(column='actor_id',mode='cmp',op='>=',value=1)],sort=dict(column='actor_id',dir='asc'),limit=2))
            self.assertLessEqual(len(result['rows']),2)
            self.assertTrue(result['rows'])
            result=db.refs(dict(refs=[dict(schema=db.options.default_schema,table='actor',column='actor_id',value=1)],cap=1))
            self.assertEqual(result['counts'][0]['count'],1)

    def test_catalog_export_and_impacts(self):
        export(self.config)
        schema=json.loads((self.config.data_dir/'schema.json').read_text())
        programs=json.loads((self.config.data_dir/'programs.json').read_text())
        self.assertEqual(set(schema['sources']),{c.id for c in self.config.connections})
        for name in ('library','store','older'):
            self.assertTrue(any(t['name']=='actor' for t in schema['sources'][name]))
            source=programs['engines'][name]
            self.assertTrue(any(p['name']=='atlas_probe_changed' for p in source['programs']))
            table='sakila.atlas_audit' if name=='store' else 'public.atlas_audit'
            self.assertTrue(source['tableImpacts'][table]['writes'])
        self.assertTrue(any(p['kind']=='event' for p in programs['engines']['store']['programs']))
        self.assertTrue((self.config.data_dir/'snapshots/index.json').exists())
