# Contributing

Use Go 1.27+ and Node 22.12+ for development. The backend is Go (standard library HTTP, `pgconn`, `go-mysql`); the frontend is React and strict TypeScript, embedded into the binary. Release binaries and Docker images must require no Node or Python runtime. Code, comments, documentation and commit messages should be in English. Current UI labels are in French.

Use only public examples and synthetic fixtures in tests. Keep connection settings, credentials, generated catalogs, screenshots and backups in ignored local storage. Never copy a production catalog or organization-specific paths into a fixture, commit, issue or pull request.

Before publishing, run `python scripts/check_public.py --staged` to inspect the exact Git index. The default command also audits candidate working files. Optional organization-specific denylisted strings belong only in the ignored `.local/public-audit-markers.json` array; do not put private names or encoded private names into the public audit code. The checker reports file paths and categories, never matched values.

Run `make test` (go vet, gofmt, `go test -race`) for fast checks, and `bash tests/run.sh` for disposable database and Firefox checks. Run `python scripts/check_public.py` after staging and before committing. `python scripts/check_distribution.py bin/sulcata` verifies that the binary embeds the interface and runs with an empty PATH. The repository ships no credentials; `public-test-only` is a disposable test value, never an example for a deployed database.

The desktop app is the same binary built with `-tags desktop,production` (plus `webkit2_41` on Linux); keep `go vet` green for both builds, as CI does. Release files are produced only by `scripts/release.sh` and published by pushing a `vX.Y.Z` tag: the release workflow publishes nothing unless `scripts/test_get_matrix.sh` passes on every listed Linux distribution and the Windows job (install, window, in-app update, uninstall) passes. Keep release file names stable: the in-app updater and `get.sh` look them up by name.

Changes to SQL execution must cover read-only enforcement, rollback, PostgreSQL savepoint recovery, MySQL implicit commits, and multiple configured sources. Do not add telemetry, remote access or default write permissions without an explicit design discussion. The update check (GitHub releases API, disabled by `SULCATA_NO_UPDATE_CHECK`) is the only request beyond configured databases and backup sources, and sends nothing but the version in its User-Agent.
