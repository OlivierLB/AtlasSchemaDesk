import json
import tempfile
import unittest
from pathlib import Path
from atlas.config import load_config
from atlas.db import Database, parameters


class ConfigTests(unittest.TestCase):
    def setUp(self):
        self.tmp=tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root=Path(self.tmp.name)
        self.path=self.root/'atlas.toml'
        self.text='''[[connections]]
id="library"
driver="postgresql"
host="127.0.0.1"
database="library"
user="reader"
password_env="DB_PASSWORD"
'''

    def load(self, text=None, env=None):
        self.path.write_text(text or self.text)
        return load_config(self.path,environ={'DB_PASSWORD':'unit-secret'} if env is None else env)

    def test_defaults_and_public_redaction(self):
        config=self.load()
        self.assertFalse(config.connections[0].allow_writes)
        self.assertNotIn('unit-secret',repr(config))
        self.assertNotIn('unit-secret',json.dumps(config.public()))
        self.assertNotIn('password',json.dumps(config.public()))
        self.assertEqual(config.redact('bad unit-secret'),'bad [redacted]')

    def test_local_dotenv_and_environment_precedence(self):
        (self.root/'.env').write_text('DB_PASSWORD="from-file"\n')
        self.assertEqual(self.load(env={}).connections[0].password,'from-file')
        self.assertEqual(self.load().connections[0].password,'unit-secret')

    def test_missing_password_host_and_duplicate(self):
        for text,env in [(self.text,{}),(self.text.replace('127.0.0.1','remote.invalid'),{'DB_PASSWORD':'x'}),(self.text+self.text,{'DB_PASSWORD':'x'})]:
            with self.subTest(text=text),self.assertRaises(ValueError):
                self.load(text,env)

    def test_types_color_and_inline_password(self):
        for extra in ['allow_writes="false"','color="red; display:none"','password="inline"','port=0']:
            with self.subTest(extra=extra),self.assertRaises(ValueError):
                self.load(self.text+'\n'+extra)

    def test_read_only_session_bypasses(self):
        config=self.load()
        db=Database(config,config.connections[0])
        for sql in ['UPDATE actor SET name=1','SET default_transaction_read_only=off','RESET ALL','PREPARE x AS DELETE FROM actor','EXECUTE x', 'SELECT 1 INTO actor', 'SELECT 1 /*!; SET SESSION TRANSACTION READ WRITE */']:
            with self.subTest(sql=sql),self.assertRaises(ValueError):
                db.validate_sql([{'sql':sql}])
        db.validate_sql([{'sql':'SELECT 1'}])

    def test_question_mark_parameters_preserve_strings(self):
        self.assertEqual(parameters("SELECT '?' AS literal, ? AS value, '100%'",'postgresql'),"SELECT '?' AS literal, %s AS value, '100%%'")


if __name__=='__main__':
    unittest.main()
