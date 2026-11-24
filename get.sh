#!/usr/bin/env bash
# Installs the Sulcata desktop app for the current Linux user from
# the latest GitHub release, with no build tools:
#
#   curl -fsSL https://raw.githubusercontent.com/OlivierLB/Sulcata/main/get.sh | bash
#
# - downloads the app and checks its SHA-256 against the release's SHA256SUMS;
# - installs it in ~/.local/share/sulcata/app (no sudo), adds a menu
#   entry and the sulcata command in ~/.local/bin;
# - installs WebKitGTK 4.1 with the package manager (sudo, after confirmation)
#   when the system lacks it.
# The app then updates itself from the interface. Rerunning reinstalls the
# latest version. Uninstalling keeps the configuration
# (~/.config/sulcata) and the window's browser data (SQL history).
#
# Options: --uninstall   remove the app
#          --yes         answer yes to the WebKitGTK installation question
#
# Everything runs from main(), called on the last line: a truncated download
# runs nothing, and commands cannot consume the rest of a piped script.
set -euo pipefail

REPO=OlivierLB/Sulcata
ASSET=sulcata_linux_amd64.tar.gz
BASE_URL=${SULCATA_RELEASE_URL:-https://github.com/$REPO/releases/latest/download}
DATA_HOME=${XDG_DATA_HOME:-$HOME/.local/share}
# The window's WebKit profile (localStorage, SQL history) lives in
# $DATA_HOME/sulcata itself: the program goes in a subfolder.
APP_DIR=$DATA_HOME/sulcata/app
BIN_DIR=$HOME/.local/bin
DESKTOP_FILE=$DATA_HOME/applications/sulcata.desktop
ICON_FILE=$DATA_HOME/icons/hicolor/512x512/apps/sulcata.png

step() { printf '\n\033[1m==> %s\033[0m\n' "$*"; }
info() { printf '    %s\n' "$*"; }
die() {
	printf '\n\033[31mÉchec :\033[0m %s\n' "$*" >&2
	exit 1
}

refresh_menu() {
	if command -v update-desktop-database >/dev/null; then update-desktop-database -q "$DATA_HOME/applications" 2>/dev/null || true; fi
	if command -v gtk-update-icon-cache >/dev/null; then gtk-update-icon-cache -q -t "$DATA_HOME/icons/hicolor" 2>/dev/null || true; fi
}

uninstall() {
	step "Désinstallation"
	rm -f "$DESKTOP_FILE" "$ICON_FILE"
	if [[ -L $BIN_DIR/sulcata ]]; then rm -f "$BIN_DIR/sulcata"; fi
	rm -rf "$APP_DIR"
	refresh_menu
	info "Application supprimée."
	info "Conservés : la configuration (${XDG_CONFIG_HOME:-$HOME/.config}/sulcata)"
	info "et les données de la fenêtre, dont l'historique SQL ($DATA_HOME/sulcata)."
}

# ldconfig lives in /sbin, often outside a regular user's PATH; grep reads
# everything (no -q) so pipefail never sees ldconfig killed by SIGPIPE.
has_webkit() {
	local ldconfig dir
	for ldconfig in "$(command -v ldconfig || true)" /sbin/ldconfig /usr/sbin/ldconfig; do
		if [[ -x $ldconfig ]] && "$ldconfig" -p 2>/dev/null | grep 'libwebkit2gtk-4\.1\.so\.0' >/dev/null; then return 0; fi
	done
	for dir in /usr/lib/x86_64-linux-gnu /usr/lib64 /usr/lib; do
		if compgen -G "$dir/libwebkit2gtk-4.1.so.0*" >/dev/null; then return 0; fi
	done
	return 1
}

install_webkit() {
	local yes=$1 package= manager= answer
	local -a sudo=()
	if command -v apt-get >/dev/null; then manager="apt-get install -y -q" package=libwebkit2gtk-4.1-0
	elif command -v dnf >/dev/null; then manager="dnf install -y" package=webkit2gtk4.1
	elif command -v pacman >/dev/null; then manager="pacman -S --needed --noconfirm" package=webkit2gtk-4.1
	elif command -v zypper >/dev/null; then manager="zypper --non-interactive install" package=libwebkit2gtk-4_1-0
	fi
	[[ -n $manager ]] || die "WebKitGTK 4.1 est absent et aucun gestionnaire de paquets reconnu : installe libwebkit2gtk-4.1 puis relance ce script"
	if ((EUID != 0)); then
		command -v sudo >/dev/null || die "WebKitGTK 4.1 est absent : installe le paquet $package (droits administrateur nécessaires) puis relance ce script"
		sudo=(sudo)
	fi
	if ((!yes)); then
		if [[ -r /dev/tty ]]; then
			printf '    Installer %s avec %s ? [O/n] ' "$package" "${manager%% *}"
			read -r answer </dev/tty || answer=n
		else
			answer=n
		fi
		[[ -z $answer || $answer == [oOyY]* ]] || die "WebKitGTK 4.1 est nécessaire : installe $package puis relance ce script (ou ajoute --yes)"
	fi
	# </dev/null: with `curl | bash`, stdin is the script itself. sudo asks
	# for its password on the terminal, not on stdin.
	if [[ $manager == apt-get* ]]; then "${sudo[@]}" env DEBIAN_FRONTEND=noninteractive apt-get update -qq </dev/null; fi
	# shellcheck disable=SC2086 # $manager holds the command and its options
	"${sudo[@]}" env DEBIAN_FRONTEND=noninteractive $manager "$package" </dev/null
	has_webkit || die "WebKitGTK 4.1 toujours introuvable après l'installation de $package"
	info "Installé."
}

main() {
	local yes=0 argument expected actual version
	for argument in "$@"; do
		case $argument in
		--uninstall)
			uninstall
			return 0
			;;
		--yes | -y) yes=1 ;;
		-h | --help)
			echo "Usage : get.sh [--yes] [--uninstall]   (curl ... | bash -s -- --yes)"
			return 0
			;;
		*) die "option inconnue : $argument (voir --help)" ;;
		esac
	done

	step "Vérification du système"
	[[ $(uname -s) == Linux ]] || die "ce script est pour Linux. Sous Windows, télécharge l'installeur depuis https://github.com/$REPO/releases/latest"
	case $(uname -m) in
	x86_64 | amd64) ;;
	*) die "l'application de bureau existe pour Linux x86_64 seulement ; sur $(uname -m), utilise le serveur (sulcata-server_linux_arm64.tar.gz) depuis https://github.com/$REPO/releases/latest" ;;
	esac
	for argument in curl tar sha256sum; do
		command -v "$argument" >/dev/null || die "$argument est nécessaire : installe-le puis relance ce script"
	done
	info "Linux x86_64, outils présents."

	step "WebKitGTK 4.1 (moteur d'affichage de la fenêtre)"
	if has_webkit; then info "Présent."; else install_webkit "$yes"; fi

	step "Téléchargement de la dernière version"
	work=$(mktemp -d) # global: the EXIT trap runs after main returns
	trap 'rm -rf "$work"' EXIT
	curl -fsSL --retry 3 -o "$work/$ASSET" "$BASE_URL/$ASSET" </dev/null || die "téléchargement impossible : $BASE_URL/$ASSET"
	curl -fsSL --retry 3 -o "$work/SHA256SUMS" "$BASE_URL/SHA256SUMS" </dev/null || die "téléchargement impossible : $BASE_URL/SHA256SUMS"
	expected=$(awk -v name="$ASSET" '$2 == name || $2 == "*" name { print $1 }' "$work/SHA256SUMS")
	[[ $expected =~ ^[0-9a-f]{64}$ ]] || die "$ASSET absent de SHA256SUMS"
	actual=$(sha256sum "$work/$ASSET" | cut -d' ' -f1)
	[[ $actual == "$expected" ]] || die "somme SHA-256 incorrecte : fichier refusé"
	info "Somme SHA-256 vérifiée."

	step "Installation"
	mkdir -p "$work/app"
	tar -xzf "$work/$ASSET" -C "$work/app" </dev/null
	[[ -x $work/app/sulcata ]] || die "archive incomplète : sulcata manquant"
	mkdir -p "$APP_DIR" "$BIN_DIR" "$(dirname "$DESKTOP_FILE")" "$(dirname "$ICON_FILE")"
	# Replace through a rename: a running copy keeps working until it restarts.
	install -m 755 "$work/app/sulcata" "$APP_DIR/.sulcata.new"
	mv -f "$APP_DIR/.sulcata.new" "$APP_DIR/sulcata"
	install -m 644 "$work/app/LICENSE" "$APP_DIR/LICENSE"
	install -m 644 "$work/app/icon.png" "$ICON_FILE"
	sed "s|^Exec=.*|Exec=\"$APP_DIR/sulcata\"|" "$work/app/sulcata.desktop" >"$DESKTOP_FILE"
	ln -sfn "$APP_DIR/sulcata" "$BIN_DIR/sulcata"
	refresh_menu
	version=$("$APP_DIR/sulcata" version </dev/null 2>/dev/null || echo "?")
	info "Sulcata $version installé dans $APP_DIR"

	step "Terminé"
	info "Lance « Sulcata » depuis le menu des applications,"
	info "ou tape : sulcata"
	case :$PATH: in
	*":$BIN_DIR:"*) ;;
	*) info "(ajoute $BIN_DIR à ton PATH pour la commande, ou utilise $APP_DIR/sulcata)" ;;
	esac
	info "Les mises à jour se proposent ensuite dans l'application."
}

main "$@"
