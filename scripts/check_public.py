#!/usr/bin/env python3
"""Audit staged and candidate files without printing sensitive matched values.

Organization-specific denylisted strings belong in the ignored local file
.local/public-audit-markers.json, never in this public repository.
"""
import argparse
import json
import re
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
LOCAL_MARKERS = ROOT / ".local/public-audit-markers.json"
PRIVATE_DIRECTORIES = {"private", "local", ".local", "data", ".claude", ".codex", ".agents", ".superdesign"}
PATTERNS = (
    ("private key", re.compile(rb"-----BEGIN (?:RSA |EC |OPENSSH )?PRIVATE KEY-----", re.I)),
    ("credential-like token", re.compile(rb"\b(?:AKIA|ASIA)[A-Z0-9]{16}\b|\bgh[pousr]_[a-zA-Z0-9]{36}\b|\bgithub_pat_[a-zA-Z0-9_]{40,}\b|\bxox[baprs]-[a-zA-Z0-9-]{20,}", re.I)),
    ("private network address", re.compile(rb"\b(?:10\.\d{1,3}\.\d{1,3}\.\d{1,3}|192\.168\.\d{1,3}\.\d{1,3}|172\.(?:1[6-9]|2\d|3[01])\.\d{1,3}\.\d{1,3})\b")),
    ("personal home path", re.compile(rb"/(?:home|Users)/[a-zA-Z0-9_.-]+/")),
)


def git(*args):
    return subprocess.check_output(["git", *args], cwd=ROOT)


def private_path(name):
    path = Path(name)
    if name == ".env.example":
        return False
    return (path.name == "atlas.toml" or path.name.startswith(".env")
            or path.name.endswith((".local.toml", ".toml.previous"))
            or ".candidate-" in path.name
            or path.suffix in {".sql", ".gz", ".dump", ".log", ".pem", ".key", ".db"}
            or bool(set(path.parts) & PRIVATE_DIRECTORIES)
            or path.parts[0] in {"backups", "dumps", "node_modules", "bin"}
            or name.startswith("web/dist/app/"))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--staged", action="store_true", help="Inspect only the exact index contents about to be committed")
    args = parser.parse_args()
    markers = []
    if LOCAL_MARKERS.exists():
        try:
            values = json.loads(LOCAL_MARKERS.read_text())
            if not isinstance(values, list) or any(not isinstance(v, str) or not v for v in values):
                raise ValueError()
            markers = [value.encode().lower() for value in values]
        except (OSError, ValueError):
            raise SystemExit("Local audit markers are invalid; refusing to publish.")
    staged = {}
    for entry in filter(None, git("ls-files", "--stage", "-z").split(b"\0")):
        meta, name = entry.split(b"\t", 1)
        mode, oid, stage = meta.split()
        if stage != b"0":
            raise SystemExit("Unmerged files found; refusing to publish.")
        staged[name.decode()] = (mode, oid.decode())
    candidates = set(staged)
    if not args.staged:
        candidates.update(filter(None, git("ls-files", "--others", "--exclude-standard", "-z").decode().split("\0")))
    failures = set()
    for name in sorted(candidates):
        if private_path(name):
            failures.add((name, "local-only file"))
            continue
        contents = []
        if name in staged:
            mode, oid = staged[name]
            if mode != b"100644" and mode != b"100755":
                failures.add((name, "non-regular file"))
                continue
            contents.append(git("cat-file", "blob", oid))
        if not args.staged:
            path = ROOT / name
            if path.is_symlink():
                failures.add((name, "symlink"))
            elif path.is_file():
                contents.append(path.read_bytes())
        for data in contents:
            if any(marker in data.lower() for marker in markers):
                failures.add((name, "private local marker"))
            for label, pattern in PATTERNS:
                if pattern.search(data):
                    failures.add((name, label))
    if failures:
        for name, reason in sorted(failures):
            print(f"{name}: {reason}", file=sys.stderr)
        return 1
    print(f"Public-content audit passed ({len(staged)} staged files; local markers {'loaded' if markers else 'not configured'})")
    return 0


if __name__ == "__main__":
    sys.exit(main())
