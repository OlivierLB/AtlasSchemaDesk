#!/usr/bin/env bash
# Downloads the SQL editor libraries into public/vendor/ (rerunnable, offline afterwards).
#
# - CodeMirror 5 (MIT): editor, SQL mode, autocomplete (sql-hint), brackets, comments.
# - sql-formatter (MIT): « Mettre en forme ».
# Versions and npm integrity hashes are pinned: a tarball whose sha512 differs is rejected.
# Already-vendored versions are skipped. Nothing from the archives is executed.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
VENDOR="$ROOT/atlas/public/vendor"
CODEMIRROR_VERSION="5.65.21"
CODEMIRROR_INTEGRITY="sha512-6teYk0bA0nR3QP0ihGMoxuKzpl5W80FpnHpBJpgy66NK3cZv5b/d/HY8PnRvfSsCG1MTfr92u2WUl+wT0E40mQ=="
FORMATTER_VERSION="15.9.0"
FORMATTER_INTEGRITY="sha512-3A/N+v+mA5/PDU+FlO4uTFE0L/Mmg63c/Ny161Qv2b1Mxp9IX8War+LLmq17LA/Kgu4KXAR7iutK2JaHWJMMtw=="

CODEMIRROR_FILES=(
  lib/codemirror.js lib/codemirror.css mode/sql/sql.js
  addon/hint/show-hint.js addon/hint/show-hint.css addon/hint/sql-hint.js
  addon/edit/matchbrackets.js addon/edit/closebrackets.js addon/comment/comment.js
  addon/selection/active-line.js addon/display/placeholder.js LICENSE
)

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

fetch() { # fetch <package> <version> <integrity> → extracted package dir on stdout
  local name="$1" version="$2" integrity="$3" tarball="$work/$1.tgz" actual
  echo "Téléchargement de $name@$version…" >&2
  curl -fsSL "https://registry.npmjs.org/$name/-/$name-$version.tgz" -o "$tarball"
  actual="sha512-$(openssl dgst -sha512 -binary "$tarball" | base64 -w0)"
  if [[ "$actual" != "$integrity" ]]; then
    echo "Empreinte inattendue pour $name@$version : $actual (attendu $integrity). Arrêt." >&2
    exit 1
  fi
  mkdir -p "$work/$name"
  tar -xzf "$tarball" -C "$work/$name" --no-same-owner
  echo "$work/$name/package"
}

target="$VENDOR/codemirror-$CODEMIRROR_VERSION"
if [[ -f "$target/lib/codemirror.js" ]]; then
  echo "CodeMirror $CODEMIRROR_VERSION déjà présent."
else
  source_dir="$(fetch codemirror "$CODEMIRROR_VERSION" "$CODEMIRROR_INTEGRITY")"
  for file in "${CODEMIRROR_FILES[@]}"; do
    install -D -m 0644 "$source_dir/$file" "$target/$file"
  done
  echo "CodeMirror installé dans $target"
fi

target="$VENDOR/sql-formatter-$FORMATTER_VERSION"
if [[ -f "$target/sql-formatter.min.js" ]]; then
  echo "sql-formatter $FORMATTER_VERSION déjà présent."
else
  source_dir="$(fetch sql-formatter "$FORMATTER_VERSION" "$FORMATTER_INTEGRITY")"
  bundle="$(find "$source_dir/dist" -maxdepth 1 -name 'sql-formatter.min.js' -o -maxdepth 1 -name 'sql-formatter.min.cjs' | head -1)"
  [[ -n "$bundle" ]] || { echo "Bundle navigateur introuvable dans sql-formatter@$FORMATTER_VERSION/dist" >&2; ls "$source_dir/dist" >&2; exit 1; }
  install -D -m 0644 "$bundle" "$target/sql-formatter.min.js"
  install -D -m 0644 "$source_dir/LICENSE" "$target/LICENSE"
  echo "sql-formatter installé dans $target (depuis $(basename "$bundle"))"
fi
