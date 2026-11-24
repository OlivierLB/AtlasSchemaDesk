#!/usr/bin/env bash
# Checks that get.sh and the Linux desktop app work for everyone, not just
# on the release machine: for each distribution, from a clean image and as a
# regular user (sudo allowed, /sbin not in PATH) and piped like `curl | bash`,
# get.sh installs WebKitGTK,
# the app is installed, all its libraries resolve, the real window starts
# under Xvfb and serves the interface, then the app uninstalls cleanly.
# Rerunnable; needs Docker and the release files (scripts/release.sh).
#
#   scripts/test_get_matrix.sh DIST_DIR [DISTRIBUTION...]
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
dist=$(cd "${1:?usage: scripts/test_get_matrix.sh DIST_DIR [DISTRIBUTION...]}" && pwd)
shift
[[ -f $dist/sulcata_linux_amd64.tar.gz && -f $dist/SHA256SUMS ]] || { echo "Fichiers de release absents de $dist" >&2; exit 1; }

# Image and the root command that gives it what any desktop already has
# (curl, sudo, CA certificates) plus Xvfb for the test.
declare -A setup=(
	[ubuntu:22.04]="apt-get update -qq && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq curl ca-certificates sudo xvfb"
	[ubuntu:24.04]="apt-get update -qq && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq curl ca-certificates sudo xvfb"
	[debian:12]="apt-get update -qq && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq curl ca-certificates sudo xvfb"
	[debian:13]="apt-get update -qq && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq curl ca-certificates sudo xvfb"
	[fedora:42]="dnf install -y -q curl sudo xorg-x11-server-Xvfb which findutils"
	[archlinux:latest]="pacman -Syu --noconfirm --needed -q curl sudo xorg-server-xvfb"
	[opensuse/tumbleweed:latest]="zypper --non-interactive --quiet install curl sudo xvfb-run xorg-x11-server-Xvfb gzip tar"
)
order=(ubuntu:22.04 ubuntu:24.04 debian:12 debian:13 fedora:42 archlinux:latest opensuse/tumbleweed:latest)
(($#)) && order=("$@")

check='
set -euo pipefail
export HOME=/home/user PATH=/usr/local/bin:/usr/bin:/bin SULCATA_RELEASE_URL=file:///dist SULCATA_NO_UPDATE_CHECK=1
# Exactly like `curl ... | bash -s -- --yes`: the script arrives on stdin.
cat /get.sh | bash -s -- --yes
app=$HOME/.local/share/sulcata/app/sulcata
missing=$(ldd "$app" | grep "not found" || true)
[[ -z $missing ]] || { echo "Bibliothèques manquantes : $missing"; exit 1; }
test -f $HOME/.local/share/applications/sulcata.desktop
Xvfb :42 -screen 0 1280x800x24 >/tmp/xvfb.log 2>&1 &
sleep 1
DISPLAY=:42 "$app" --port 8791 >/tmp/app.log 2>&1 &
pid=$!
ok=0
for _ in $(seq 60); do
	if curl -sf localhost:8791/api/config >/dev/null; then ok=1; break; fi
	sleep 0.5
done
sleep 3
if ((ok == 0)) || ! kill -0 $pid 2>/dev/null; then echo "La fenêtre ne démarre pas :"; cat /tmp/app.log; exit 1; fi
kill $pid
sleep 1
test -d $HOME/.local/share/sulcata/storage || { echo "Profil WebKit absent"; exit 1; }
cat /get.sh | bash -s -- --uninstall >/dev/null
test ! -e "$app"
test -d $HOME/.local/share/sulcata/storage || { echo "La désinstallation a effacé les données de la fenêtre"; exit 1; }
echo "OK : installation (curl | bash), bibliothèques, fenêtre, désinstallation (données conservées)"
'

logs=$(mktemp -d)
failed=()
for image in "${order[@]}"; do
	[[ -n ${setup[$image]:-} ]] || { echo "Distribution inconnue : $image" >&2; exit 2; }
	printf '\n==> %s\n' "$image"
	log=$logs/${image//[\/:]/_}.log
	status=0
	docker run --rm -v "$root/get.sh:/get.sh:ro" -v "$dist:/dist:ro" "$image" bash -c "
		set -euo pipefail
		{ ${setup[$image]}; } >/tmp/setup.log 2>&1 || { cat /tmp/setup.log; exit 1; }
		useradd -m user
		echo 'user ALL=(ALL) NOPASSWD: ALL' > /etc/sudoers.d/user
		su user -s /bin/bash -c '$check'
	" >"$log" 2>&1 || status=$?
	if ((status == 0)); then
		grep -E 'Installer|Présent|Installé\.|OK :' "$log" | sed 's/^/    /'
	else
		failed+=("$image")
		echo "    ÉCHEC (code $status), fin du journal :"
		tail -25 "$log" | sed 's/^/    | /'
	fi
done

printf '\n'
if ((${#failed[@]})); then
	echo "ÉCHEC sur : ${failed[*]} (journaux : $logs)"
	exit 1
fi
rm -rf "$logs"
echo "Toutes les distributions passent : ${order[*]}"
