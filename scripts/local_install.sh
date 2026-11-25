#!/usr/bin/env bash
# Installs the desktop app from this working copy exactly as a user would get
# it: builds the release files in Docker (Ubuntu 22.04, no local build tools
# besides Go and a built frontend), serves them as a fake release on
# 127.0.0.1, then runs get.sh against it. Rerunnable; get.sh asks before
# installing WebKitGTK with sudo.
#
#   scripts/local_install.sh [VERSION]     (default 0.4.2)
#   scripts/local_install.sh --uninstall
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
if [[ ${1:-} == --uninstall ]]; then
	exec bash "$root/get.sh" --uninstall
fi
version=${1:-0.4.2}
out=$root/.local/local-release
port=9917
# Go may live outside PATH (~/.local/go, or the toolchain install.sh downloads).
GO=${GO:-$(command -v go || true)}
for candidate in "$HOME/.local/go/bin/go" "${SULCATA_TOOLCHAIN_DIR:-$HOME/.local/sulcata-toolchain}"/go-*/bin/go; do
	[[ -n $GO ]] && break
	if [[ -x $candidate ]]; then GO=$candidate; fi
done
[[ -n $GO ]] || { echo "Go introuvable : lance ./install.sh (il l'installe) ou définis GO=/chemin/vers/go" >&2; exit 1; }
goroot=$("$GO" env GOROOT)
modcache=$("$GO" env GOMODCACHE)
[[ -f $root/web/dist/app/index.html ]] || { echo "Compile d'abord l'interface : make frontend" >&2; exit 1; }

echo "==> Fichiers de release $version (Docker, Ubuntu 22.04)"
rm -rf "$out" && mkdir -p "$out"
docker run --rm -v "$root:/src:ro" -v "$out:/out" -v "$goroot:/go-root:ro" -v "$modcache:/gomod:ro" \
	-e GOMODCACHE=/gomod -e GOFLAGS=-mod=mod -e GOCACHE=/tmp/gocache -e GOTOOLCHAIN=local -e SKIP_WINRES=1 \
	-e HOST_UID="$(id -u)" -e HOST_GID="$(id -g)" -e VERSION="$version" \
	ubuntu:22.04 bash -euo pipefail -c '
		export DEBIAN_FRONTEND=noninteractive PATH=/go-root/bin:$PATH
		apt-get update -qq >/dev/null
		apt-get install -y -qq gcc pkg-config libgtk-3-dev libwebkit2gtk-4.1-dev nsis zip >/dev/null 2>&1
		cp -r /src /build && cd /build
		scripts/release.sh "$VERSION" /out
		chown -R "$HOST_UID:$HOST_GID" /out'

echo "==> Fausse release servie sur http://127.0.0.1:$port"
python3 -m http.server "$port" --bind 127.0.0.1 --directory "$out" >/dev/null 2>&1 &
server=$!
trap 'kill $server 2>/dev/null || true' EXIT
for _ in $(seq 20); do curl -sf "http://127.0.0.1:$port/SHA256SUMS" >/dev/null && break; sleep 0.2; done

SULCATA_RELEASE_URL=http://127.0.0.1:$port bash "$root/get.sh"
