#!/usr/bin/env bash
# Builds the Linux desktop app in an Ubuntu 22.04 container (the oldest glibc
# the release targets), starts it under Xvfb from an empty user profile and
# saves a screenshot. It then publishes a fake newer release and checks that
# the in-app update downloads it, verifies it, replaces the binary and
# restarts. Rerunnable; needs Docker and a built frontend (web/dist/app).
#
#   scripts/desktop_smoke.sh [OUTPUT_DIR]   (default: .local/desktop-smoke)
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
out=${1:-$root/.local/desktop-smoke}
mkdir -p "$out"
# Go may live outside PATH (~/.local/go, or the toolchain install.sh downloads).
GO=${GO:-$(command -v go || true)}
for candidate in "$HOME/.local/go/bin/go" "${ATLAS_TOOLCHAIN_DIR:-$HOME/.local/atlas-toolchain}"/go-*/bin/go; do
	[[ -n $GO ]] && break
	if [[ -x $candidate ]]; then GO=$candidate; fi
done
[[ -n $GO ]] || { echo "Go introuvable : lance ./install.sh (il l'installe) ou définis GO=/chemin/vers/go" >&2; exit 1; }
goroot=$("$GO" env GOROOT)
modcache=$("$GO" env GOMODCACHE)
[[ -f $root/web/dist/app/index.html ]] || { echo "Build the frontend first (make frontend)." >&2; exit 1; }

docker run --rm -v "$root:/src:ro" -v "$out:/out" -v "$goroot:/go-root:ro" -v "$modcache:/gomod:ro" \
  -e GOMODCACHE=/gomod -e GOFLAGS=-mod=mod -e GOCACHE=/tmp/gocache -e GOTOOLCHAIN=local \
  ubuntu:22.04 bash -euo pipefail -c '
    export DEBIAN_FRONTEND=noninteractive PATH=/go-root/bin:$PATH
    echo "==> Paquets de compilation et Xvfb"
    apt-get update -qq >/dev/null
    apt-get install -y -qq gcc pkg-config libgtk-3-dev libwebkit2gtk-4.1-dev xvfb imagemagick curl dbus-x11 python3 >/dev/null
    cp -r /src /build && cd /build
    build() { go build -tags desktop,production,webkit2_41 -ldflags "-s -w -X main.version=$1" -o "$2" ./cmd/atlas-schema-desk; }
    echo "==> Compilation 0.3.0 (installée) et 0.4.0 (fausse release)"
    mkdir -p /tmp/profile/.local/share/atlas-schema-desk/app /tmp/release
    build 0.3.0 /tmp/profile/.local/share/atlas-schema-desk/app/atlas-schema-desk
    build 0.4.0 /tmp/release/atlas-schema-desk
    ldd /tmp/release/atlas-schema-desk | grep -E "webkit|gtk"
    tar -C /tmp/release -czf /tmp/release/atlas-schema-desk_linux_amd64.tar.gz atlas-schema-desk
    (cd /tmp/release && sha256sum atlas-schema-desk_linux_amd64.tar.gz > SHA256SUMS)
    mkdir -p /tmp/release/repos/OlivierLB/AtlasSchemaDesk/releases
    cat > /tmp/release/repos/OlivierLB/AtlasSchemaDesk/releases/latest <<JSON
{"tag_name":"v0.4.0","html_url":"http://127.0.0.1:9900/","assets":[
 {"name":"atlas-schema-desk_linux_amd64.tar.gz","browser_download_url":"http://127.0.0.1:9900/atlas-schema-desk_linux_amd64.tar.gz"},
 {"name":"SHA256SUMS","browser_download_url":"http://127.0.0.1:9900/SHA256SUMS"}]}
JSON
    (cd /tmp/release && python3 -m http.server 9900 >/dev/null 2>&1 &)

    echo "==> Lancement sous Xvfb depuis un profil vide"
    export HOME=/tmp/profile XDG_CONFIG_HOME=/tmp/profile/.config DISPLAY=:99 ATLAS_UPDATE_API=http://127.0.0.1:9900
    cd $HOME
    Xvfb :99 -screen 0 1440x900x24 >/dev/null 2>&1 &
    sleep 1
    dbus-launch --exit-with-session $HOME/.local/share/atlas-schema-desk/app/atlas-schema-desk > /out/app.log 2>&1 &
    for _ in $(seq 60); do curl -sf localhost:8765/api/config >/dev/null && break; sleep 0.5; done
    curl -s localhost:8765/api/config; echo
    sleep 6
    import -window root /out/window.png
    test -f $XDG_CONFIG_HOME/atlas-schema-desk/atlas.toml && echo "Configuration créée."

    echo "==> Mise à jour proposée puis installée"
    curl -s localhost:8765/api/update; echo
    curl -s -X POST -H "X-DB-Atlas: 1" -H "Content-Type: application/json" -d "{}" localhost:8765/api/update/install; echo
    sleep 3
    for _ in $(seq 60); do curl -sf localhost:8765/api/update | grep -q "\"current\":\"0.4.0\"" && break; sleep 0.5; done
    curl -s localhost:8765/api/update; echo
    $HOME/.local/share/atlas-schema-desk/app/atlas-schema-desk version
    sleep 4
    import -window root /out/after-update.png
    echo "==> Journal"; cat /out/app.log
  '
echo "Captures : $out/window.png $out/after-update.png"
