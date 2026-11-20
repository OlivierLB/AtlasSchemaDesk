"""Load local configuration without exposing database credentials to the UI."""
import os
import re
import tomllib
from dataclasses import dataclass, field
from pathlib import Path


@dataclass(frozen=True)
class Connection:
    id: str
    label: str
    driver: str
    host: str
    port: int
    database: str
    user: str
    password: str = field(repr=False)
    default_schema: str = "public"
    color: str = "#8ab4f8"
    allow_writes: bool = False


@dataclass
class Config:
    path: Path
    connections: list
    bind: str
    port: int
    data_dir: Path
    routines_repository: dict
    dump_sources: list

    def connection(self, name):
        for conn in self.connections:
            if conn.id == name:
                return conn
        raise ValueError(f"Unknown connection: {name}")

    def public(self):
        return {"connections": [{k: getattr(c, k) for k in
                ("id", "label", "driver", "default_schema", "color", "allow_writes")}
                for c in self.connections], "modules": {
                    "dumps": bool(self.dump_sources), "routines": bool(self.routines_repository)}}

    def redact(self, message):
        text = str(message)
        for conn in self.connections:
            if conn.password:
                text = text.replace(conn.password, "[redacted]")
        text = re.sub(r"((?:mysql|postgres(?:ql)?|pgsql)://)[^@\s]+@", r"\1[redacted]@", text, flags=re.I)
        return text


def load_config(path="atlas.toml", environ=None):
    path = Path(path).expanduser().resolve()
    env = dict(os.environ if environ is None else environ)
    dotenv = path.parent / ".env"
    if dotenv.is_file():
        for index, line in enumerate(dotenv.read_text().splitlines(), 1):
            line = line.strip()
            if not line or line.startswith("#"):
                continue
            match = re.fullmatch(r"(?:export\s+)?([A-Za-z_]\w*)\s*=\s*(.*)", line)
            if not match:
                raise ValueError(f"Invalid .env assignment at line {index}")
            key, value = match.groups()
            if value.startswith(("'", '"')):
                if len(value) < 2 or value[-1] != value[0]:
                    raise ValueError(f"Unclosed .env quote at line {index}")
                value = value[1:-1]
            env.setdefault(key, value)
    with path.open("rb") as handle:
        raw = tomllib.load(handle)
    server = raw.get("server", {})
    bind = server.get("bind", "127.0.0.1")
    if bind not in ("127.0.0.1", "localhost", "0.0.0.0"):
        raise ValueError("Server bind must be localhost, 127.0.0.1 or 0.0.0.0 (Docker)")
    port = server.get("port", 8765)
    if type(port) is not int or not 1 <= port <= 65535:
        raise ValueError("Invalid server port")
    allowed = server.get("allowed_db_hosts", ["localhost", "127.0.0.1"])
    if not isinstance(allowed, list) or not all(isinstance(x,str) for x in allowed):
        raise ValueError("allowed_db_hosts must be a list of host names")
    connections, ids = [], set()
    for item in raw.get("connections", []):
        name = item.get("id", "")
        if not re.fullmatch(r"[a-z][a-z0-9_-]{0,63}", name) or name in ids or name in {"sql", "events", "data", "api"}:
            raise ValueError(f"Invalid, reserved or duplicate connection id: {name}")
        ids.add(name)
        driver = item.get("driver")
        if driver not in ("postgresql", "mysql"):
            raise ValueError(f"Unsupported driver for {name}")
        if item.get("host") not in allowed:
            raise ValueError(f"Database host for {name} is not in allowed_db_hosts")
        if "password" in item:
            raise ValueError("Use password_env instead of a password in TOML")
        variable = item.get("password_env", "")
        if not re.fullmatch(r"[A-Za-z_]\w*", variable) or variable not in env:
            raise ValueError(f"Missing password environment variable for {name}: {variable}")
        for key in ("host", "database", "user"):
            if not isinstance(item.get(key),str) or not item[key]:
                raise ValueError(f"Missing {key} for {name}")
        dbport = item.get("port", 5432 if driver == "postgresql" else 3306)
        if type(dbport) is not int or not 1 <= dbport <= 65535:
            raise ValueError(f"Invalid database port for {name}")
        writes = item.get("allow_writes", False)
        if type(writes) is not bool:
            raise ValueError(f"allow_writes must be a boolean for {name}")
        color = item.get("color", "#8ab4f8")
        for key in ('label', 'default_schema'):
            if key in item and (not isinstance(item[key],str) or not item[key]):
                raise ValueError(f"{key} must be a nonempty string for {name}")
        if not re.fullmatch(r"#[0-9a-fA-F]{6}",color):
            raise ValueError(f"Invalid color for {name}")
        connections.append(Connection(name, item.get("label", name), driver, item["host"], dbport,
            item["database"], item["user"], env[variable], item.get("default_schema", item["database"] if driver == "mysql" else "public"), color, writes))
    if not connections:
        raise ValueError("Configure at least one connection")
    routines = raw.get("routines_repository", {})
    if routines:
        routines = dict(routines)
        routines["path"] = str((path.parent / routines["path"]).resolve())
    dumps = raw.get("dump_sources", [])
    seen = set()
    for source in dumps:
        if source.get("connection") not in ids or source["connection"] in seen:
            raise ValueError("Dump sources need a unique configured connection")
        seen.add(source["connection"])
        if source.get("type") not in ("directory", "s3-versioned"):
            raise ValueError("Unsupported dump source type")
        if source["type"] == "directory":
            source["path"] = str((path.parent / source["path"]).resolve())
        else:
            if not source.get("bucket") or not source.get("key"):
                raise ValueError("S3 dump source requires bucket and key")
        if source.get("local_path"):
            source["local_path"] = str((path.parent / source["local_path"]).resolve())
    return Config(path, connections, bind, port, (path.parent / server.get("data_dir", ".local/data")).resolve(), routines, dumps)
