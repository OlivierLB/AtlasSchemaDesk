#!/usr/bin/env bash
# Sulcata installer: from a fresh clone to a running, verified server.
#
#   ./install.sh                 find or install Go and Node, build, configure, check
#   ./install.sh --start         ...then start the server
#   ./install.sh --rebuild       force a rebuild even if the binary is up to date
#   ./install.sh --non-interactive
#                                never prompt; copy the example configuration instead
#   ./install.sh --configure     run the configuration prompts even without a terminal
#   ./install.sh --yes           install missing system packages without asking
#
# Installs everything Sulcata needs to run, and nothing for development:
#  - Go and Node (build only) go to $SULCATA_TOOLCHAIN_DIR (default
#    ~/.local/sulcata-toolchain), checksum-verified, without sudo;
#  - missing system tools (curl, tar, gzip, CA certificates) and the tools used
#    by configured backup sources (S3 CLI, psql, mysql, decompressors) come from
#    the system package manager, with sudo, after confirmation.
# Re-runnable: every step skips what is already in place. No shell profile is
# modified. Existing sulcata.toml and .env files are never overwritten.
set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
cd "$ROOT"

# Versions installed when no suitable toolchain is found. Minimums come from
# go.mod and frontend/package.json so they cannot drift from the build.
GO_INSTALL_VERSION=1.27.4
NODE_INSTALL_VERSION=24.11.0
TOOLCHAIN_DIR=${SULCATA_TOOLCHAIN_DIR:-$HOME/.local/sulcata-toolchain}
CONFIG=${SULCATA_CONFIG:-$ROOT/sulcata.toml}
BINARY=$ROOT/bin/sulcata

START=0 REBUILD=0 MODE=auto YES=0
for arg in "$@"; do
	case $arg in
	--start) START=1 ;;
	--rebuild) REBUILD=1 ;;
	--non-interactive) MODE=never ;;
	--configure) MODE=always ;;
	-y | --yes) YES=1 ;;
	-h | --help)
		sed -n '2,20p' "$0" | sed 's/^# \{0,1\}//'
		exit 0
		;;
	*)
		echo "Option inconnue : $arg (voir --help)" >&2
		exit 2
		;;
	esac
done

step() { printf '\n\033[1m==> %s\033[0m\n' "$*"; }
info() { printf '    %s\n' "$*"; }
warn() { printf '    \033[33mAttention :\033[0m %s\n' "$*" >&2; }
die() {
	printf '\n\033[31mÉchec :\033[0m %s\n' "$*" >&2
	exit 1
}

# version_ge A B: true when version A >= version B (numeric dot-separated).
version_ge() {
	local i a b
	IFS=. read -ra a <<<"$1"
	IFS=. read -ra b <<<"$2"
	for ((i = 0; i < 3; i++)); do
		if ((10#${a[i]:-0} != 10#${b[i]:-0})); then
			((10#${a[i]:-0} > 10#${b[i]:-0}))
			return
		fi
	done
	return 0
}

sha256() {
	if command -v sha256sum >/dev/null; then sha256sum "$1" | cut -d' ' -f1; else shasum -a 256 "$1" | cut -d' ' -f1; fi
}

download() { # url destination
	info "Téléchargement de $1"
	curl --fail --location --silent --show-error --retry 2 --output "$2" "$1" ||
		die "téléchargement impossible : $1 (vérifie l'accès réseau ou un proxy)"
}

verify_checksum() { # file expected
	local actual
	[[ $2 =~ ^[0-9a-f]{64}$ ]] || die "somme SHA-256 de référence illisible pour $(basename "$1") (réponse inattendue du serveur) ; rien n'est installé"
	actual=$(sha256 "$1")
	[[ $actual == "$2" ]] || die "somme SHA-256 incorrecte pour $(basename "$1") (attendu $2, obtenu $actual) ; fichier rejeté"
	info "Somme SHA-256 vérifiée."
}

ask() { # prompt default -> REPLY
	local answer
	read -r -p "    $1${2:+ [$2]} : " answer || die "saisie interrompue"
	REPLY=${answer:-$2}
}

# --- System packages --------------------------------------------------------

PKG=
detect_package_manager() {
	local manager
	[[ -z $PKG ]] || return 0
	if [[ $OS == darwin ]]; then
		if command -v brew >/dev/null; then PKG=brew; fi
		return 0
	fi
	for manager in apt-get dnf yum pacman zypper apk; do
		if command -v "$manager" >/dev/null; then
			PKG=$manager
			return 0
		fi
	done
}

# package_for TOOL: package providing TOOL with the detected manager.
package_for() {
	case $PKG:$1 in
	*:sha256sum) echo coreutils ;;
	apt-get:xz) echo xz-utils ;;
	apt-get:psql | apk:psql) echo postgresql-client ;;
	brew:psql) echo libpq ;;
	*:psql) echo postgresql ;;
	apt-get:mysql) echo default-mysql-client ;;
	brew:mysql) echo mysql-client ;;
	pacman:mysql) echo mariadb-clients ;;
	dnf:mysql | yum:mysql) echo mariadb ;;
	*:mysql) echo mariadb-client ;;
	apt-get:aws | brew:aws | yum:aws) echo awscli ;;
	dnf:aws) echo awscli2 ;;
	pacman:aws) echo aws-cli-v2 ;;
	*:aws) echo aws-cli ;;
	apk:node) echo nodejs npm ;;
	*) echo "$1" ;;
	esac
}

