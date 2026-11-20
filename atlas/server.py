#!/usr/bin/env python3
"""Local HTTP transport for Atlas Schema Desk."""

import argparse
import json
import os
import queue
import re
import subprocess
import sys
import threading
import time
from datetime import datetime, timezone
from functools import partial
from http import HTTPStatus
from http.server import SimpleHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

from .sql_console import classify, confirmation_reasons, split_statements

from .config import load_config
from .db import DbWorker
PROJECT_DIR = Path.cwd()
PUBLIC_DIR = Path(__file__).parent / "public"
SCHEMA_FILE = None
DATA_ENGINES = ()
APPLY_ENGINES = set()
DATA_PAGE_MAX = 500
REFS_CAP = 1000
SQL_REQUEST_TIMEOUT = 150
SQL_MAX_CHARS = 200_000
CSRF_HEADER = "X-DB-Atlas"
LOG_TAIL_CHARS = 4000
VERSION_ID = re.compile(r"[A-Za-z0-9._+/=-]{1,1024}")
CREDENTIALS_IN_URL = re.compile(r"((?:mysql|pgsql|postgres(?:ql)?)://)[^@\s]+@", re.I)
CONFIG = None
OPERATIONS = threading.Condition()
ACTIVE_REQUESTS = 0


def now_iso() -> str:
    return datetime.now(timezone.utc).isoformat(timespec="seconds")


def redact(text: str) -> str:
    return CONFIG.redact(text) if CONFIG else CREDENTIALS_IN_URL.sub(r"\1[redacted]@", text)


class JobRunner:
    """Runs one instance of a command at a time and keeps its last outcome."""

    def __init__(self, name: str, command: list, env: dict | None = None) -> None:
        self.name = name
        self.command = command
        self.env = env or {}
        self._lock = threading.Lock()
        self._state = {
            "state": "idle",  # idle | running | success | error
            "startedAt": None,
            "finishedAt": None,
            "durationSeconds": None,
            "exitCode": None,
            "log": "",
        }

    def status(self) -> dict:
        with self._lock:
            return dict(self._state)

    def is_running(self) -> bool:
        with self._lock:
            return self._state["state"] == "running"

    def start(self, reason: str, command: list | None = None, context: dict | None = None) -> bool:
        with self._lock:
            if self._state["state"] == "running":
                return False
            self._state.update(state="running", startedAt=now_iso(), finishedAt=None,
                               durationSeconds=None, exitCode=None, log="", context=context or {})
        threading.Thread(target=self._run, args=(reason, command or self.command),
                         name=f"db-atlas-{self.name}", daemon=True).start()
        return True

    def _append_log(self, line: str) -> None:
        with self._lock:
            self._state["log"] = (self._state["log"] + line)[-LOG_TAIL_CHARS:]

    def _run(self, reason: str, command: list) -> None:
        started = time.monotonic()
        print(f"[DB Atlas] {self.name} : lancement ({reason}, {now_iso()})", flush=True)
        try:
            # Stream output so long jobs (dump download + restore) show progress while running.
            process = subprocess.Popen(command, cwd=CONFIG.path.parent, stdout=subprocess.PIPE,
                                       stderr=subprocess.STDOUT, stdin=subprocess.DEVNULL,
                                       text=True, errors="replace", env={**os.environ, "PYTHONPATH": str(Path(__file__).parent.parent), **self.env})
            for line in process.stdout:
                self._append_log(redact(line))
            exit_code = process.wait()
            log = self.status()["log"].strip()
        except OSError as error:
            exit_code = -1
            log = f"Impossible de lancer {command[0]} : {error}"
        duration = round(time.monotonic() - started, 1)
        state = "success" if exit_code == 0 else "error"
        with self._lock:
            self._state.update(state=state, finishedAt=now_iso(), durationSeconds=duration,
                               exitCode=exit_code, log=log)
        outcome = "terminé" if state == "success" else f"en échec (code {exit_code})"
        print(f"[DB Atlas] {self.name} : {outcome} en {duration} s", flush=True)
        if state == "error":
            print(log, file=sys.stderr, flush=True)




