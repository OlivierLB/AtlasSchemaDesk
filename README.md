# Atlas Schema Desk

A local PostgreSQL and MySQL explorer: browse schemas and foreign keys, inspect stored programs and their impact, read table data, and use a SQL console with explicit commit and rollback. The interface includes schema history, CodeMirror completion, formatting, and optional backup restoration.

Atlas Schema Desk is a single Go binary with the React interface embedded. PostgreSQL 10+ and MySQL 8 are tested. The application runs locally without a cloud account. Building from a checkout needs Go 1.27+ and Node 22.12+; a built binary or the Docker image needs neither, nor Python.

## Start in a few minutes

```sh
git clone https://github.com/OlivierLB/AtlasSchemaDesk.git
cd AtlasSchemaDesk
make frontend build          # or: npm ci/run build --prefix frontend, then go build
cp atlas.example.toml atlas.toml
cp .env.example .env
```

Edit `atlas.toml` for an existing database and set its password in `.env` or your shell environment. Start with a database account that has only the privileges you need, preferably SELECT and catalog access.

```sh
bin/atlas-schema-desk --config atlas.toml
# Open http://localhost:8765
```

The server exports catalogs on startup; click **Actualiser** to refresh them. The interface currently uses French labels. Export failures preserve the previous complete catalog and appear in the status banner. Database drivers are compiled in. `bin/atlas-schema-desk check --config atlas.toml` verifies every connection read-only, and `inspect` prints a credential-free configuration summary.

## Connections

Add any number of `[[connections]]` sections with unique lowercase IDs. The ID is independent of the driver: two PostgreSQL sources can use different names and colors. Each browser tab owns independent persistent connections, serialized per source. Reloading a tab retains its session; duplicating it creates a separate one.

```toml
[server]
bind = "127.0.0.1"
port = 8765
allowed_db_hosts = ["127.0.0.1", "localhost"]

[[connections]]
id = "library"
label = "Library"
driver = "postgresql"
host = "127.0.0.1"
port = 5432
database = "pagila"
user = "reader"
password_env = "LIBRARY_DB_PASSWORD"
default_schema = "public"
color = "#8ab4f8"
allow_writes = false
```

Use `driver = "mysql"` for MySQL and set `default_schema` to the database name. Passwords belong in environment variables, never in TOML. The `.env` parser accepts `KEY=value`, quoted values and full-line comments; it performs no interpolation, shell evaluation, or escape expansion. Existing environment variables take precedence. Paths resolve relative to the configuration file.

`atlas.toml`, `.env`, `private/`, `local/`, `.local/`, dumps, logs, test artifacts and generated catalogs are ignored by Git. Keep all organization-specific settings there. `.env.example` and `atlas.example.toml` contain only public examples. Never force-add local files. The public-content check also rejects tracked local configuration and generated data.

### Editing connections in the interface

The **Connexions et sauvegardes** page groups each database with its own optional S3 or directory backup source. Add or edit a source from its database card; the database association stays fixed in the form. A source must reference an existing connection, and a connection can own at most one source. The settings API exposes this relationship as `connections[].backup` (or `null`); the flat `dumpSources` list remains available for compatibility. The existing TOML `[[dump_sources]]` format stays supported.

**Connexions** (`#/connexions`) lists the configured databases and backup sources and lets you add, edit and delete them. Changes are validated with the startup loader, then written to the configuration file (the previous version is kept as `*.previous`; comments are not preserved). A password typed in the form is written to the sibling `.env` file (mode 600) and is never sent back to the browser; leave it empty to keep the current one. Connection IDs are immutable because catalogs and history are keyed by them; deleting a connection also removes its backup source. Hosts must belong to `allowed_db_hosts`, which stays file-only. **Appliquer et redémarrer** re-executes the server in place (same process, so launchers keep tracking it) once no transaction, request or maintenance job is running. Restore commands and local dump paths are displayed but only editable in the file: a web request must never choose a shell command or a file to overwrite.

## SQL and transactions

Read-only is the default. It is checked in the API and configured in the database session. Session changes that could bypass this default are rejected. Use a database account with genuinely restricted permissions as an additional boundary; the SQL classifier is conservative guidance rather than a full SQL parser.

Enable `allow_writes = true` per connection when needed. Writes open a transaction that remains pending until **Valider** (commit) or **Annuler** (rollback). Closing the server rolls pending transactions back. PostgreSQL statements and grid reads use savepoints to recover from errors. A MySQL structure statement can commit an existing transaction even when that structure statement fails; the UI reports the resulting state. Some MySQL storage engines are not transactional. Calls to routines can have effects that static analysis cannot predict.

Console results are capped at 500 rows. Values are shown as the database prints them (JSON as JSON, PostgreSQL arrays as `{…}`, booleans as `true`/`false`). Large text cells are truncated to 2,000 characters; binary cells display their byte length. The console uses `?` placeholders with a separate parameter list for grid edits. PostgreSQL statements time out after 15 seconds for the grid and 120 seconds for the console. MySQL applies its `max_execution_time` setting to eligible SELECT statements; it does not bound every kind of statement. Socket read/write timeouts provide an additional bound.

## Optional modules

Set `[server].refresh_dumps_on_start = false` to keep backup sources available while loading their existing local cache at startup. Remote listing then runs only when the user explicitly refreshes backups in the interface. The default is `true`; this setting never triggers a restore.

To compare routines with a local source repository:

```toml
[routines_repository]
path = "../my-routines"
pattern = "{schema}/{kind}s/{name}.sql"
# Optional: restrict source matching to specific connection IDs.
connections = ["library"]
# Find CREATE definitions in shared SQL files instead of requiring one file per routine.
scan_definitions = true
```

