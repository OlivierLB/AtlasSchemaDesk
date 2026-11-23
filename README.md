# Atlas Schema Desk

[English](README.md) · [Français](README.fr.md)

A local explorer for PostgreSQL and MySQL databases. Browse schemas, tables and foreign keys, inspect stored programs and their impact on tables, read and filter table data, and run SQL in a console with explicit commit and rollback. Optional modules compare routines with a local SQL repository and list or restore database backups.

Atlas Schema Desk is a single Go binary with its React interface embedded. It runs entirely on your machine, without a cloud account or telemetry. PostgreSQL 10+ and MySQL 8 are tested (CI uses PostgreSQL 10, PostgreSQL 18 and MySQL 8.4).

## Features

- **Overview** — every configured database with its color, recently opened tables and catalog status.
- **Navigation tree** — databases, schemas and tables with search, across any number of sources.
- **Table views** — *Structure* (columns, keys, indexes), *Relations* (incoming and outgoing foreign keys), *Data* (paginated grid with filters, sort and per-cell editing on writable connections), *SQL programs* (triggers and routines touching the table), *Relation map* and *SQL query*.
- **Stored programs** — functions, procedures, triggers and events with their source code and a static impact analysis: tables read, written, written indirectly through calls, probable reads/writes through dynamic SQL, called programs and callers.
- **Events** — a dedicated page listing scheduled events (MySQL) with filters by source and status.
- **SQL console** — CodeMirror editor with schema-aware completion, formatting (sql-formatter), query history kept in the browser, read-only by default, pending transactions committed or rolled back explicitly.
- **Schema history** — each export fingerprints the catalog; a new version is recorded only when a source changes.
- **Connections and backups** — add, edit and delete connections and backup sources from the interface, then apply the configuration with an in-place restart.
- **Maintenance jobs** — catalog export, backup listing and restoration run as cancellable jobs with bounded, redacted live logs.

The interface labels are currently in French.

## Requirements

| Use | Needs |
| --- | --- |
| Run a built binary | nothing else (drivers and interface are compiled in) |
| Run the Docker image | Docker |
| Build from source | Go 1.27+ and Node 22.12+ |
| Browser and integration tests | Python 3, Playwright with Firefox, Docker Compose |
| S3 backup sources | AWS CLI, installed and configured separately |

## Quick start

```sh
git clone https://github.com/OlivierLB/AtlasSchemaDesk.git
cd AtlasSchemaDesk
make frontend build          # npm ci + Vite build, then go build into bin/
cp atlas.example.toml atlas.toml
cp .env.example .env
```

Edit `atlas.toml` for an existing database and set its password in `.env` or in your shell environment. Start with a database account that has only the privileges you need, preferably SELECT and catalog access.

```sh
bin/atlas-schema-desk --config atlas.toml
# Open http://localhost:8765
```

On startup the server exports the catalogs of every connection; click **Actualiser** to refresh them later. If an export fails, the previous complete catalog is kept and the error appears in the status banner.

## Command line

```text
atlas-schema-desk [--config atlas.toml] [--port N] [--bind ADDRESS]   start the server (default command)
atlas-schema-desk check   --config atlas.toml                         test every connection, read-only
atlas-schema-desk inspect --config atlas.toml                         print a credential-free configuration summary (JSON)
atlas-schema-desk export  --config atlas.toml                         export catalogs without starting the server
atlas-schema-desk dumps   --config atlas.toml list                    list backups of the configured sources
atlas-schema-desk dumps   --config atlas.toml apply CONNECTION VERSION   restore one backup version
atlas-schema-desk version                                             print the version
```

`--port` and `--bind` override `[server]` values. The bind address must be `127.0.0.1`, `localhost` or `0.0.0.0` (containers only).

## Configuration

The configuration is a TOML file (`atlas.toml` by default). A sibling `.env` file is read automatically; relative paths resolve from the configuration file's directory.