has_ca_bundle() {
	[[ $OS == darwin ]] && return 0
	local file
	for file in /etc/ssl/certs/ca-certificates.crt /etc/pki/tls/certs/ca-bundle.crt /etc/ssl/cert.pem /etc/ssl/ca-bundle.pem; do
		[[ -s $file ]] && return 0
	done
	return 1
}

# install_tools REQUIRED REASON TOOL...: install TOOLs (commands, or the
# pseudo-tool ca-certificates) with the system package manager. A required
# failure stops the installer; an optional one only warns.
install_tools() {
	local required=$1 reason=$2 tool package packages=() commands=() sudo=() line missing=()
	shift 2
	fail() { if ((required)); then die "$1"; else warn "$1"; fi; }
	detect_package_manager
	if [[ -z $PKG ]]; then
		fail "$reason : $* introuvable(s) et aucun gestionnaire de paquets reconnu$([[ $OS == darwin ]] && echo ' (installe Homebrew : https://brew.sh)'). Installe-les puis relance ./install.sh"
		return 0
	fi
	for tool in "$@"; do
		for package in $(package_for "$tool"); do
			[[ " ${packages[*]} " == *" $package "* ]] || packages+=("$package")
		done
	done
	if [[ $PKG != brew && $EUID -ne 0 ]]; then
		if ! command -v sudo >/dev/null; then
			fail "$reason : $* manquant(s) ; installe en root : ${packages[*]}"
			return 0
		fi
		sudo=(sudo)
	fi
	case $PKG in
	apt-get) commands=("${sudo[*]} apt-get update -qq" "${sudo[*]} env DEBIAN_FRONTEND=noninteractive apt-get install -y -qq ${packages[*]}") ;;
	dnf | yum) commands=("${sudo[*]} $PKG install -y ${packages[*]}") ;;
	pacman) commands=("${sudo[*]} pacman -S --needed --noconfirm ${packages[*]}") ;;
	zypper) commands=("${sudo[*]} zypper --non-interactive install ${packages[*]}") ;;
	apk) commands=("${sudo[*]} apk add --no-cache ${packages[*]}") ;;
	brew)
		commands=("brew install ${packages[*]}")
		# Keg-only client formulas are not on PATH unless linked.
		for package in libpq mysql-client; do
			[[ " ${packages[*]} " == *" $package "* ]] && commands+=("brew link --force $package")
		done
		;;
	esac
	info "$reason : il manque $*."
	for line in "${commands[@]}"; do info "  ${line# }"; done
	if ((YES == 0)); then
		if [[ $MODE != never && -t 0 ]]; then
			ask "Lancer ces commandes maintenant ? (O/n)" O
			if [[ ! $REPLY =~ ^[oOyY] ]]; then
				fail "installation refusée : lance ces commandes toi-même puis relance ./install.sh"
				return 0
			fi
		else
			fail "lance ces commandes (ou relance avec --yes) puis relance ./install.sh"
			return 0
		fi
	fi
	for line in "${commands[@]}"; do
		bash -c "$line" || {
			fail "échec de : ${line# }"
			return 0
		}
	done
	hash -r
	for tool in "$@"; do
		if [[ $tool == ca-certificates ]]; then has_ca_bundle || missing+=("$tool"); else command -v "$tool" >/dev/null || missing+=("$tool"); fi
	done
	if ((${#missing[@]})); then
		fail "toujours introuvable(s) après installation : ${missing[*]}"
	else
		info "Installé : $*"
	fi
}

# --- Platform and prerequisites -------------------------------------------

step "Vérification du système"
case $(uname -s) in
Linux) OS=linux ;;
Darwin) OS=darwin ;;
*) die "système non pris en charge : $(uname -s). Sous Windows, lance install.cmd (voir README)." ;;
esac
case $(uname -m) in
x86_64 | amd64) GO_ARCH=amd64 NODE_ARCH=x64 ;;
aarch64 | arm64) GO_ARCH=arm64 NODE_ARCH=arm64 ;;
*) die "processeur non pris en charge : $(uname -m)" ;;
esac
MUSL=0
if [[ $OS == linux ]] && { ldd --version 2>&1 || true; } | grep -qi musl; then MUSL=1; fi
missing=()
for tool in curl tar gzip; do command -v "$tool" >/dev/null || missing+=("$tool"); done
command -v sha256sum >/dev/null || command -v shasum >/dev/null || missing+=("sha256sum")
has_ca_bundle || missing+=("ca-certificates")
if ((${#missing[@]})); then install_tools 1 "Outils de base" "${missing[@]}"; fi
info "$OS/$GO_ARCH, outils de base présents."

GO_MIN=$(sed -n 's/^go \([0-9.]*\).*/\1/p' go.mod)
NODE_MIN=$(sed -n 's/.*"node": *">=\([0-9.]*\)".*/\1/p' frontend/package.json)
[[ -n $GO_MIN && -n $NODE_MIN ]] || die "versions minimales introuvables dans go.mod ou frontend/package.json"

# --- Go ---------------------------------------------------------------------

go_version() { "$1" env GOVERSION 2>/dev/null | sed 's/^go//'; }

step "Go (version $GO_MIN ou plus)"
GO=
for candidate in "$(command -v go || true)" "$HOME/.local/go/bin/go" "$TOOLCHAIN_DIR/go-$GO_INSTALL_VERSION/bin/go"; do
	[[ -n $candidate && -x $candidate ]] || continue
	version=$(go_version "$candidate")
	if [[ -n $version ]] && version_ge "$version" "$GO_MIN"; then
		GO=$candidate
		break
	fi
	if [[ -n $version ]]; then info "Ignoré : $candidate ($version, trop ancien)"; fi
done
if [[ -z $GO ]]; then
	archive=go$GO_INSTALL_VERSION.$OS-$GO_ARCH.tar.gz
	target=$TOOLCHAIN_DIR/go-$GO_INSTALL_VERSION
	tmp=$(mktemp -d)
	trap 'rm -rf "$tmp"' EXIT
	download "https://dl.google.com/go/$archive.sha256" "$tmp/sum"
	download "https://dl.google.com/go/$archive" "$tmp/$archive"
	verify_checksum "$tmp/$archive" "$(tr -d '[:space:]' <"$tmp/sum")"
	mkdir -p "$tmp/x" "$TOOLCHAIN_DIR"
	tar -xzf "$tmp/$archive" -C "$tmp/x"
	rm -rf "$target" && mv "$tmp/x/go" "$target"
	GO=$target/bin/go
	info "Go installé dans $target"
fi
info "Go : $GO ($(go_version "$GO"))"
# Never let the go command download another toolchain behind our back.
export GOTOOLCHAIN=local

# --- Node -------------------------------------------------------------------

node_version() { "$1" --version 2>/dev/null | sed 's/^v//'; }

step "Node (version $NODE_MIN ou plus)"
NODE=
for candidate in "$(command -v node || true)" "$TOOLCHAIN_DIR/node-$NODE_INSTALL_VERSION/bin/node"; do
	[[ -n $candidate && -x $candidate ]] || continue
	version=$(node_version "$candidate")
	if [[ -n $version ]] && version_ge "$version" "$NODE_MIN" && [[ -x $(dirname "$candidate")/npm ]]; then
		NODE=$candidate
		break
	fi
	if [[ -n $version ]]; then info "Ignoré : $candidate ($version, trop ancien ou sans npm)"; fi
done
if [[ -z $NODE && $MUSL == 1 ]]; then
	# Official Node builds need glibc; musl systems (Alpine) use their package.
	install_tools 1 "Node (système musl)" node
	NODE=$(command -v node)
	version=$(node_version "$NODE")
	version_ge "${version:-0}" "$NODE_MIN" && command -v npm >/dev/null ||
		die "le paquet Node du système ($version) est trop ancien ou sans npm : Node $NODE_MIN+ requis. Mets le système à jour ou utilise l'image Docker."
fi
if [[ -z $NODE ]]; then
	name=node-v$NODE_INSTALL_VERSION-$OS-$NODE_ARCH
	target=$TOOLCHAIN_DIR/node-$NODE_INSTALL_VERSION
	tmp=${tmp:-$(mktemp -d)}
	trap 'rm -rf "$tmp"' EXIT
	download "https://nodejs.org/dist/v$NODE_INSTALL_VERSION/SHASUMS256.txt" "$tmp/SHASUMS256.txt"
	download "https://nodejs.org/dist/v$NODE_INSTALL_VERSION/$name.tar.gz" "$tmp/$name.tar.gz"
	expected=$(awk -v f="$name.tar.gz" '$2 == f {print $1}' "$tmp/SHASUMS256.txt")
	[[ -n $expected ]] || die "somme SHA-256 introuvable pour $name.tar.gz"
	verify_checksum "$tmp/$name.tar.gz" "$expected"
	mkdir -p "$tmp/n" "$TOOLCHAIN_DIR"
	tar -xzf "$tmp/$name.tar.gz" -C "$tmp/n"
	rm -rf "$target" && mv "$tmp/n/$name" "$target"
	NODE=$target/bin/node
	[[ -n $(node_version "$NODE") ]] || die "Node téléchargé ne démarre pas sur ce système ($target/bin/node --version échoue). Utilise l'image Docker (voir README)."
	info "Node installé dans $target"
fi
info "Node : $NODE ($(node_version "$NODE"))"
# npm scripts need node on PATH; this only affects the installer's own process.
export PATH="$(dirname "$NODE"):$PATH"

# --- Build ------------------------------------------------------------------

step "Compilation"
stale=
if ((REBUILD == 0)) && [[ -x $BINARY ]]; then
	stale=$(find go.mod go.sum cmd internal web frontend/src frontend/index.html frontend/package-lock.json \
		-path web/dist -prune -o -newer "$BINARY" -type f -print 2>/dev/null | head -1) || true
fi
if ((REBUILD == 0)) && [[ -x $BINARY && -z $stale ]]; then
	info "Binaire à jour : $BINARY (--rebuild pour forcer)"
else
	info "Dépendances de l'interface (npm ci)…"
	npm ci --prefix frontend --no-audit --no-fund --loglevel=error || die "npm ci a échoué (voir ci-dessus)"
	info "Compilation de l'interface…"
	npm run build --prefix frontend --silent || die "la compilation de l'interface a échoué (voir ci-dessus)"
	info "Compilation du binaire Go…"
	"$GO" build -trimpath -o "$BINARY" ./cmd/sulcata || die "go build a échoué (voir ci-dessus)"
	info "Binaire prêt : $BINARY ($("$BINARY" version 2>/dev/null || echo 'version inconnue'))"
fi

# --- Configuration ----------------------------------------------------------

toml_string() { # escape for a TOML basic string
	local s=${1//\\/\\\\}
	printf '"%s"' "${s//\"/\\\"}"
}

ask_required() {
	while :; do
		ask "$1" "${2:-}"
		[[ -n $REPLY ]] && return
		warn "valeur obligatoire"
	done
}

configure() {
	local driver host port database user password schema label id env_var writes default_id
	info "Première connexion (les autres s'ajoutent ensuite dans l'interface, page Connexions)."
	while :; do
		ask "Type de base (postgresql ou mysql)" postgresql
		driver=$REPLY
		[[ $driver == postgresql || $driver == mysql ]] && break
		warn "réponds postgresql ou mysql"
	done
	ask_required "Hôte" 127.0.0.1
	host=$REPLY
	while :; do
		ask "Port" "$([[ $driver == mysql ]] && echo 3306 || echo 5432)"
		port=$REPLY
		[[ $port =~ ^[0-9]+$ ]] && ((port >= 1 && port <= 65535)) && break
		warn "port entre 1 et 65535"
	done
	ask_required "Nom de la base"
	database=$REPLY
	ask_required "Utilisateur (idéalement en lecture seule)"
	user=$REPLY
	read -r -s -p "    Mot de passe (masqué, vide si aucun) : " password || die "saisie interrompue"
	echo
	[[ $password != *$'\n'* ]] || die "mot de passe sur plusieurs lignes non pris en charge"
	ask "Schéma par défaut" "$([[ $driver == mysql ]] && echo "$database" || echo public)"
	schema=$REPLY
	ask "Libellé affiché" "$database"
	label=$REPLY
	default_id=$(printf '%s' "$database" | tr '[:upper:]' '[:lower:]' | tr -c 'a-z0-9_-' '-' | sed 's/^[^a-z]*//' | cut -c1-64)
	case $default_id in "" | sql | events | data | api) default_id=db ;; esac
	while :; do
		ask "Identifiant (minuscules, définitif)" "$default_id"
		id=$REPLY
		[[ $id =~ ^[a-z][a-z0-9_-]{0,63}$ ]] && [[ ! $id =~ ^(sql|events|data|api)$ ]] && break
		warn "lettre minuscule puis lettres, chiffres, _ ou - (64 max) ; sql, events, data et api sont réservés"
	done
	ask "Autoriser les écritures ? (o/N)" N
	writes=false
	if [[ $REPLY =~ ^[oOyY] ]]; then writes=true; fi
	env_var=SULCATA_$(printf '%s' "$id" | tr '[:lower:]-' '[:upper:]_')_PASSWORD

	local hosts='"127.0.0.1", "localhost"'
	[[ $host == 127.0.0.1 || $host == localhost ]] || hosts="$hosts, $(toml_string "$host")"
	local tmpconf
	tmpconf=$(mktemp "$CONFIG.XXXXXX")
	cat >"$tmpconf" <<EOF
# Generated by install.sh. Passwords live in .env, never here.
# Full reference: sulcata.example.toml and README.md.
[server]
bind = "127.0.0.1"
port = 8765
# Database hosts the server may connect to.
allowed_db_hosts = [$hosts]
data_dir = ".local/data"

[[connections]]
id = $(toml_string "$id")
label = $(toml_string "$label")
driver = "$driver"
host = $(toml_string "$host")
port = $port
database = $(toml_string "$database")
user = $(toml_string "$user")
password_env = "$env_var"
default_schema = $(toml_string "$schema")
color = "#8ab4f8"
allow_writes = $writes
EOF
	mv "$tmpconf" "$CONFIG"
	info "Configuration écrite : $CONFIG"

	local envfile
	envfile=$(dirname "$CONFIG")/.env
	if [[ -f $envfile ]] && grep -Eq "^(export[[:space:]]+)?$env_var[[:space:]]*=" "$envfile"; then
		warn "$env_var existe déjà dans $envfile : valeur existante conservée"
	else
		(
			umask 077
			[[ -f $envfile ]] || : >"$envfile"
		)
		# Single quotes: the .env parser strips them and expands nothing inside.
		printf "%s='%s'\n" "$env_var" "$password" >>"$envfile"
		chmod 600 "$envfile"
		info "Mot de passe enregistré dans $envfile (droits 600) sous $env_var"
	fi
}

step "Configuration"
CONFIGURED=1
if [[ -e $CONFIG || -L $CONFIG ]]; then
	info "Configuration existante conservée : $CONFIG"
elif [[ $MODE == always || ($MODE == auto && -t 0) ]]; then
	configure
else
	cp sulcata.example.toml "$CONFIG"
	[[ -e .env ]] || (umask 077 && cp .env.example .env)
	CONFIGURED=0
	warn "mode non interactif : exemples copiés dans $CONFIG et .env. Renseigne ta base puis relance ./install.sh"
fi

# --- Tools used by backup sources -------------------------------------------

# S3 sources call the AWS CLI; restore_command is a trusted shell command whose
# usual client and decompression programs are detected by name (heuristic:
# single-line restore_command values only).
if ((CONFIGURED == 1)); then
	step "Outils des sauvegardes configurées"
	needed=()
	if grep -Eq '^[^#]*s3-versioned' "$CONFIG" && ! command -v aws >/dev/null; then needed+=(aws); fi
	restore=$(grep -E '^[[:space:]]*restore_command[[:space:]]*=' "$CONFIG" || true)
	for pair in psql:psql pg_restore:psql mysql:mysql gzip:gzip gunzip:gzip zcat:gzip bzip2:bzip2 bunzip2:bzip2 bzcat:bzip2 \
		xz:xz unxz:xz xzcat:xz zstd:zstd unzstd:zstd zstdcat:zstd unzip:unzip; do
		word=${pair%%:*} tool=${pair#*:}
		if grep -Eq "(^|[^A-Za-z0-9_.-])$word([^A-Za-z0-9_.-]|$)" <<<"$restore" && ! command -v "$word" >/dev/null &&
			[[ " ${needed[*]} " != *" $tool "* ]]; then
			needed+=("$tool")
		fi
	done
	if ((${#needed[@]})); then
		install_tools 0 "Sauvegardes" "${needed[@]}"
	else
		info "Rien à installer pour les sauvegardes configurées."
	fi
	if [[ " ${needed[*]} " == *" aws "* ]] && command -v aws >/dev/null; then
		info "Pense à configurer l'accès S3 (aws configure, ou ton profil habituel) avant de lister les sauvegardes."
	fi
fi

# --- Check ------------------------------------------------------------------

if ((CONFIGURED == 1)); then
	step "Test des connexions (lecture seule)"
	if ! "$BINARY" check --config "$CONFIG"; then
		die "au moins une connexion échoue. Corrige $CONFIG (hôte, port, base, utilisateur, allowed_db_hosts) ou le mot de passe dans .env, puis relance ./install.sh"
	fi
fi

step "Installation terminée"
if ((CONFIGURED == 0)); then
	info "Il reste à décrire ta base dans $CONFIG et son mot de passe dans .env."
	exit 0
fi
info "Lancer : bin/sulcata --config $(basename "$CONFIG")"
info "Puis ouvrir http://localhost:8765 (ou le port de [server] dans la configuration)"
info "Après avoir ajouté une source de sauvegarde, relance ./install.sh pour installer ses outils."
if ((START == 0)) && [[ $MODE != never && -t 0 ]]; then
	ask "Démarrer maintenant ? (O/n)" O
	if [[ $REPLY =~ ^[oOyY] ]]; then START=1; fi
fi
if ((START == 1)); then
	step "Démarrage (Ctrl+C pour arrêter)"
	exec "$BINARY" --config "$CONFIG"
fi
