import json
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch
from atlas.config import load_config
from atlas.dumps import listing,apply,versions


class DumpTests(unittest.TestCase):
    def setUp(self):
        self.tmp=tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root=Path(self.tmp.name)
        (self.root/'backups').mkdir()
        (self.root/'backups/a.sql').write_text('public sample dump')
        self.path=self.root/'atlas.toml'
        self.path.write_text('''[[connections]]
id="library"
driver="postgresql"
host="localhost"
database="library"
user="reader"
password_env="DB_PASSWORD"
[[dump_sources]]
connection="library"
type="directory"
path="backups"
pattern="*.sql"
local_path="current.sql"
restore_command="exit 1"
''')
        self.config=load_config(self.path,environ={'DB_PASSWORD':'unit-secret'})
        self.source=self.config.dump_sources[0]

    def test_listing_does_not_disclose_filesystem_paths(self):
        listing(self.config)
        payload=(self.config.data_dir/'s3-dumps.json').read_text()
        self.assertNotIn(str(self.root),payload)
        self.assertEqual(len(json.loads(payload)['targets'][0]['versions']),1)

    def test_failed_restore_preserves_previous_dump(self):
        target=self.root/'current.sql'
        target.write_text('previous dump')
        with self.assertRaises(RuntimeError):
            apply(self.config,'library',versions(self.source)[0]['versionId'])
        self.assertEqual(target.read_text(),'previous dump')
        self.assertFalse(list(self.root.glob('.download-*')))

    def test_success_promotes_dump_after_command_and_exports(self):
        self.source['restore_command']='test -s "$ATLAS_DUMP_PATH"'
        with patch('atlas.catalog.export') as export:
            apply(self.config,'library',versions(self.source)[0]['versionId'])
            export.assert_called_once()
        self.assertEqual((self.root/'current.sql').read_text(),'public sample dump')

    def test_s3_filters_exact_key_and_delete_markers(self):
        source=dict(type='s3-versioned',bucket='public-example',key='library.sql')
        with patch('atlas.dumps.aws',return_value=dict(Versions=[dict(Key='library.sql',VersionId='id+/=',LastModified='2026-01-01',Size=10),dict(Key='library.sql.other',VersionId='other',LastModified='2026-01-01',Size=1)],DeleteMarkers=[dict(Key='library.sql')])):
            self.assertEqual([x['versionId'] for x in versions(source)],['id+/='])