UTF-8 and Latin-1 source files are supported. Comparison ignores whitespace, comments and quote differences. Impact analysis derives reads, writes, calls and indirect writes from SQL text; dynamic SQL references are marked probable. Row counts are catalog estimates, and an unanalyzed nonempty PostgreSQL table displays an unknown count.

To list directory backups:

```toml
[[dump_sources]]
connection = "library"
type = "directory"
path = "./backups"
pattern = "*.sql.gz"
local_path = ".local/dumps/library.sql.gz"
restore_command = 'gzip -dc "$ATLAS_DUMP_PATH" | psql --dbname=pagila'
```

For S3, use `type = "s3-versioned"`, `bucket` and `key`; install and configure the AWS CLI separately. AWS CLI pagination is retained. Only exact-key object versions are offered. No backups are fetched or restored until requested. Dumps and source links stay hidden when their modules are unconfigured.

**Restoration replaces database contents.** `restore_command` is a trusted shell command from your local configuration and runs with your user privileges. The selected temporary download path is passed in `ATLAS_DUMP_PATH`, and the source ID in `ATLAS_CONNECTION`. The command must read that temporary file and return nonzero on failure. No request-supplied command is executed. Free disk space and size are checked, the previous local dump is kept until restoration succeeds, and open transactions or active requests block restoration. A failed command can still leave a partially restored database; this is not a database backup or atomic restore system.

## Docker

```sh
docker build -t atlas-schema-desk .
mkdir -p .local
export ATLAS_UID=$(id -u) ATLAS_GID=$(id -g)
docker compose up
```

The example Compose service mounts a local `atlas.toml` and `.env`, saves exports in an ignored directory, and publishes only `127.0.0.1:8765`. Configure database hosts explicitly. Inside a container, `127.0.0.1` refers to that container; Linux users can configure `host.docker.internal` using the example's host-gateway mapping and add it to `allowed_db_hosts`. Connect to your own networks explicitly if required.

The default HTTP bind is localhost. `0.0.0.0` is available for containers; publish its port only on localhost. The server validates HTTP Host headers and requires `X-DB-Atlas: 1` on POST requests to limit DNS rebinding and browser cross-origin requests. There is no authentication or TLS: do not expose it to the internet or an untrusted network. Only packaged static assets and generated catalog files are served; configuration and credentials are outside that directory.

## Tests

The interface uses React, strict TypeScript and Vite. Node 22.12+ is required for development (CI and Docker use Node 24). In two terminals, run `go run ./cmd/atlas-schema-desk --config atlas.toml` and `npm run dev --prefix frontend`. Vite proxies the API and catalogs to the local Go server.

Run `npm run typecheck --prefix frontend`, `npm run lint --prefix frontend` and `npm test --prefix frontend` for frontend checks. `frontend/openapi.json` is the HTTP contract: edit it with the Go handlers, then run `npm run api:generate --prefix frontend` and commit both. Go tests fail when a contract route is not served or a response field is undeclared. Build the frontend before `go build`: the binary embeds `web/dist/app`.

Sessions expire after 30 minutes without a database operation or explicit renewal. Status polling does not keep transactions alive. The interface warns five minutes before expiry; expiry and server shutdown close connections and roll back pending transactions. Reload recovery uses the browser's Web Locks API; browsers without it receive a fresh isolated session, and the previous session expires normally. A disconnected browser does not cancel or retry an SQL operation. If its outcome is uncertain, inspect the session and data before continuing.

Maintenance jobs stream bounded, redacted logs and support cancellation. Optional positive integer settings under `[server]` are `session_idle_seconds = 1800`, `export_timeout_seconds = 300`, `dump_list_timeout_seconds = 120`, and `restore_timeout_seconds = 3600`. Cancellation or failure of a restore can leave the database partially restored: inspect it before reuse. Process-tree cancellation is supported on Linux and in the Docker image.

```sh
make frontend
make test                                  # go vet, gofmt, go test -race
pip install -r tests/requirements.txt      # browser test tooling only
python -m playwright install firefox
bash tests/run.sh                          # disposable databases, integration and Firefox checks
python scripts/check_distribution.py bin/atlas-schema-desk
docker build -t atlas-schema-desk:test .
python scripts/check_docker.py
```

The suite starts a separate `atlas-schema-desk-test` Compose project with PostgreSQL 18, PostgreSQL 10 and MySQL 8.4 on localhost ports 55432, 55433 and 53306. Its data uses disposable tmpfs storage. Pagila 3.0 and a public Sakila mirror are downloaded at pinned commits with SHA-256 verification into ignored storage. Tests add small probe tables, triggers, a procedure and an event. They verify transactions, savepoints, actual database read-only mode, MySQL implicit commits, configuration, optional backups, HTTP protections and Firefox UI behavior with six source IDs. The script stops only that test project on exit.

The sample databases retain their original licenses: [Pagila](https://github.com/devrimgunduz/pagila), and [Sakila's BSD license](https://dev.mysql.com/doc/sakila/en/sakila-license.html). Samples are downloaded for tests and are not shipped in the package.

Before committing, run `python scripts/check_public.py`. CI runs this check, unit/integration/browser tests, a self-contained binary check and the Docker image check. `scripts/bench_backend.py` measures startup, memory and API latency of a running build. See [CONTRIBUTING.md](CONTRIBUTING.md).

## License

MIT. Third-party notices of the bundled interface (CodeMirror, sql-formatter, React…) are generated into `THIRD_PARTY_LICENSES.txt` by the frontend build and embedded in the binary. Go dependencies are listed in `go.mod`.
