<#
.SYNOPSIS
Atlas Schema Desk installer for Windows: from a fresh clone to a running, verified server.

.DESCRIPTION
Installs everything Atlas needs to run, and nothing for development:
 - Go and Node (build only) go to $env:ATLAS_TOOLCHAIN_DIR (default
   %LOCALAPPDATA%\atlas-toolchain), checksum-verified, without administrator rights;
 - tools used by configured backup sources (S3 CLI, psql, mysql) come from winget,
   after confirmation.
Re-runnable: every step skips what is already in place. PATH and the PowerShell
profile are not modified. Existing atlas.toml and .env files are never overwritten.
Runs on Windows PowerShell 5.1 and PowerShell 7 (also on Linux and macOS, where
install.sh is the recommended installer).

.EXAMPLE
.\install.cmd                    # same as install.ps1, without changing the execution policy
.\install.ps1 -Start             # ...then start the server
.\install.ps1 -NonInteractive    # never prompt; copy the example configuration instead
#>
[CmdletBinding()]
param(
	[switch]$Start,          # start the server at the end
	[switch]$Rebuild,        # rebuild even if the binary is up to date
	[switch]$NonInteractive, # never prompt
	[switch]$Configure,      # run the configuration prompts even when input is redirected
	[switch]$Yes             # install missing backup tools without asking
)
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue' # Invoke-WebRequest is very slow with a progress bar
Set-StrictMode -Version 2
Set-Location -LiteralPath $PSScriptRoot
$Root = $PSScriptRoot

# Versions installed when no suitable toolchain is found. Minimums come from
# go.mod and frontend/package.json so they cannot drift from the build.
$GoInstallVersion = '1.27.4'
$NodeInstallVersion = '24.11.0'

$OnWindows = [IO.Path]::DirectorySeparatorChar -eq '\'
$Exe = ''
if ($OnWindows) { $Exe = '.exe' }
if ($env:ATLAS_TOOLCHAIN_DIR) { $ToolchainDir = $env:ATLAS_TOOLCHAIN_DIR }
elseif ($OnWindows) { $ToolchainDir = Join-Path $env:LOCALAPPDATA 'atlas-toolchain' }
else { $ToolchainDir = Join-Path $HOME '.local/atlas-toolchain' }
if ($env:ATLAS_CONFIG) { $Config = $env:ATLAS_CONFIG } else { $Config = Join-Path $Root 'atlas.toml' }
$Binary = Join-Path $Root ('bin/atlas-schema-desk' + $Exe)
$Redirected = [Console]::IsInputRedirected
$CanPrompt = (-not $NonInteractive) -and ($Configure -or -not $Redirected)
$Utf8 = New-Object Text.UTF8Encoding $false

function Step($Text) { Write-Host ''; Write-Host "==> $Text" -ForegroundColor White }
function Info($Text) { Write-Host "    $Text" }
function Warn($Text) { Write-Host "    Attention : $Text" -ForegroundColor Yellow }
function Fail($Text) {
	Write-Host ''
	Write-Host "Échec : $Text" -ForegroundColor Red
	exit 1
}

function Read-Answer($Prompt, $Default) {
	$label = "    $Prompt"
	if ($Default) { $label += " [$Default]" }
	if ($Redirected) {
		Write-Host -NoNewline "$label : "
		$answer = [Console]::In.ReadLine()
		if ($null -eq $answer) { Fail 'saisie interrompue' }
		Write-Host ''
	} else {
		$answer = Read-Host $label
	}
	if ([string]::IsNullOrEmpty($answer)) { return $Default }
	return $answer
}

