"""Verify the publication audit against isolated synthetic Git indexes."""
import json
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest

SCRIPT = Path(__file__).resolve().parents[1] / "scripts/check_public.py"


class PublicAuditTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.git("init", "-q")
        (self.root / "scripts").mkdir()
        shutil.copyfile(SCRIPT, self.root / "scripts/check_public.py")
        (self.root / ".gitignore").write_text(".local/\n")
        self.git("add", ".")

    def git(self, *args):
        return subprocess.run(["git", *args], cwd=self.root, check=True, capture_output=True)

    def audit(self, *args):
        return subprocess.run([sys.executable, "scripts/check_public.py", *args], cwd=self.root, text=True, capture_output=True)

    def test_public_sources_need_no_private_marker_file(self):
        self.assertEqual(self.audit().returncode, 0)

    def test_safe_worktree_cannot_hide_sensitive_staged_content(self):
        token = "ghp_" + "a" * 36
        path = self.root / "sample.txt"
        path.write_text(token)
        self.git("add", "sample.txt")
        path.write_text("sanitized working copy")
        result = self.audit("--staged")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("sample.txt: credential-like token", result.stderr)
        self.assertNotIn(token, result.stdout + result.stderr)

    def test_candidate_worktree_is_checked_as_well(self):
        path = self.root / "sample.txt"
        path.write_text("public example")
        self.git("add", "sample.txt")
        path.write_text("10." + "23.45.67")
        self.assertEqual(self.audit("--staged").returncode, 0)
        self.assertNotEqual(self.audit().returncode, 0)

    def test_force_added_private_file_is_rejected(self):
        (self.root / ".local").mkdir()
        (self.root / ".local/sample.json").write_text("{}")
        self.git("add", "-f", ".local/sample.json")
        self.assertIn("local-only file", self.audit("--staged").stderr)

    def test_symlink_cannot_publish_local_configuration(self):
        (self.root / "public-link").symlink_to(".local/settings.toml")
        self.git("add", "public-link")
        self.assertIn("non-regular file", self.audit("--staged").stderr)

    def test_local_markers_are_applied_without_printing_them(self):
        marker = "synthetic-private-fixture"
        (self.root / ".local").mkdir()
        (self.root / ".local/public-audit-markers.json").write_text(json.dumps([marker]))
        (self.root / "sample.txt").write_text(marker)
        self.git("add", "sample.txt")
        result = self.audit("--staged")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("private local marker", result.stderr)
        self.assertNotIn(marker, result.stdout + result.stderr)


if __name__ == "__main__":
    unittest.main()
