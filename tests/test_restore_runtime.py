"""Restore command integration tests, with fake Docker; never touch a database."""
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

SPEC = importlib.util.spec_from_file_location("restore_runtime", Path(__file__).resolve().parents[1] / "scripts/restore_runtime.py")
runtime = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(runtime)


class RestoreRuntimeTests(unittest.TestCase):
    def test_tty_removed_but_sql_client_flags_and_stdin_preserved(self):
        self.assertEqual(runtime.docker_arguments(["exec", "-e", "PGPASSWORD=example", "-it", "db", "psql", "-t"]), ["exec", "-e", "PGPASSWORD=example", "-i", "db", "psql", "-t"])
        self.assertEqual(runtime.docker_arguments(["run", "--rm", "--tty", "-v", "/data:/data", "image", "sh", "-t"]), ["run", "--rm", "-v", "/data:/data", "image", "sh", "-t"])
        self.assertEqual(runtime.docker_arguments(["compose", "up", "-d"]), ["compose", "up", "-d"])

    def test_cleanup_mount_is_only_retired_directory_and_uses_local_image(self):
        with tempfile.TemporaryDirectory() as root:
            old = Path(root) / "old"
            old.mkdir()
            mounts = json.dumps([dict(Destination="/var/lib/mysql", Source=str(Path(root) / "current"))])
            with patch.object(runtime.subprocess, "check_output", side_effect=[mounts, "sha256:test-image\n"]), patch.object(runtime.subprocess, "run") as run:
                self.assertTrue(runtime.cleanup_old("/docker", "db", root))
            args = run.call_args.args[0]
            self.assertIn(f"type=bind,src={old},dst=/cleanup", args)
            self.assertIn("--pull=never", args)
            self.assertIn("--network=none", args)
            self.assertIn("--entrypoint=/bin/sh", args)
            self.assertNotIn("sudo", args)
            self.assertFalse(old.exists())

    def test_cleanup_refuses_unrelated_directory_or_symlink(self):
        with tempfile.TemporaryDirectory() as root:
            mounts = json.dumps([dict(Destination="/var/lib/mysql", Source="/other/current")])
            with patch.object(runtime.subprocess, "check_output", return_value=mounts), patch.object(runtime.subprocess, "run") as run:
                self.assertFalse(runtime.cleanup_old("/docker", "db", root))
                run.assert_not_called()
            (Path(root) / "old").symlink_to("/tmp", target_is_directory=True)
            mounts = json.dumps([dict(Destination="/var/lib/mysql", Source=str(Path(root) / "current"))])
            with patch.object(runtime.subprocess, "check_output", return_value=mounts), patch.object(runtime.subprocess, "run") as run:
                self.assertFalse(runtime.cleanup_old("/docker", "db", root))
                run.assert_not_called()


if __name__ == "__main__":
    unittest.main()