function Read-Secret($Prompt) {
	if ($Redirected) {
		Write-Host -NoNewline "    $Prompt : "
		$answer = [Console]::In.ReadLine()
		Write-Host ''
		if ($null -eq $answer) { return '' }
		return $answer
	}
	$secure = Read-Host "    $Prompt" -AsSecureString
	$pointer = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($secure)
	try { return [Runtime.InteropServices.Marshal]::PtrToStringBSTR($pointer) }
	finally { [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($pointer) }
}

function Test-Yes($Answer) { return $Answer -match '^[oOyY]' }

function Test-VersionAtLeast($Actual, $Minimum) {
	$parse = {
		param($v)
		$v = ($v -replace '[^0-9.].*$', '')
		if ($v -notmatch '\.') { $v += '.0' }
		return [version]$v
	}
	try { return (& $parse $Actual) -ge (& $parse $Minimum) } catch { return $false }
}

function Save-Download($Url, $Destination) {
	Info "Téléchargement de $Url"
	try { Invoke-WebRequest -Uri $Url -OutFile $Destination -UseBasicParsing }
	catch { Fail "téléchargement impossible : $Url ($($_.Exception.Message)). Vérifie l'accès réseau ou un proxy." }
}

function Assert-Checksum($File, $Expected) {
	$Expected = "$Expected".Trim().ToLower()
	if ($Expected -notmatch '^[0-9a-f]{64}$') { Fail "somme SHA-256 de référence illisible pour $(Split-Path -Leaf $File) (réponse inattendue du serveur) ; rien n'est installé" }
	$actual = (Get-FileHash -Algorithm SHA256 -LiteralPath $File).Hash.ToLower()
	if ($actual -ne $Expected) { Fail "somme SHA-256 incorrecte pour $(Split-Path -Leaf $File) (attendu $Expected, obtenu $actual) ; fichier rejeté" }
	Info 'Somme SHA-256 vérifiée.'
}

function Expand-Download($Archive, $Destination) {
	New-Item -ItemType Directory -Force -Path $Destination | Out-Null
	if ($Archive.EndsWith('.zip')) {
		# tar.exe (Windows 10 1803+) is much faster than Expand-Archive on Node's many files.
		if ($OnWindows -and (Get-Command tar.exe -ErrorAction SilentlyContinue)) {
			& tar.exe -xf $Archive -C $Destination
			if ($LASTEXITCODE -ne 0) { Fail "décompression impossible : $Archive" }
		} else {
			Expand-Archive -LiteralPath $Archive -DestinationPath $Destination -Force
		}
	} else {
		& tar -xzf $Archive -C $Destination
		if ($LASTEXITCODE -ne 0) { Fail "décompression impossible : $Archive" }
	}
}

function New-TempDirectory {
	$path = Join-Path ([IO.Path]::GetTempPath()) ('atlas-install-' + [Guid]::NewGuid().ToString('N'))
	New-Item -ItemType Directory -Path $path | Out-Null
	return $path
}

# --- Platform -----------------------------------------------------------------

Step 'Vérification du système'
if ($OnWindows) {
	$OsName = 'windows'; $NodeOs = 'win'; $ArchiveExt = '.zip'
	$cpu = $env:PROCESSOR_ARCHITEW6432
	if (-not $cpu) { $cpu = $env:PROCESSOR_ARCHITECTURE }
} else {
	if ($IsMacOS) { $OsName = 'darwin' } else { $OsName = 'linux' }
	$NodeOs = $OsName; $ArchiveExt = '.tar.gz'
	$cpu = (& uname -m)
}
switch -Regex ($cpu) {
	'^(AMD64|x86_64|amd64)$' { $GoArch = 'amd64'; $NodeArch = 'x64' }
	'^(ARM64|aarch64|arm64)$' { $GoArch = 'arm64'; $NodeArch = 'arm64' }
	default { Fail "processeur non pris en charge : $cpu (Windows 64 bits requis)" }
}
Info "$OsName/$GoArch, PowerShell $($PSVersionTable.PSVersion)"
if (-not $OnWindows) { Warn 'hors Windows, ./install.sh est l''installeur recommandé (il installe aussi les paquets système).' }
if ($OnWindows -and $PSVersionTable.PSVersion.Major -lt 6) {
	# Windows PowerShell 5.1 may default to TLS 1.0, which the download sites refuse.
	[Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
}

$GoMin = ((Get-Content go.mod) -match '^go [0-9.]+' | Select-Object -First 1) -replace '^go ([0-9.]+).*$', '$1'
$NodeMin = ((Get-Content frontend/package.json -Raw) -replace '(?s).*"node":\s*">=([0-9.]+)".*', '$1')
if (-not ($GoMin -match '^[0-9.]+$' -and $NodeMin -match '^[0-9.]+$')) { Fail 'versions minimales introuvables dans go.mod ou frontend/package.json' }

# --- Go -----------------------------------------------------------------------

function Get-GoVersion($Path) {
	try { $v = & $Path env GOVERSION 2>$null; if ($LASTEXITCODE -eq 0) { return ("$v".Trim() -replace '^go', '') } } catch {}
	return ''
}

Step "Go (version $GoMin ou plus)"
$Go = $null
$candidates = @()
$found = Get-Command go -ErrorAction SilentlyContinue
if ($found) { $candidates += $found.Source }
if (-not $OnWindows) { $candidates += (Join-Path $HOME '.local/go/bin/go') }
$candidates += (Join-Path $ToolchainDir "go-$GoInstallVersion/bin/go$Exe")
foreach ($candidate in $candidates) {
	if (-not (Test-Path -LiteralPath $candidate -PathType Leaf)) { continue }
	$version = Get-GoVersion $candidate
	if ($version -and (Test-VersionAtLeast $version $GoMin)) { $Go = $candidate; break }
	if ($version) { Info "Ignoré : $candidate ($version, trop ancien)" }
}
if (-not $Go) {
	$archive = "go$GoInstallVersion.$OsName-$GoArch$ArchiveExt"
	$target = Join-Path $ToolchainDir "go-$GoInstallVersion"
	$tmp = New-TempDirectory
	try {
		Save-Download "https://dl.google.com/go/$archive.sha256" (Join-Path $tmp 'sum')
		Save-Download "https://dl.google.com/go/$archive" (Join-Path $tmp $archive)
		Assert-Checksum (Join-Path $tmp $archive) (Get-Content -Raw (Join-Path $tmp 'sum'))
		Expand-Download (Join-Path $tmp $archive) (Join-Path $tmp 'x')
		New-Item -ItemType Directory -Force -Path $ToolchainDir | Out-Null
		if (Test-Path -LiteralPath $target) { Remove-Item -Recurse -Force -LiteralPath $target }
		Move-Item -LiteralPath (Join-Path $tmp 'x/go') -Destination $target
	} finally { Remove-Item -Recurse -Force -LiteralPath $tmp -ErrorAction SilentlyContinue }
	$Go = Join-Path $target "bin/go$Exe"
	Info "Go installé dans $target"
}
Info "Go : $Go ($(Get-GoVersion $Go))"
# Never let the go command download another toolchain behind our back.
$env:GOTOOLCHAIN = 'local'

# --- Node ---------------------------------------------------------------------

function Get-NodeVersion($Path) {
	try { $v = & $Path --version 2>$null; if ($LASTEXITCODE -eq 0) { return ("$v".Trim() -replace '^v', '') } } catch {}
	return ''
}
function Get-NpmPath($NodePath) {
	$name = 'npm'
	if ($OnWindows) { $name = 'npm.cmd' }
	return (Join-Path (Split-Path -Parent $NodePath) $name)
}

Step "Node (version $NodeMin ou plus)"
$Node = $null
$nodeName = "node-v$NodeInstallVersion-$NodeOs-$NodeArch"
$nodeTarget = Join-Path $ToolchainDir "node-$NodeInstallVersion"
$candidates = @()
$found = Get-Command node -ErrorAction SilentlyContinue
if ($found) { $candidates += $found.Source }
if ($OnWindows) { $candidates += (Join-Path $nodeTarget 'node.exe') } else { $candidates += (Join-Path $nodeTarget 'bin/node') }
foreach ($candidate in $candidates) {
	if (-not (Test-Path -LiteralPath $candidate -PathType Leaf)) { continue }
	$version = Get-NodeVersion $candidate
	if ($version -and (Test-VersionAtLeast $version $NodeMin) -and (Test-Path -LiteralPath (Get-NpmPath $candidate))) { $Node = $candidate; break }
	if ($version) { Info "Ignoré : $candidate ($version, trop ancien ou sans npm)" }
}
if (-not $Node) {
	$archive = "$nodeName$ArchiveExt"
	$tmp = New-TempDirectory
	try {
		Save-Download "https://nodejs.org/dist/v$NodeInstallVersion/SHASUMS256.txt" (Join-Path $tmp 'SHASUMS256.txt')
		Save-Download "https://nodejs.org/dist/v$NodeInstallVersion/$archive" (Join-Path $tmp $archive)
		$line = Get-Content (Join-Path $tmp 'SHASUMS256.txt') | Where-Object { $_ -match "^[0-9a-f]{64}\s+$([regex]::Escape($archive))$" } | Select-Object -First 1
		if (-not $line) { Fail "somme SHA-256 introuvable pour $archive" }
		Assert-Checksum (Join-Path $tmp $archive) (($line -split '\s+')[0])
		Expand-Download (Join-Path $tmp $archive) (Join-Path $tmp 'n')
		New-Item -ItemType Directory -Force -Path $ToolchainDir | Out-Null
		if (Test-Path -LiteralPath $nodeTarget) { Remove-Item -Recurse -Force -LiteralPath $nodeTarget }
		Move-Item -LiteralPath (Join-Path $tmp "n/$nodeName") -Destination $nodeTarget
	} finally { Remove-Item -Recurse -Force -LiteralPath $tmp -ErrorAction SilentlyContinue }
	if ($OnWindows) { $Node = Join-Path $nodeTarget 'node.exe' } else { $Node = Join-Path $nodeTarget 'bin/node' }
	if (-not (Get-NodeVersion $Node)) { Fail "Node téléchargé ne démarre pas sur ce système ($Node --version échoue)." }
	Info "Node installé dans $nodeTarget"
}
Info "Node : $Node ($(Get-NodeVersion $Node))"
$Npm = Get-NpmPath $Node
# npm scripts need node on PATH; this only affects the installer's own process.
$env:PATH = (Split-Path -Parent $Node) + [IO.Path]::PathSeparator + $env:PATH

# --- Build --------------------------------------------------------------------

Step 'Compilation'
$stale = $true
if (-not $Rebuild -and (Test-Path -LiteralPath $Binary)) {
	$built = (Get-Item -LiteralPath $Binary).LastWriteTimeUtc
	$sources = @('go.mod', 'go.sum', 'cmd', 'internal', 'web', 'frontend/src', 'frontend/index.html', 'frontend/package-lock.json') |
		Where-Object { Test-Path $_ } | ForEach-Object { Get-ChildItem -LiteralPath $_ -Recurse -File }
	$newer = $sources | Where-Object {
		$_.FullName.Substring($Root.Length) -notmatch '^[\\/]web[\\/]dist[\\/]' -and $_.LastWriteTimeUtc -gt $built
	} | Select-Object -First 1
	$stale = [bool]$newer
}
if (-not $stale) {
	Info "Binaire à jour : $Binary (-Rebuild pour forcer)"
} else {
	Info "Dépendances de l'interface (npm ci)…"
	& $Npm ci --prefix frontend --no-audit --no-fund --loglevel=error
	if ($LASTEXITCODE -ne 0) { Fail 'npm ci a échoué (voir ci-dessus)' }
	Info "Compilation de l'interface…"
	& $Npm run build --prefix frontend --silent
	if ($LASTEXITCODE -ne 0) { Fail "la compilation de l'interface a échoué (voir ci-dessus)" }
	Info 'Compilation du binaire Go…'
	& $Go build -trimpath -o $Binary ./cmd/atlas-schema-desk
	if ($LASTEXITCODE -ne 0) { Fail 'go build a échoué (voir ci-dessus)' }
	Info "Binaire prêt : $Binary ($(& $Binary version))"
}

# --- Configuration ------------------------------------------------------------

function ConvertTo-TomlString($Value) {
	return '"' + ($Value -replace '\\', '\\' -replace '"', '\"') + '"'
}

function New-Configuration {
	Info "Première connexion (les autres s'ajoutent ensuite dans l'interface, page Connexions)."
	do { $driver = Read-Answer 'Type de base (postgresql ou mysql)' 'postgresql' } while ($driver -notin @('postgresql', 'mysql'))
	$dbHost = ''
	while (-not $dbHost) { $dbHost = Read-Answer 'Hôte' '127.0.0.1' }
	$defaultPort = '5432'
	if ($driver -eq 'mysql') { $defaultPort = '3306' }
	do {
		$port = Read-Answer 'Port' $defaultPort
		$valid = $port -match '^[0-9]+$' -and [int]$port -ge 1 -and [int]$port -le 65535
		if (-not $valid) { Warn 'port entre 1 et 65535' }
	} while (-not $valid)
	$database = ''
	while (-not $database) { $database = Read-Answer 'Nom de la base' '' }
	$user = ''
	while (-not $user) { $user = Read-Answer 'Utilisateur (idéalement en lecture seule)' '' }
	$password = Read-Secret 'Mot de passe (masqué, vide si aucun)'
	if ($password -match "[`r`n]") { Fail 'mot de passe sur plusieurs lignes non pris en charge' }
	$defaultSchema = 'public'
	if ($driver -eq 'mysql') { $defaultSchema = $database }
	$schema = Read-Answer 'Schéma par défaut' $defaultSchema
	$label = Read-Answer 'Libellé affiché' $database
	$defaultId = ($database.ToLower() -replace '[^a-z0-9_-]', '-') -replace '^[^a-z]+', ''
	if ($defaultId.Length -gt 64) { $defaultId = $defaultId.Substring(0, 64) }
	if ($defaultId -in @('', 'sql', 'events', 'data', 'api')) { $defaultId = 'db' }
	do {
		$id = Read-Answer 'Identifiant (minuscules, définitif)' $defaultId
		$valid = $id -cmatch '^[a-z][a-z0-9_-]{0,63}$' -and $id -notin @('sql', 'events', 'data', 'api')
		if (-not $valid) { Warn 'lettre minuscule puis lettres, chiffres, _ ou - (64 max) ; sql, events, data et api sont réservés' }
	} while (-not $valid)
	$writes = 'false'
	if (Test-Yes (Read-Answer 'Autoriser les écritures ? (o/N)' 'N')) { $writes = 'true' }
	$envVar = 'ATLAS_' + ($id.ToUpper() -replace '-', '_') + '_PASSWORD'

	$hosts = '"127.0.0.1", "localhost"'
	if ($dbHost -notin @('127.0.0.1', 'localhost')) { $hosts += ', ' + (ConvertTo-TomlString $dbHost) }
	$lines = @(
		'# Generated by install.ps1. Passwords live in .env, never here.',
		'# Full reference: atlas.example.toml and README.md.',
		'[server]',
		'bind = "127.0.0.1"',
		'port = 8765',
		'# Database hosts the server may connect to.',
		"allowed_db_hosts = [$hosts]",
		'data_dir = ".local/data"',
		'',
		'[[connections]]',
		"id = $(ConvertTo-TomlString $id)",
		"label = $(ConvertTo-TomlString $label)",
		"driver = `"$driver`"",
		"host = $(ConvertTo-TomlString $dbHost)",
		"port = $port",
		"database = $(ConvertTo-TomlString $database)",
		"user = $(ConvertTo-TomlString $user)",
		"password_env = `"$envVar`"",
		"default_schema = $(ConvertTo-TomlString $schema)",
		'color = "#8ab4f8"',
		"allow_writes = $writes"
	)
	$tmpConfig = "$Config.tmp-$PID"
	[IO.File]::WriteAllText($tmpConfig, (($lines -join "`n") + "`n"), $Utf8)
	Move-Item -LiteralPath $tmpConfig -Destination $Config
	Info "Configuration écrite : $Config"

	$envFile = Join-Path (Split-Path -Parent $Config) '.env'
	$existing = ''
	if (Test-Path -LiteralPath $envFile) { $existing = [IO.File]::ReadAllText($envFile) }
	if ($existing -match "(?m)^(export\s+)?$envVar\s*=") {
		Warn "$envVar existe déjà dans $envFile : valeur existante conservée"
	} else {
		$prefix = ''
		if ($existing -and -not $existing.EndsWith("`n")) { $prefix = "`n" }
		# Single quotes: the .env parser strips them and expands nothing inside.
		[IO.File]::AppendAllText($envFile, "$prefix$envVar='$password'`n", $Utf8)
		if (-not $OnWindows) { & chmod 600 $envFile }
		Info "Mot de passe enregistré dans $envFile sous $envVar"
	}
}

Step 'Configuration'
$Configured = $true
if (Test-Path -LiteralPath $Config) {
	Info "Configuration existante conservée : $Config"
} elseif ($CanPrompt) {
	New-Configuration
} else {
	Copy-Item atlas.example.toml $Config
	if (-not (Test-Path -LiteralPath (Join-Path $Root '.env'))) { Copy-Item (Join-Path $Root '.env.example') (Join-Path $Root '.env') }
	$Configured = $false
	Warn "mode non interactif : exemples copiés dans $Config et .env. Renseigne ta base puis relance l'installeur."
}

# --- Tools used by backup sources ---------------------------------------------

# S3 sources call the AWS CLI; restore_command (run by cmd.exe on Windows) is
# scanned for the usual database clients by name (single-line values only).
$WingetPackages = @{ aws = 'Amazon.AWSCLI'; psql = 'PostgreSQL.PostgreSQL.17'; mysql = 'Oracle.MySQL' }
$KnownBinDirs = @{ psql = 'C:\Program Files\PostgreSQL\*\bin'; mysql = 'C:\Program Files\MySQL\MySQL Server*\bin' }

function Update-SessionPath {
	if ($OnWindows) {
		$env:PATH = [Environment]::GetEnvironmentVariable('Path', 'Machine') + ';' + [Environment]::GetEnvironmentVariable('Path', 'User') + ';' + $env:PATH
	}
}

if ($Configured) {
	Step 'Outils des sauvegardes configurées'
	$text = Get-Content -LiteralPath $Config
	$needed = @()
	if (($text | Where-Object { $_ -match '^[^#]*s3-versioned' }) -and -not (Get-Command aws -ErrorAction SilentlyContinue)) { $needed += 'aws' }
	$restore = ($text | Where-Object { $_ -match '^\s*restore_command\s*=' }) -join "`n"
	foreach ($pair in @('psql:psql', 'pg_restore:psql', 'mysql:mysql')) {
		$word, $tool = $pair -split ':'
		if ($restore -match "(^|[^A-Za-z0-9_.-])$word([^A-Za-z0-9_.-]|$)" -and -not (Get-Command $word -ErrorAction SilentlyContinue) -and $tool -notin $needed) { $needed += $tool }
	}
	foreach ($word in @('gzip', 'gunzip', 'zcat', 'xz', 'zstd', 'bzip2')) {
		if ($restore -match "(^|[^A-Za-z0-9_.-])$word([^A-Za-z0-9_.-]|$)" -and -not (Get-Command $word -ErrorAction SilentlyContinue)) {
			Warn "restore_command utilise $word, introuvable : installe-le ou adapte la commande (sous Windows, elle est exécutée par cmd.exe)."
		}
	}
	if (-not $needed) {
		Info 'Rien à installer pour les sauvegardes configurées.'
	} elseif (-not $OnWindows) {
		Warn "il manque $($needed -join ', ') : lance ./install.sh, qui passe par le gestionnaire de paquets du système."
	} elseif (-not (Get-Command winget -ErrorAction SilentlyContinue)) {
		Warn "il manque $($needed -join ', ') et winget est absent (installe « App Installer » depuis le Microsoft Store), puis relance l'installeur."
	} else {
		Info "Sauvegardes : il manque $($needed -join ', ')."
		foreach ($tool in $needed) { Info "  winget install --id $($WingetPackages[$tool]) -e" }
		# Not $go: PowerShell variable names are case-insensitive and $Go holds the Go path.
		$proceed = [bool]$Yes
		if (-not $proceed -and $CanPrompt) { $proceed = Test-Yes (Read-Answer 'Lancer ces commandes maintenant ? (O/n)' 'O') }
		if (-not $proceed) {
			Warn "lance ces commandes (ou relance avec -Yes) puis relance l'installeur."
		} else {
			foreach ($tool in $needed) {
				& winget install --id $WingetPackages[$tool] -e --accept-source-agreements --accept-package-agreements
				if ($LASTEXITCODE -ne 0) { Warn "échec de winget pour $($WingetPackages[$tool])" }
			}
			Update-SessionPath
			foreach ($tool in $needed) {
				if (Get-Command $tool -ErrorAction SilentlyContinue) { Info "Installé : $tool"; continue }
				$dir = $null
				if ($KnownBinDirs.ContainsKey($tool)) { $dir = Get-ChildItem -Path $KnownBinDirs[$tool] -Directory -ErrorAction SilentlyContinue | Select-Object -Last 1 }
				if ($dir) { Warn "$tool est installé dans $($dir.FullName) mais pas dans le PATH : ajoute ce dossier au PATH (ou écris le chemin complet dans restore_command)." }
				else { Warn "$tool toujours introuvable après installation." }
			}
			if ($needed -contains 'aws' -and (Get-Command aws -ErrorAction SilentlyContinue)) {
				Info "Pense à configurer l'accès S3 (aws configure, ou ton profil habituel) avant de lister les sauvegardes."
			}
		}
	}
}

# --- Check --------------------------------------------------------------------

if ($Configured) {
	Step 'Test des connexions (lecture seule)'
	& $Binary check --config $Config
	if ($LASTEXITCODE -ne 0) {
		Fail "au moins une connexion échoue. Corrige $Config (hôte, port, base, utilisateur, allowed_db_hosts) ou le mot de passe dans .env, puis relance l'installeur."
	}
}

Step 'Installation terminée'
if (-not $Configured) {
	Info "Il reste à décrire ta base dans $Config et son mot de passe dans .env."
	exit 0
}
$relative = 'bin/atlas-schema-desk'
if ($OnWindows) { $relative = 'bin\atlas-schema-desk.exe' }
Info "Lancer : $relative --config $(Split-Path -Leaf $Config)"
Info 'Puis ouvrir http://localhost:8765 (ou le port de [server] dans la configuration)'
Info "Après avoir ajouté une source de sauvegarde, relance l'installeur pour installer ses outils."
if (-not $Start -and $CanPrompt -and -not $Redirected) { $Start = Test-Yes (Read-Answer 'Démarrer maintenant ? (O/n)' 'O') }
if ($Start) {
	Step 'Démarrage (Ctrl+C pour arrêter)'
	& $Binary --config $Config
	exit $LASTEXITCODE
}
