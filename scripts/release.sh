#!/usr/bin/env bash
# Builds every release file into OUTPUT_DIR (default dist/):
#   sulcata_linux_amd64.tar.gz          desktop app, Linux (WebKitGTK 4.1)
#   sulcata_windows_amd64_setup.exe     desktop app installer, Windows
#   sulcata-server_<os>_<arch>.tar.gz   headless server (Linux, macOS; .zip for Windows)
#   SHA256SUMS                                    checked by the in-app updater
# Needs Go, a built frontend (web/dist/app), gcc + libgtk-3-dev +
# libwebkit2gtk-4.1-dev for the Linux desktop build, makensis and zip; runs
# on Linux. go-winres is fetched with `go run` (network) unless SKIP_WINRES=1.
#
#   scripts/release.sh VERSION [OUTPUT_DIR]
set -euo pipefail

version=${1:?usage: scripts/release.sh VERSION [OUTPUT_DIR]}
version=${version#v}
[[ $version =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo "Version x.y.z attendue, reçu : $version" >&2; exit 2; }
root=$(cd "$(dirname "$0")/.." && pwd)
out=$(mkdir -p "${2:-$root/dist}" && cd "${2:-$root/dist}" && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"; rm -f "$root"/cmd/sulcata/rsrc_windows_*.syso' EXIT
cd "$root"
[[ -f web/dist/app/index.html ]] || { echo "Interface non compilée : lance d'abord npm ci && npm run build dans frontend/." >&2; exit 1; }
ldflags="-s -w -X main.version=$version"
step() { printf '\n==> %s\n' "$*"; }

step "Application de bureau Linux amd64"
mkdir -p "$work/linux"
CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go build -trimpath -tags desktop,production,webkit2_41 \
  -ldflags "$ldflags" -o "$work/linux/sulcata" ./cmd/sulcata
cp packaging/linux/sulcata.desktop packaging/icon.png LICENSE "$work/linux/"
tar -C "$work/linux" -czf "$out/sulcata_linux_amd64.tar.gz" \
  sulcata sulcata.desktop icon.png LICENSE

step "Application de bureau Windows amd64"
if [[ ${SKIP_WINRES:-} != 1 ]]; then
  (cd packaging/windows && go run github.com/tc-hib/go-winres@v0.3.3 make --in winres.json --arch amd64 \
    --product-version "$version.0" --file-version "$version.0" --out ../../cmd/sulcata/rsrc)
fi
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -tags desktop,production \
  -ldflags "$ldflags -H windowsgui" -o "$work/sulcata.exe" ./cmd/sulcata
webview2=$(go list -m -f '{{.Dir}}' github.com/wailsapp/wails/v2)/internal/webview2runtime/MicrosoftEdgeWebview2Setup.exe
[[ -f $webview2 ]] || { echo "Programme d'installation WebView2 introuvable : $webview2" >&2; exit 1; }
(cd packaging/windows && makensis -V2 -DVERSION="$version" -DBINARY="$work/sulcata.exe" \
  -DWEBVIEW2="$webview2" -DOUTFILE="$out/sulcata_windows_amd64_setup.exe" installer.nsi)

step "Serveur sans interface (linux, darwin, windows)"
# One top-level folder per archive; .zip for Windows (Explorer opens it).
for target in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64; do
  goos=${target%/*} goarch=${target#*/}
  name=sulcata-server_${goos}_${goarch}
  exe=sulcata
  [[ $goos == windows ]] && exe=sulcata.exe
  mkdir -p "$work/$name"
  CGO_ENABLED=0 GOOS=$goos GOARCH=$goarch go build -trimpath -ldflags "$ldflags" \
    -o "$work/$name/$exe" ./cmd/sulcata
  cp LICENSE sulcata.example.toml "$work/$name/"
  if [[ $goos == windows ]]; then
    (cd "$work" && zip -qr "$out/$name.zip" "$name")
  else
    tar -C "$work" -czf "$out/$name.tar.gz" "$name"
  fi
  echo "    $goos/$goarch"
done

step "Sommes de contrôle"
(cd "$out" && rm -f SHA256SUMS && sha256sum -- *.tar.gz *.zip *.exe > SHA256SUMS && cat SHA256SUMS)