### Server

```toml
[server]
bind = "127.0.0.1"
port = 8765
allowed_db_hosts = ["127.0.0.1", "localhost"]
data_dir = ".local/data"            # generated catalogs and history
session_idle_seconds = 1800
export_timeout_seconds = 300
dump_list_timeout_seconds = 120
restore_timeout_seconds = 3600
refresh_dumps_on_start = true
```

All keys are optional; the values above are the defaults. Database hosts must be listed in `allowed_db_hosts`. Timeouts must be positive integers.

### Connections

Add any number of `[[connections]]` sections:

```toml
[[connections]]
id = "library"
label = "Library"
driver = "postgresql"               # or "mysql"
host = "127.0.0.1"
port = 5432                         # default 5432 (PostgreSQL) or 3306 (MySQL)
database = "pagila"
user = "reader"
password_env = "LIBRARY_DB_PASSWORD"
default_schema = "public"           # defaults to "public", or the database name for MySQL
color = "#8ab4f8"
allow_writes = false
```

- `id` is lowercase (`[a-z][a-z0-9_-]`, up to 64 characters), unique, and independent of the driver: two PostgreSQL sources can have different names and colors. `sql`, `events`, `data` and `api` are reserved.
- Passwords live in environment variables, never in TOML: a `password` key is rejected. The `.env` parser accepts `KEY=value`, an optional `export` prefix, quoted values and full-line comments; it performs no interpolation, shell evaluation or escape expansion. Existing environment variables take precedence.
- Each browser tab owns its own persistent database connections, serialized per source. Reloading a tab keeps its session; duplicating a tab creates a new one.

### Local files and Git

`atlas.toml`, `.env`, `private/`, `local/`, `.local/`, dumps, logs, test artifacts and generated catalogs are ignored by Git. Keep every organization-specific setting there; `atlas.example.toml` and `.env.example` contain only public examples. Never force-add local files: `scripts/check_public.py` rejects tracked local configuration and generated data.

### Editing connections in the interface

**Connexions et sauvegardes** (`#/connexions`) groups each database with its optional backup source. Add, edit or delete a connection or backup source from its card.

- Changes are validated with the same loader as startup, then written to the configuration file. The previous version is kept as `*.previous`; TOML comments are not preserved.
- A password typed in the form is written to the sibling `.env` file (mode 600) and is never sent back to the browser. Leave it empty to keep the current one.
- Connection IDs are immutable because catalogs and history are keyed by them. Deleting a connection also deletes its backup source. A connection owns at most one backup source.
- `allowed_db_hosts`, restore commands and local dump paths are displayed but can only be changed in the file: a web request must never choose a host outside the allowlist, a shell command or a file to overwrite.
- **Appliquer et redémarrer** re-executes the server in place (same process ID, so launchers keep tracking it) once no transaction, request or maintenance job is running.

The settings API exposes the relationship as `connections[].backup` (or `null`); the flat `dumpSources` list remains for compatibility.

## SQL, data and transactions

Read-only is the default. It is enforced in the API and set on the database session; session changes that could bypass it are rejected. Use a database account with genuinely restricted privileges as an additional boundary: the SQL classifier is conservative guidance, not a full SQL parser.

Set `allow_writes = true` on a connection to allow changes. A write opens a transaction that stays pending until **Valider** (commit) or **Annuler** (rollback). Stopping the server, session expiry or closing the session rolls pending transactions back.

- PostgreSQL statements and grid reads use savepoints to recover from errors inside a transaction.
- A MySQL DDL statement commits the current transaction implicitly, even when it fails; the interface reports the resulting state. Some MySQL storage engines are not transactional. Routine calls can have effects that static analysis cannot predict.
- Data grid edits generate a parameterized `UPDATE … WHERE <primary key> = ?` and require a primary key.

Limits:

| Item | Limit |
| --- | --- |
| Console result rows | 500 |
| Data grid page size | 100 by default, up to 500 |
| Text cell | first 2,000 characters; binary cells show their byte length |
| PostgreSQL statement timeout | 15 s for the grid, 120 s for the console |
| MySQL | `max_execution_time` on eligible SELECT statements, plus 125 s socket read/write timeouts |

Values are shown as the database prints them (JSON as JSON, PostgreSQL arrays as `{…}`, booleans as `true`/`false`).

### Sessions

A browser session expires after `session_idle_seconds` (30 minutes by default) without a database operation or explicit renewal; status polling does not keep it alive. The interface warns five minutes before expiry. Expiry and server shutdown close connections and roll back pending transactions.

Reload recovery uses the browser's Web Locks API; browsers without it get a fresh isolated session and the previous one expires normally. A disconnected browser neither cancels nor retries an SQL operation: if its outcome is uncertain, inspect the session and the data before continuing.

## Event window investigation

On **Événements**, select a date and time range (or a whole day), then choose **Analyser le créneau**. The projection uses the cached catalog, not a live database query. Inputs and results use the browser's time zone; each event is calculated in its own exported time zone. A finish time before the start crosses midnight.

Results aggregate scheduled starts, show the latest start within an optional lookback period, and identify tables touched by several candidate events. Click a timeline bar to narrow the window. These are investigation leads, not measured durations, actual concurrency, historical execution records or proof of a slowdown. Event and scheduler states are those observed at the latest export, including for past dates.