class Catalog:
    """Exported schema (public/data/schema.json), reloaded when the file changes."""

    def __init__(self) -> None:
        self._mtime = None
        self.tables = {}

    def refresh(self) -> None:
        mtime = SCHEMA_FILE.stat().st_mtime
        if mtime == self._mtime:
            return
        sources = json.loads(SCHEMA_FILE.read_text(encoding="utf-8")).get("sources", {})
        self.tables = {(engine, t["schema"], t["name"]): t for engine, tables in sources.items() for t in tables}
        self._mtime = mtime

    def table(self, engine: str, schema: str, name: str) -> dict:
        self.refresh()
        table = self.tables.get((engine, schema, name))
        if table is None:
            raise LookupError(f"table inconnue du catalogue : {engine} {schema}.{name}")
        return table

    def incoming(self, engine: str, schema: str, name: str) -> list:
        """Foreign keys of other tables that point to this table."""
        self.refresh()
        return [(t, fk) for (e, _, _), t in self.tables.items() if e == engine
                for fk in t.get("foreignKeys", []) if fk["targetSchema"] == schema and fk["targetTable"] == name]


BINARY_TYPE = re.compile(r"blob|binary|bytea", re.I)
NUMERIC_TYPE = re.compile(r"^(tiny|small|medium|big)?int|^integer|^decimal|^numeric|^float|^double|^real|^serial|^bigserial|^smallserial|^bit\b|^boolean|^bool\b|^money", re.I)
FILTER_OPERATOR = re.compile(r"^(<=|>=|!=|<>|<|>|=)\s*(.*)$", re.S)


def parse_filter(column: dict, text: str):
    """UI filter text → worker filter. null / !null, comparison operators, else contains (text) or equals (numbers)."""
    value = text.strip()
    if not value:
        return None
    lowered = value.lower()
    if lowered in ("null", "∅"):
        return {"column": column["name"], "mode": "null"}
    if lowered in ("!null", "not null", "non null"):
        return {"column": column["name"], "mode": "notnull"}
    match = FILTER_OPERATOR.match(value)
    if match:
        operator = "!=" if match.group(1) == "<>" else match.group(1)
        if operator == "=":
            return {"column": column["name"], "mode": "eq", "value": match.group(2)}
        return {"column": column["name"], "mode": "cmp", "op": operator, "value": match.group(2)}
    if NUMERIC_TYPE.search(column["type"] or ""):
        return {"column": column["name"], "mode": "eq", "value": value}
    return {"column": column["name"], "mode": "like", "value": value}


