import tempfile
import unittest
from pathlib import Path
from atlas.analysis import analyse,table_stats
from atlas.catalog import link_sources


class AnalysisTests(unittest.TestCase):
    def test_direct_indirect_and_dynamic_writes(self):
        tables=[dict(schema='public',name='actor'),dict(schema='public',name='audit')]
        programs=[dict(schema='public',name='log',kind='procedure',body='INSERT INTO public.audit VALUES(1)'),dict(schema='public',name='change_actor',kind='procedure',body='UPDATE public.actor SET id=1; CALL public.log()'),dict(schema='public',name='dynamic_actor',kind='function',body="EXECUTE 'DELETE FROM public.actor'")]
        index=analyse('postgresql',programs,tables)
        self.assertIn('postgresql:procedure:public.change_actor',index['public.audit']['indirectWrites'])
        self.assertEqual(programs[2]['impact']['probableWrites'],['public.actor'])

    def test_unanalyzed_nonempty_table_is_unknown(self):
        self.assertIsNone(table_stats(dict(table_stats=[dict(schema_name='public',name='actor',estimated_rows=0,total_bytes=8192,heap_bytes=8192)]))['public.actor']['rows'])

    def test_latin1_repository_is_optional(self):
        with tempfile.TemporaryDirectory() as tmp:
            root=Path(tmp)
            file=root/'public/functions/greet.sql'
            file.parent.mkdir(parents=True)
            file.write_bytes("CREATE FUNCTION greet() RETURNS text AS 'SELECT café';".encode('latin-1'))
            program=dict(schema='public',name='greet',kind='function',body='SELECT café')
            link_sources([program],dict(path=tmp))
            self.assertEqual(program['source']['status'],'same')
