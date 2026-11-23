#!/usr/bin/env bash
# Full verification: Go unit tests, disposable sample databases, Go integration
# tests and Firefox browser checks against the built binary. Rerunnable.
set -euo pipefail
cd "$(dirname "$0")/.."
GO="${GO:-$(command -v go || echo "$HOME/.local/go/bin/go")}"
"$GO" test -race ./...
compose=(docker compose -p atlas-schema-desk-test -f docker-compose.test.yml)
trap '"${compose[@]}" down -v' EXIT
"${compose[@]}" down -v
"${compose[@]}" up -d --wait
python3 tests/fetch_samples.py
python3 tests/setup_samples.py
ATLAS_TEST_CONFIG="$PWD/.local/test.toml" "$GO" test -race -count=1 -run Integration ./internal/...
"$GO" build -trimpath -o bin/atlas-schema-desk ./cmd/atlas-schema-desk
python3 tests/ui.py
