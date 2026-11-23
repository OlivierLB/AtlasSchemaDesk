"""Measure a backend: time to ready, resident memory and API latency.

Usage: python3 scripts/bench_backend.py PORT -- SERVER COMMAND...
Example:
  python3 scripts/bench_backend.py 18770 -- bin/atlas-schema-desk --config .local/test.toml --port 18770

Rerunnable; only issues read-only requests (SELECT, data grid) on the
configured connections. Requires the server's first connection to be readable.
"""

import json
import statistics
import subprocess
import sys
import time
import urllib.request
from pathlib import Path


def call(base, path, body=None, session=None):
    headers = {"X-Atlas-Session": session} if session else {}
    if body is not None:
        headers |= {"Content-Type": "application/json", "X-DB-Atlas": "1"}
    request = urllib.request.Request(base + path, data=json.dumps(body).encode() if body is not None else None, headers=headers)
    with urllib.request.urlopen(request, timeout=30) as response:
        return json.load(response)


def rss_kib(pid):
    for line in Path(f"/proc/{pid}/status").read_text().splitlines():
        if line.startswith("VmRSS:"):
            return int(line.split()[1])
    return 0


def latency(fn, count=200):
    samples = []
    for _ in range(count):
        started = time.perf_counter()
        fn()
        samples.append((time.perf_counter() - started) * 1000)
    samples.sort()
    return statistics.median(samples), samples[int(len(samples) * 0.95)]


def main():
    if len(sys.argv) < 4 or sys.argv[2] != "--":
        raise SystemExit(__doc__)
    base = f"http://127.0.0.1:{int(sys.argv[1])}"
    started = time.perf_counter()
    process = subprocess.Popen(sys.argv[3:], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    try:
        while True:
            if process.poll() is not None:
                raise SystemExit("server exited")
            try:
                if call(base, "/api/status")["state"] == "success":
                    break
            except OSError:
                pass
            time.sleep(0.02)
        ready = time.perf_counter() - started
        engine = call(base, "/api/config")["connections"][0]["id"]
        schema = json.loads(urllib.request.urlopen(base + "/data/schema.json").read())["sources"][engine][0]
        session = call(base, "/api/sessions", {})["id"]
        sql = latency(lambda: call(base, "/api/sql", {"engine": engine, "sql": "SELECT 1"}, session))
        grid = latency(lambda: call(base, "/api/data/rows", {"engine": engine, "schema": schema["schema"], "table": schema["name"]}, session))
        print(f"prêt (export du catalogue inclus) : {ready:.2f} s")
        print(f"mémoire résidente du serveur : {rss_kib(process.pid) / 1024:.1f} Mo")
        print(f"POST /api/sql SELECT 1 : médiane {sql[0]:.2f} ms, p95 {sql[1]:.2f} ms")
        print(f"POST /api/data/rows (100 lignes) : médiane {grid[0]:.2f} ms, p95 {grid[1]:.2f} ms")
    finally:
        process.terminate()
        process.wait(timeout=30)


if __name__ == "__main__":
    main()
