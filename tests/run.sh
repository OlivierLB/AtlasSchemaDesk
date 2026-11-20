#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
python -m unittest discover -s tests -p 'test_*.py' -v
compose=(docker compose -p atlas-schema-desk-test -f docker-compose.test.yml)
trap '"${compose[@]}" down -v' EXIT
"${compose[@]}" down -v
"${compose[@]}" up -d --wait
python tests/fetch_samples.py
python tests/setup_samples.py
ATLAS_TEST_CONFIG="$PWD/.local/test.toml" python -m unittest discover -s tests -p 'integration_*.py' -v
python tests/ui.py
