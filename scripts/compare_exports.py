"""Compare two catalog export directories (e.g. Python vs Go backends).

Usage: python3 scripts/compare_exports.py DIR_A DIR_B

Rerunnable and read-only. Compares schema.json, programs.json and the snapshot
fingerprints, ignoring generation timestamps. Exit code 0 when equivalent.
"""

import json
import sys
from pathlib import Path


def load(path):
    return json.loads(path.read_text(encoding="utf-8"))


def strip_times(value):
    if isinstance(value, dict):
        return {k: strip_times(v) for k, v in value.items() if k not in ("generatedAt", "checkedAt")}
    if isinstance(value, list):
        return [strip_times(v) for v in value]
    return value


def first_difference(a, b, where="$"):
    if type(a) is not type(b):
        return f"{where}: type {type(a).__name__} != {type(b).__name__}"
    if isinstance(a, dict):
        for key in sorted(set(a) | set(b)):
            if key not in a or key not in b:
                return f"{where}.{key}: present only in {'B' if key not in a else 'A'}"
            found = first_difference(a[key], b[key], f"{where}.{key}")
            if found:
                return found
        if list(a) != list(b):
            return None  # same content, different key order (not significant)
        return None
    if isinstance(a, list):
        if len(a) != len(b):
            return f"{where}: length {len(a)} != {len(b)}"
        for index, (x, y) in enumerate(zip(a, b)):
            found = first_difference(x, y, f"{where}[{index}]")
            if found:
                return found
        return None
    return None if a == b else f"{where}: {a!r} != {b!r}"


def main():
    if len(sys.argv) != 3:
        raise SystemExit(__doc__)
    left, right = Path(sys.argv[1]), Path(sys.argv[2])
    failures = 0
    for name in ("schema.json", "programs.json"):
        a, b = strip_times(load(left / name)), strip_times(load(right / name))
        difference = first_difference(a, b)
        print(f"{name}: {'identique' if not difference else 'DIFFÉRENT — ' + difference}")
        failures += bool(difference)
    hashes = []
    for root in (left, right):
        index = load(root / "snapshots/index.json")
        hashes.append({name: item["hash"] for name, item in index["versions"][-1]["sources"].items()})
    same = hashes[0] == hashes[1]
    print(f"empreintes de schéma: {'identiques' if same else 'DIFFÉRENTES'} {hashes[0]}")
    failures += not same
    programs = load(left / "programs.json")["engines"]
    tables = load(left / "schema.json")["sources"]
    for name, engine in programs.items():
        print(f"  {name}: {len(tables[name])} tables, {len(engine['programs'])} programmes")
    raise SystemExit(1 if failures else 0)


if __name__ == "__main__":
    main()
