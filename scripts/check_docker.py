"""Verify the shipped image end-to-end with an isolated, disposable PostgreSQL."""

import json
import os
import re
import subprocess
import tempfile
import time
import urllib.error
import urllib.request
import uuid
from pathlib import Path

from playwright.sync_api import sync_playwright


def docker(*args, check=True):
    return subprocess.run(
        ["docker", *args], check=check, capture_output=True, text=True
    )


def main():
    suffix = uuid.uuid4().hex[:10]
    network, database, application = (
        f"sulcata-smoke-{suffix}-{kind}" for kind in ("net", "pg", "app")
    )
    base = "http://127.0.0.1:18766"
    with tempfile.TemporaryDirectory(prefix="sulcata-image-") as temporary:
        root = Path(temporary)
        data = root / "local"
        data.mkdir()
        config = root / "sulcata.toml"
        config.write_text("""[server]
port = 18766
allowed_db_hosts = ["db"]
[[connections]]
id = "sample"
label = "Sample"
driver = "postgresql"
host = "db"
port = 5432
database = "sample"
user = "postgres"
password_env = "SULCATA_SMOKE_PASSWORD"
""")
        try:
            docker("network", "create", network)
            docker(
                "run",
                "-d",
                "--name",
                database,
                "--network",
                network,
                "--network-alias",
                "db",
                "--tmpfs",
                "/var/lib/postgresql",
                "-e",
                "POSTGRES_PASSWORD=public-test-only",
                "-e",
                "POSTGRES_DB=sample",
                "postgres:18",
            )
            for _ in range(120):
                if (
                    docker(
                        "exec",
                        database,
                        "pg_isready",
                        "-h",
                        "127.0.0.1",
                        "-U",
                        "postgres",
                        "-d",
                        "sample",
                        check=False,
                    ).returncode
                    == 0
                ):
                    break
                time.sleep(0.5)
            else:
                raise RuntimeError("Disposable image-test database did not start")
            docker(
                "exec",
                database,
                "psql",
                "-U",
                "postgres",
                "-d",
                "sample",
                "-v",
                "ON_ERROR_STOP=1",
                "-c",
                "CREATE TABLE sulcata_smoke_probe (id integer PRIMARY KEY, value text); INSERT INTO sulcata_smoke_probe VALUES (1,'docker-ok');",
            )
            docker(
                "run",
                "-d",
                "--name",
                application,
                "--network",
                network,
                "--read-only",
                "--tmpfs",
                "/tmp",
                "--user",
                f"{os.getuid()}:{os.getgid()}",
                "-p",
                "127.0.0.1:18766:18766",
                "-v",
                f"{config}:/workspace/sulcata.toml:ro",
                "-v",
                f"{data}:/workspace/.local",
                "-e",
                "SULCATA_SMOKE_PASSWORD=public-test-only",
                "sulcata:test",
                "--port",
                "18766",
            )
            for _ in range(120):
                try:
                    with urllib.request.urlopen(
                        base + "/api/status", timeout=2
                    ) as response:
                        status = json.load(response)
                    if status["state"] == "error":
                        raise RuntimeError(status["log"])
                    if status["state"] == "success":
                        break
                except (urllib.error.URLError, ConnectionError, TimeoutError):
                    pass
                time.sleep(0.5)
            else:
                raise RuntimeError("Application image did not become ready")
            # The image ships one static binary: no Node or Python runtime.
            docker("exec", application, "sh", "-c", "! command -v node && ! command -v python3")
            with urllib.request.urlopen(base, timeout=5) as response:
                index = response.read().decode()
            for asset in re.findall(r'(?:src|href)="(/assets/[^\"]+)"', index):
                with urllib.request.urlopen(base + asset, timeout=5) as response:
                    assert response.status == 200
            with sync_playwright() as playwright:
                browser = playwright.firefox.launch(headless=True)
                page = browser.new_page()
                errors = []
                page.on("pageerror", lambda error: errors.append(str(error)))
                page.goto(base + "/#/sample/public/sulcata_smoke_probe/donnees")
                page.wait_for_selector('td:text-is("docker-ok")')
                assert not errors, errors
                browser.close()
            print(
                "Docker image passed: browser → Go binary → PostgreSQL → grid; no Node/Python; read-only filesystem"
            )
        except Exception as error:
            if isinstance(error, subprocess.CalledProcessError):
                print(error.stderr, flush=True)
            print(docker('logs', application, check=False).stdout[-4000:], flush=True)
            raise
        finally:
            for name in (application, database):
                docker("rm", "-f", name, check=False)
            docker("network", "rm", network, check=False)


if __name__ == "__main__":
    main()
