# Contributing

Use Python 3.11+ and keep the frontend build-free. Code, comments, documentation and commit messages should be in English. Current UI labels are in French.

Use only public examples and synthetic fixtures in tests. Keep connection settings, credentials, generated catalogs, screenshots and backups in ignored local storage. Never copy a production catalog or organization-specific paths into a fixture, commit, issue or pull request.

Run `python -m unittest discover -s tests -p 'test_*.py' -v` for fast checks, and `bash tests/run.sh` for disposable database and Firefox checks. Run `python scripts/check_public.py` after staging and before committing. Verify that built wheels include the frontend assets and exclude local configuration and generated data. The repository ships no credentials; `public-test-only` is a disposable test value, never an example for a deployed database.

Changes to SQL execution must cover read-only enforcement, rollback, PostgreSQL savepoint recovery, MySQL implicit commits, and multiple configured sources. Do not add telemetry, remote access or default write permissions without an explicit design discussion.