class AtlasHandler(SimpleHTTPRequestHandler):
    def __init__(self, *args, export: JobRunner, s3: JobRunner, apply: JobRunner, worker: DbWorker, catalog: Catalog,
                 allowed_hosts: set, **kwargs):
        self.export = export
        self.s3 = s3
        self.apply = apply
        self.worker = worker
        self.catalog = catalog
        self.allowed_hosts = allowed_hosts
        super().__init__(*args, directory=str(PUBLIC_DIR), **kwargs)

    def translate_path(self, path):
        from urllib.parse import unquote, urlsplit
        clean = unquote(urlsplit(path).path)
        if clean.startswith("/data/"):
            target = (CONFIG.data_dir / clean[6:]).resolve()
            if not target.is_relative_to(CONFIG.data_dir.resolve()):
                return str(CONFIG.data_dir / ".missing")
            return str(target)
        return super().translate_path(path)

    def _host_allowed(self) -> bool:
        # Blocks DNS-rebinding: only accept requests addressed to localhost names.
        return (self.headers.get("Host") or "") in self.allowed_hosts

    def _send_json(self, status: HTTPStatus, payload: dict) -> None:
        body = json.dumps(payload, ensure_ascii=False).encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "application/json; charset=utf-8")
        self.send_header("Content-Length", str(len(body)))
        self.send_header("Cache-Control", "no-store")
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self) -> None:  # noqa: N802 (http.server API)
        if not self._host_allowed():
            self._send_json(HTTPStatus.FORBIDDEN, {"error": "Hôte non autorisé"})
            return
        if self.path.split("?", 1)[0] == "/api/status":
            self._send_json(HTTPStatus.OK, {**self.export.status(), "s3": self.s3.status(), "apply": self.apply.status(),
                                            "transactions": dict(self.worker.transactions),
                                            "transactionsLostAt": self.worker.transactions_lost_at})
            return
        if self.path.split("?", 1)[0] == "/api/config":
            self._send_json(HTTPStatus.OK, CONFIG.public())
            return
        super().do_GET()

    def do_HEAD(self) -> None:  # noqa: N802
        if not self._host_allowed():
            self._send_json(HTTPStatus.FORBIDDEN, {"error": "Hôte non autorisé"})
            return
        super().do_HEAD()

    def do_POST(self) -> None:  # noqa: N802
        if not self._host_allowed():
            self._send_json(HTTPStatus.FORBIDDEN, {"error": "Hôte non autorisé"})
            return
        route = self.path.split("?", 1)[0]
        data_routes = {"/api/data/rows": self._data_rows, "/api/data/refs": self._data_refs, "/api/sql": self._sql,
                       "/api/sql/commit": lambda body: self._sql_end(body, "commit"),
                       "/api/sql/rollback": lambda body: self._sql_end(body, "rollback")}
        runner = {"/api/refresh": self.export, "/api/s3/refresh": self.s3, "/api/apply": self.apply}.get(route)
        if runner is None and route not in data_routes:
            self._send_json(HTTPStatus.NOT_FOUND, {"error": "Route inconnue"})
            return
        if self.headers.get(CSRF_HEADER) != "1":
            self._send_json(HTTPStatus.FORBIDDEN, {"error": f"En-tête {CSRF_HEADER} manquant"})
            return
        if route in data_routes:
            self._data_request(data_routes[route])
            return
        if runner is self.s3 and not CONFIG.dump_sources:
            self._send_json(HTTPStatus.NOT_FOUND, {"error": "Dumps module is disabled"})
            return
        if route == "/api/apply":
            self._start_apply()
            return
        with OPERATIONS:
            if runner is self.export and self.apply.is_running():
                self._send_json(HTTPStatus.CONFLICT, {"error": "A database restore is running"})
                return
            if runner.start("interface"):
                self._send_json(HTTPStatus.ACCEPTED, runner.status())
            else:
                self._send_json(HTTPStatus.CONFLICT, {**runner.status(), "error": "Already running"})

    def _read_json(self, limit: int = 1_048_576) -> dict:
        length = int(self.headers.get("Content-Length") or 0)
        if length < 0 or length > limit:
            raise ValueError(f"corps trop gros ({length} octets)")
        body = json.loads(self.rfile.read(length) or b"{}")
        if not isinstance(body, dict):
            raise ValueError("objet JSON attendu")
        return body

    def _data_request(self, handler) -> None:
        """Common error handling for data routes: bad input 400, worker/database failure 502."""
        try:
            body = self._read_json()
        except (ValueError, json.JSONDecodeError) as error:
            self._send_json(HTTPStatus.BAD_REQUEST, {"error": f"Corps JSON invalide : {error}"})
            return
        global ACTIVE_REQUESTS
        with OPERATIONS:
            if self.apply.is_running():
                self._send_json(HTTPStatus.CONFLICT, {"error": "A database restore is running"})
                return
            ACTIVE_REQUESTS += 1
        try:
            try:
                status, payload = handler(body)
            except (LookupError, ValueError, TypeError) as error:
                status, payload = HTTPStatus.BAD_REQUEST, {"error": CONFIG.redact(error)}
            except (RuntimeError, TimeoutError, OSError) as error:
                status, payload = HTTPStatus.BAD_GATEWAY, {"error": CONFIG.redact(error)}
        finally:
            with OPERATIONS:
                ACTIVE_REQUESTS -= 1
        self._send_json(status, payload)

    def _data_rows(self, body: dict):
        engine, schema, name = body.get("engine"), body.get("schema"), body.get("table")
        table = self.catalog.table(engine, schema, name)
        columns = {column["name"]: column for column in table["columns"]}
        filters = []
        raw_filters = body.get("filters") or {}
        if not isinstance(raw_filters, dict):
            raise ValueError("Filters must be an object")
        for column_name, text in raw_filters.items():
            if column_name not in columns:
                raise LookupError(f"colonne inconnue : {column_name}")
            parsed = parse_filter(columns[column_name], str(text))
            if parsed:
                filters.append(parsed)
        sort = body.get("sort") or None
        if sort:
            if not isinstance(sort, dict):
                raise ValueError("Sort must be an object")
            if sort.get("column") not in columns:
                raise LookupError(f"colonne de tri inconnue : {sort.get('column')}")
            sort = {"column": sort["column"], "dir": "desc" if sort.get("dir") == "desc" else "asc"}
        page_size = max(1, min(DATA_PAGE_MAX, int(body.get("pageSize") or 100)))
        page = max(0, int(body.get("page") or 0))
        response = self.worker.request({
            "op": "rows", "engine": engine, "schema": schema, "table": name, "filters": filters, "sort": sort,
            "columns": [{"name": c["name"], "binary": bool(BINARY_TYPE.search(c["type"] or ""))} for c in table["columns"]],
            "limit": page_size, "offset": page * page_size,
        })
        if not response.get("ok"):
            return HTTPStatus.BAD_GATEWAY, {"error": response.get("error", "erreur inconnue du worker")}
        return HTTPStatus.OK, {key: response[key] for key in ("rows", "hasMore", "durationMs", "sql")} | {"filters": filters}

    def _data_refs(self, body: dict):
        engine, schema, name = body.get("engine"), body.get("schema"), body.get("table")
        self.catalog.table(engine, schema, name)
        row = body.get("row") or {}
        if not isinstance(row, dict):
            raise ValueError("Row must be an object")
        refs = []
        for source, fk in self.catalog.incoming(engine, schema, name):
            value = row.get(fk["targetColumn"])
            if value is None or isinstance(value, dict):
                continue
            refs.append({"schema": source["schema"], "table": source["name"], "column": fk["column"],
                         "targetColumn": fk["targetColumn"], "value": value})
        if not refs:
            return HTTPStatus.OK, {"refs": []}
        response = self.worker.request({"op": "refs", "engine": engine, "refs": refs, "cap": REFS_CAP})
        if not response.get("ok"):
            return HTTPStatus.BAD_GATEWAY, {"error": response.get("error", "erreur inconnue du worker")}
        return HTTPStatus.OK, {"refs": [{**ref, **count} for ref, count in zip(refs, response["counts"])]}

    def _sql(self, body: dict):
        """Free SQL on a local database. Writes stay in an open transaction until commit/rollback."""
        engine = body.get("engine")
        if engine not in DATA_ENGINES:
            raise LookupError(f"base inconnue : {engine}")
        sql = body.get("sql")
        if not isinstance(sql, str) or not sql.strip():
            raise ValueError("requête vide")
        if len(sql) > SQL_MAX_CHARS:
            raise ValueError(f"requête trop longue ({len(sql)} caractères, maximum {SQL_MAX_CHARS})")
        params = body.get("params") or []
        if not isinstance(params, list) or not all(value is None or isinstance(value, (str, int, float)) for value in params):
            raise ValueError("paramètres invalides : liste de valeurs simples attendue")
        statements = [{"sql": statement, "kind": classify(statement, CONFIG.connection(engine).driver)} for statement in split_statements(sql, CONFIG.connection(engine).driver)]
        if not statements:
            raise ValueError("aucune instruction (seulement des commentaires ?)")
        if params and len(statements) > 1:
            raise ValueError("les paramètres ne sont acceptés que pour une seule instruction")
        self.worker.validate_sql(engine, statements)
        control = [s["sql"] for s in statements if s["kind"] == "control"]
        if control:
            raise ValueError(f"« {control[0][:60]} » : les transactions se gèrent avec les boutons Valider / Annuler de la console")
        open_tx = bool(self.worker.transactions.get(engine))
        reasons = [{"index": index, "sql": s["sql"][:200], "reasons": confirmation_reasons(s["sql"], CONFIG.connection(engine).driver, s["kind"], open_tx)}
                   for index, s in enumerate(statements)]
        reasons = [item for item in reasons if item["reasons"]]
        if reasons and not body.get("confirmed"):
            return HTTPStatus.OK, {"needsConfirmation": True, "confirmations": reasons, "statements": len(statements)}
        response = self.worker.request({"op": "sql", "engine": engine, "statements": statements, "params": params},
                                       timeout=SQL_REQUEST_TIMEOUT)
        if not response.get("ok"):
            return HTTPStatus.BAD_GATEWAY, {"error": response.get("error", "erreur inconnue du worker"), "txOpen": self.worker.transactions.get(engine, False)}
        print(f"[DB Atlas] SQL {engine} : {len(statements)} instruction(s) "
              f"({', '.join(s['kind'] for s in statements)}), transaction {'ouverte' if response['txOpen'] else 'fermée'}", flush=True)
        return HTTPStatus.OK, {"results": response["results"], "error": response["error"], "txOpen": response["txOpen"],
                               "implicitCommit": response["implicitCommit"],
                               "statements": [{"kind": s["kind"], "sql": s["sql"][:500]} for s in statements]}

    def _sql_end(self, body: dict, action: str):
        engine = body.get("engine")
        if engine not in DATA_ENGINES:
            raise LookupError(f"base inconnue : {engine}")
        response = self.worker.request({"op": action, "engine": engine})
        if not response.get("ok"):
            return HTTPStatus.BAD_GATEWAY, {"error": response.get("error", "erreur inconnue du worker")}
        print(f"[DB Atlas] SQL {engine} : transaction {'validée' if action == 'commit' else 'annulée'}"
              + ("" if response.get("done") else " (aucune transaction ouverte)"), flush=True)
        return HTTPStatus.OK, {"txOpen": False, "done": response.get("done", False)}

    def _start_apply(self) -> None:
        """Start apply-dump-version.sh for {engine, versionId} sent as JSON."""
        try:
            body = self._read_json(4096)
        except (ValueError, json.JSONDecodeError):
            self._send_json(HTTPStatus.BAD_REQUEST, {"error": "Corps JSON invalide"})
            return
        engine, version_id = body.get("engine"), body.get("versionId")
        if engine not in APPLY_ENGINES or not isinstance(version_id, str) or not VERSION_ID.fullmatch(version_id):
            self._send_json(HTTPStatus.BAD_REQUEST, {"error": "Base ou version invalide"})
            return
        if self.export.is_running():
            self._send_json(HTTPStatus.CONFLICT, {"error": "Un export est en cours, réessaie dans un instant"})
            return
        if any(self.worker.transactions.values()):
            self._send_json(HTTPStatus.CONFLICT, {"error": "Une transaction SQL est ouverte sur cette base : valide-la ou annule-la avant de restaurer"})
            return
        command = [sys.executable, "-m", "atlas.dumps", "--config", str(CONFIG.path), "apply", engine, version_id]
        with OPERATIONS:
            if ACTIVE_REQUESTS or any(self.worker.transactions.values()) or self.export.is_running():
                self._send_json(HTTPStatus.CONFLICT, {"error": "Finish active requests and transactions before restoring"})
                return
            self.worker.close()
            if self.apply.start("interface", command, {"engine": engine, "versionId": version_id}):
                self._send_json(HTTPStatus.ACCEPTED, self.apply.status())
            else:
                self._send_json(HTTPStatus.CONFLICT, {**self.apply.status(), "error": "A restore is already running"})

    def log_message(self, format: str, *args) -> None:  # noqa: A002
        # Keep the log readable: skip the frequent status polling lines.
        # args[0] is the request line for access logs but an int (status code) for errors.
        if "/api/status" not in (str(args[0]) if args else ""):
            super().log_message(format, *args)