Refresh the catalog once after upgrading to export structured schedules. Events with missing metadata, unresolved time zones, unsupported compound intervals or a daylight-saving transition on an analyzed date are listed explicitly as non-calculable. Disabled events and disabled schedulers are listed separately. The API caps windows at seven days and lookback at 24 hours, aggregating high-frequency schedules rather than enumerating every execution. See the [MySQL event metadata reference](https://dev.mysql.com/doc/refman/8.4/en/information-schema-events-table.html) for the source fields.

After building the frontend, run `python tests/ui_event_window.py` in the test environment to check the window UI against synthetic fixtures.

## Optional modules

### Routine source repository

Compare stored programs with SQL files in a local repository:

```toml
[routines_repository]
path = "../my-routines"
pattern = "{schema}/{kind}s/{name}.sql"
connections = ["library"]           # connection IDs this repository applies to
scan_definitions = true             # find CREATE statements in shared SQL files
```

UTF-8 and Latin-1 files are supported. The comparison ignores whitespace, comments and quoting differences.

Impact analysis derives reads, writes, calls and indirect writes from the SQL text; references built with dynamic SQL are marked *probable*. Row counts are catalog estimates; a non-empty PostgreSQL table that was never analyzed shows an unknown count.

### Backups

Directory source:

```toml
[[dump_sources]]
connection = "library"
type = "directory"
path = "./backups"
pattern = "*.sql.gz"
local_path = ".local/dumps/library.sql.gz"
restore_command = 'gzip -dc "$ATLAS_DUMP_PATH" | psql --dbname=pagila'
```

S3 source: use `type = "s3-versioned"` with `bucket` and `key` instead of `path` and `pattern`. Only versions of that exact key are offered, and AWS CLI pagination is followed. Install and configure the AWS CLI separately.

Set `[server].refresh_dumps_on_start = false` to load the local backup cache at startup and list remote backups only when you refresh them in the interface. This setting never triggers a restore. Nothing is downloaded or restored until requested. The backup and routine-source features stay hidden when their modules are not configured.

**Restoration replaces the database contents.** `restore_command` is a trusted shell command from your local configuration and runs with your user privileges. The temporary download path is passed in `ATLAS_DUMP_PATH` and the connection ID in `ATLAS_CONNECTION`. The command must read that file and exit non-zero on failure. No command supplied by a request is ever executed.

Before restoring, free disk space and size are checked; the previous local dump is kept until the restore succeeds; open transactions or active requests block the restore. A failed or cancelled command can still leave a partially restored database: this is not a backup system nor an atomic restore. Process-tree cancellation is supported on Linux and in the Docker image.

## Docker

```sh
docker build -t atlas-schema-desk .
mkdir -p .local
export ATLAS_UID=$(id -u) ATLAS_GID=$(id -g)
docker compose up
```

The image contains only the binary and a shell (for trusted `restore_command` entries); no Node or Python. The Compose service runs read-only, mounts `atlas.toml` (read-only) and `.env`, stores generated data in `.local/`, and publishes only `127.0.0.1:8765`.

Inside a container, `127.0.0.1` is the container itself. On Linux, use `host.docker.internal` (mapped to the host gateway in the example) and add it to `allowed_db_hosts`. Attach other Docker networks explicitly if needed.

## Security model

- The default bind is localhost. `0.0.0.0` exists for containers; publish the port on localhost only.
- The server validates the HTTP `Host` header and requires `X-DB-Atlas: 1` on POST requests to limit DNS rebinding and cross-origin requests.
- There is **no authentication and no TLS**: never expose it to the internet or an untrusted network.
- Only the embedded interface and generated catalog files are served. Hosts, users and passwords never reach the browser through the public configuration, and error messages and job logs are redacted.

## Development

The backend uses Go's standard HTTP library, `pgx/pgconn` and `go-mysql`. The frontend uses React 19, strict TypeScript, TanStack Query, CodeMirror and Vite.

```sh
go run ./cmd/atlas-schema-desk --config atlas.toml   # terminal 1
npm run dev --prefix frontend                        # terminal 2, Vite proxies the API to Go
```

- Build the frontend before `go build`: the binary embeds `web/dist/app`.
- `frontend/openapi.json` is the HTTP contract. Change it together with the Go handlers, run `npm run api:generate --prefix frontend` and commit both. Go tests fail when a contract route is not served or a response field is undeclared.

### Tests

```sh
make frontend
make test                                  # go vet, gofmt, go test -race
npm run typecheck --prefix frontend
npm run lint --prefix frontend
npm test --prefix frontend
pip install -r tests/requirements.txt      # browser test tooling only
python -m playwright install firefox
bash tests/run.sh                          # or: make integration
python scripts/check_distribution.py bin/atlas-schema-desk
docker build -t atlas-schema-desk:test .
python scripts/check_docker.py
```

`make check` runs `make test`, the build, the public-content check and the distribution check.

`tests/run.sh` starts a separate `atlas-schema-desk-test` Compose project with PostgreSQL 18, PostgreSQL 10 and MySQL 8.4 on localhost ports 55432, 55433 and 53306, backed by disposable tmpfs storage. Pagila 3.0 and a public Sakila mirror are downloaded at pinned commits with SHA-256 verification into ignored storage. The tests add small probe tables, triggers, a procedure and an event, and cover transactions, savepoints, database-level read-only mode, MySQL implicit commits, configuration, optional backups, HTTP protections and Firefox UI behavior with six source IDs. The script stops only that test project on exit.

The sample databases keep their own licenses: [Pagila](https://github.com/devrimgunduz/pagila) and [Sakila (BSD)](https://dev.mysql.com/doc/sakila/en/sakila-license.html). They are downloaded for tests and not shipped.

Before committing, run `python scripts/check_public.py`. CI runs it along with the frontend checks, Go checks, integration and browser tests, the self-contained binary check and the Docker image check. `scripts/bench_backend.py` measures startup, memory and API latency of a running build. See [CONTRIBUTING.md](CONTRIBUTING.md).

## License

MIT. Third-party notices for the bundled interface (React, CodeMirror, sql-formatter…) are generated into `THIRD_PARTY_LICENSES.txt` by the frontend build and embedded in the binary. Go dependencies are listed in `go.mod`.
