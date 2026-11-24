"""Check that the release binary is self-contained: interface embedded, no
checkout, Node or Python needed at runtime.

Usage: python3 scripts/check_distribution.py [bin/sulcata]
Rerunnable; starts the binary on a free localhost port with a synthetic
configuration (no database is contacted by these checks).
"""

import json
import re
import socket
import subprocess
import sys
import tempfile
import time
import urllib.request
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]


def call(base, path, body=None, session=None):
    headers = {"X-Sulcata-Session": session} if session else {}
    if body is not None:
        headers |= {"Content-Type": "application/json", "X-Sulcata": "1"}
    request = urllib.request.Request(base + path, data=json.dumps(body).encode() if body is not None else None, headers=headers)
    with urllib.request.urlopen(request, timeout=5) as response:
        return response.status, response.read()


def main():
    binary = Path(sys.argv[1] if len(sys.argv) > 1 else ROOT / "bin/sulcata").resolve()
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        port = sock.getsockname()[1]
    with tempfile.TemporaryDirectory(prefix="sulcata-dist-") as temporary:
        root = Path(temporary)
        (root / "sulcata.toml").write_text(f"""[server]
port = {port}
[[connections]]
id = "sample"
driver = "postgresql"
host = "127.0.0.1"
port = 9
database = "sample"
user = "reader"
password_env = "SULCATA_FIXTURE_PASSWORD"
""")
        env = {"SULCATA_FIXTURE_PASSWORD": "fixture-only", "PATH": ""}
        process = subprocess.Popen([str(binary), "--config", "sulcata.toml"], cwd=root, env=env,
                                   stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
        base = f"http://127.0.0.1:{port}"
        try:
            for _ in range(100):
                try:
                    status, index = call(base, "/")
                    break
                except OSError:
                    time.sleep(0.05)
            else:
                raise SystemExit("binary did not start")
            assert status == 200, status
            assets = re.findall(r'(?:src|href)="(/assets/[^"]+)"', index.decode())
            assert assets, "no embedded assets referenced by index.html"
            for asset in assets:
                assert call(base, asset)[0] == 200, asset
            config = json.loads(call(base, "/api/config")[1])
            assert config["connections"][0]["id"] == "sample" and "fixture-only" not in json.dumps(config)
            status, body = call(base, "/api/sessions", {})
            assert status == 201, body
            session = json.loads(body)["id"]
            assert call(base, "/api/sessions/current", session=session)[0] == 200
        finally:
            process.terminate()
            output = process.communicate(timeout=30)[0].decode()
        assert "fixture-only" not in output, "password printed in server output"
        assert process.returncode == 0, f"unclean shutdown ({process.returncode}): {output[-2000:]}"
    print(f"Self-contained binary passed ({binary.stat().st_size / 1e6:.1f} MB): embedded interface, empty PATH, clean SIGTERM shutdown")


if __name__ == "__main__":
    main()