def main() -> int:
    global CONFIG, SCHEMA_FILE, DATA_ENGINES, APPLY_ENGINES
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--config", default="atlas.toml")
    parser.add_argument("--port", type=int)
    parser.add_argument("--bind")
    args = parser.parse_args()
    try:
        CONFIG = load_config(args.config)
    except (OSError, ValueError) as error:
        parser.error(str(error))
    CONFIG.data_dir.mkdir(parents=True, exist_ok=True)
    SCHEMA_FILE = CONFIG.data_dir / "schema.json"
    DATA_ENGINES = tuple(c.id for c in CONFIG.connections)
    APPLY_ENGINES = {s["connection"] for s in CONFIG.dump_sources}
    command = [sys.executable, "-m", "atlas.catalog", "--config", str(CONFIG.path)]
    export = JobRunner("export", command)
    s3 = JobRunner("dumps", [sys.executable, "-m", "atlas.dumps", "--config", str(CONFIG.path), "list"])
    apply = JobRunner("restore", [])
    worker = DbWorker(CONFIG)
    port = args.port or CONFIG.port
    bind = args.bind or CONFIG.bind
    if bind not in ("127.0.0.1", "localhost", "0.0.0.0"):
        parser.error("Unsupported bind address")
    allowed_hosts = {f"{name}:{port}" for name in ("localhost", "127.0.0.1", "[::1]")}
    handler = partial(AtlasHandler, export=export, s3=s3, apply=apply, worker=worker,
                      catalog=Catalog(), allowed_hosts=allowed_hosts)
    server = ThreadingHTTPServer((bind, port), handler)
    export.start("startup")
    if CONFIG.dump_sources:
        s3.start("startup")
    print(f"Atlas Schema Desk: http://localhost:{port}", flush=True)
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        pass
    finally:
        server.server_close()
        worker.close()
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
