# Atlas Schema Desk

A local PostgreSQL and MySQL explorer: browse schemas and foreign keys, inspect stored programs and their impact, read table data, and use a SQL console with explicit commit and rollback. The interface includes schema history, CodeMirror completion, formatting, and optional backup restoration.

Python 3.11+ is required. PostgreSQL 10+ and MySQL 8 are tested. The application runs locally and needs no frontend build or cloud account.

## Start in a few minutes

```sh
git clone https://github.com/OlivierLB/AtlasSchemaDesk.git
cd AtlasSchemaDesk
python3 -m venv .venv
. .venv/bin/activate
pip install '.[drivers]'
cp atlas.example.toml atlas.toml
cp .env.example .env
```

Edit `atlas.toml` for an existing database and set its password in `.env` or your shell environment. Start with a database account that has only the privileges you need, preferably SELECT and catalog access.

```sh
atlas-schema-desk --config atlas.toml
# Open http://localhost:8765
```

The server exports catalogs on startup; click **Actualiser** to refresh them. The interface currently uses French labels. Export failures preserve the previous complete catalog and appear in the status banner. Missing drivers produce an installation hint on first use.

## Connections

Add any number of `[[connections]]` sections with unique lowercase IDs. The ID is independent of the driver: two PostgreSQL sources can use different names and colors. Each source has a persistent session and its own lock.

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

## SQL and transactions

Read-only is the default. It is checked in the API and configured in the database session. Session changes that could bypass this default are rejected. Use a database account with genuinely restricted permissions as an additional boundary; the SQL classifier is conservative guidance rather than a full SQL parser.

Enable `allow_writes = true` per connection when needed. Writes open a transaction that remains pending until **Valider** (commit) or **Annuler** (rollback). Closing the server rolls pending transactions back. PostgreSQL statements and grid reads use savepoints to recover from errors. A MySQL structure statement can commit an existing transaction even when that structure statement fails; the UI reports the resulting state. Some MySQL storage engines are not transactional. Calls to routines can have effects that static analysis cannot predict.

Console results are capped at 500 rows. Large text cells are truncated to 2,000 characters; binary cells display their byte length. The console uses `?` placeholders with a separate parameter list for grid edits. PostgreSQL statements time out after 15 seconds for the grid and 120 seconds for the console. MySQL applies its `max_execution_time` setting to eligible SELECT statements; it does not bound every kind of statement. Socket read/write timeouts provide an additional bound.

## Optional modules

To compare routines with a local source repository:

```toml
[routines_repository]
path = "../my-routines"
pattern = "{schema}/{kind}s/{name}.sql"
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

```sh
pip install '.[drivers,test]'
python -m playwright install firefox
bash tests/run.sh
```

The suite starts a separate `atlas-schema-desk-test` Compose project with PostgreSQL 18, PostgreSQL 10 and MySQL 8.4 on localhost ports 55432, 55433 and 53306. Its data uses disposable tmpfs storage. Pagila 3.0 and a public Sakila mirror are downloaded at pinned commits with SHA-256 verification into ignored storage. Tests add small probe tables, triggers, a procedure and an event. They verify transactions, savepoints, actual database read-only mode, MySQL implicit commits, configuration, optional backups, HTTP protections and Firefox UI behavior with six source IDs. The script stops only that test project on exit.

The sample databases retain their original licenses: [Pagila](https://github.com/devrimgunduz/pagila), and [Sakila's BSD license](https://dev.mysql.com/doc/sakila/en/sakila-license.html). Samples are downloaded for tests and are not shipped in the package.

Before committing, run `python scripts/check_public.py`. CI runs this check, unit/integration/browser tests and a package build. See [CONTRIBUTING.md](CONTRIBUTING.md).

## License

MIT. Vendored CodeMirror 5.65.21 and sql-formatter 15.9.0 retain their upstream MIT licenses in `atlas/public/vendor/`. `scripts/vendor-editor.sh` can refresh these assets with pinned npm integrity checks.
