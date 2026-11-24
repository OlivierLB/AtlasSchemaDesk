<p align="center"><img src="packaging/sulcata-logo.svg" alt="Sulcata" width="360"></p>

# Sulcata

[English](README.md) · [Français](README.fr.md)

Un explorateur local pour bases PostgreSQL et MySQL. Parcourez schémas, tables et clés étrangères, examinez les programmes stockés et leur impact sur les tables, lisez et filtrez les données, et exécutez du SQL dans une console avec validation et annulation explicites. Des modules optionnels comparent les routines à un dépôt SQL local et listent ou restaurent des sauvegardes.

Sulcata est un binaire Go unique qui embarque son interface React : une application de bureau avec sa propre fenêtre et des mises à jour intégrées sous Windows et Linux, ou un serveur local ouvert dans le navigateur (Linux, Windows, macOS, Docker). Il tourne entièrement sur votre machine, sans compte cloud ni télémétrie (en dehors de vos bases et sources de sauvegarde, sa seule requête sortante est la vérification des mises à jour, désactivable). PostgreSQL 10+ et MySQL 8 sont testés (la CI utilise PostgreSQL 10, PostgreSQL 18 et MySQL 8.4).

## Fonctionnalités

- **Vue d’ensemble** — chaque base configurée avec sa couleur, les tables ouvertes récemment et l’état des catalogues.
- **Arborescence** — bases, schémas et tables avec recherche, sur un nombre quelconque de sources.
- **Vues de table** — *Structure* (colonnes, clés, index), *Relations* (clés étrangères entrantes et sortantes), *Données* (grille paginée avec filtres, tri et édition cellule par cellule sur les connexions en écriture), *Programmes SQL* (déclencheurs et routines qui touchent la table), *Carte des relations* et *Requête SQL*.
- **Programmes stockés** — fonctions, procédures, déclencheurs et événements avec leur code source et une analyse d’impact statique : tables lues, modifiées, modifiées indirectement via des appels, lectures et écritures probables via SQL dynamique, programmes appelés et appelants.
- **Événements** — une page dédiée aux événements planifiés (MySQL), filtrables par source et par statut.
- **Console SQL** — éditeur CodeMirror avec complétion selon le schéma, formatage (sql-formatter), historique des requêtes conservé dans le navigateur, lecture seule par défaut, transactions en attente validées ou annulées explicitement.
- **Historique de schéma** — chaque export calcule une empreinte du catalogue ; une nouvelle version n’est enregistrée que si une source a changé.
- **Connexions et sauvegardes** — ajout, modification et suppression des connexions et sources de sauvegarde depuis l’interface, puis application de la configuration par un redémarrage sur place.
- **Tâches de maintenance** — export des catalogues, liste et restauration des sauvegardes s’exécutent comme des tâches annulables, avec des journaux en direct bornés et expurgés.
- **Application de bureau** — installeur pour Windows, installation en une commande sous Linux, démarre vide et se configure depuis l’interface, propose des mises à jour vérifiées.

Les libellés de l’interface sont actuellement en français.

## Prérequis

| Usage | Nécessite |
| --- | --- |
| Application de bureau sous Windows | Windows 10 ou 11 (le moteur WebView2 est installé s’il manque) |
| Application de bureau sous Linux | x86_64 avec WebKitGTK 4.1 (installé par `get.sh` s’il manque) |
| Lancer un binaire serveur | rien d’autre (pilotes et interface sont intégrés) |
| Lancer l’image Docker | Docker |
| Compiler depuis les sources | Go 1.27+ et Node 22.12+ (les installeurs les téléchargent s’ils manquent) |
| Tests navigateur et d’intégration | Python 3, Playwright avec Firefox, Docker Compose |
| Sources de sauvegarde S3 | AWS CLI (installée par l’installeur si une source S3 est configurée ; accès configurés à part) |

## Installation

### Application de bureau (recommandé)

Sulcata s’installe comme n’importe quelle application, s’ouvre dans sa propre fenêtre et se met à jour toute seule. Rien à compiler.

- **Windows** : téléchargez `sulcata_windows_amd64_setup.exe` depuis la [dernière version](https://github.com/OlivierLB/Sulcata/releases/latest) et lancez-le. Aucun droit administrateur : l’installation se fait pour votre utilisateur dans `%LOCALAPPDATA%\Programs\Sulcata`, avec des raccourcis dans le menu Démarrer et sur le bureau. L’installeur n’est pas encore signé : si SmartScreen avertit, choisissez **Informations complémentaires → Exécuter quand même**.
- **Linux** (x86_64) : lancez

  ```sh
  curl -fsSL https://raw.githubusercontent.com/OlivierLB/Sulcata/main/get.sh | bash
  ```

  Le script vérifie la somme SHA-256 du téléchargement, installe l’application dans `~/.local/share/sulcata/app` (sans `sudo`), l’ajoute au menu des applications et place la commande `sulcata` dans `~/.local/bin`. Seulement si WebKitGTK 4.1 manque, il propose de l’installer avec votre gestionnaire de paquets (apt, dnf, pacman ou zypper, avec `sudo`). Ajoutez `-s -- --yes` après `bash` pour accepter sans question.
- **macOS** : pas encore d’application de bureau ; utilisez le serveur ci-dessous ou Docker.

Au premier lancement, la fenêtre s’ouvre sur un espace vide : cliquez sur **Ajouter une base** et saisissez l’adresse, l’utilisateur et le mot de passe de la base. La configuration est créée dans votre dossier utilisateur (`~/.config/sulcata/sulcata.toml` sous Linux, `%APPDATA%\sulcata\sulcata.toml` sous Windows), avec les mots de passe dans le fichier `.env` voisin. Un lancement depuis un dossier qui contient un `sulcata.toml` utilise ce fichier à la place.

**Mises à jour** : au lancement, l’application compare sa version à la dernière version publiée sur GitHub. S’il en existe une plus récente, un bandeau propose **Installer et redémarrer** : le fichier est téléchargé, sa somme SHA-256 vérifiée avec le `SHA256SUMS` de la version, puis il est installé et l’application redémarre (sous Windows, l’installeur s’exécute en silence puis rouvre l’application). Rien ne s’installe sans votre clic, et la mise à jour est refusée, avec un message, tant qu’une transaction, une requête ou une tâche de maintenance est en cours : terminez-la et recliquez. `SULCATA_NO_UPDATE_CHECK=1` désactive la vérification.

**Désinstallation** : Windows *Paramètres → Applications* ; Linux `curl -fsSL https://raw.githubusercontent.com/OlivierLB/Sulcata/main/get.sh | bash -s -- --uninstall`. Votre configuration et les données de la fenêtre (comme l’historique de la console SQL) sont conservées.

### Serveur sans fenêtre

Chaque version publie aussi des archives `sulcata-server_<os>_<arch>` pour Linux, macOS (`.tar.gz`) et Windows (`.zip`), amd64 et arm64 : la même application sans fenêtre, pour macOS, un shell distant, un lanceur ou un conteneur. Décompressez l’archive, entrez dans son dossier et lancez `./sulcata --open` (Windows : `.\sulcata.exe --open`) pour la démarrer et ouvrir l’interface dans le navigateur ; sans configuration, elle démarre vide comme l’application de bureau. Les binaires ne sont pas signés : sous macOS, autorisez le premier lancement avec `xattr -d com.apple.quarantine sulcata` (ou *Réglages Système → Confidentialité et sécurité → Ouvrir quand même*). Voir aussi [Docker](#docker).

### Compiler depuis les sources

Pour contribuer ou utiliser la version en cours de développement. Linux et macOS :

```sh
git clone https://github.com/OlivierLB/Sulcata.git
cd Sulcata
./install.sh                 # ou : make install
```

Windows (PowerShell ou invite de commandes, sans droits administrateur) :

```powershell
git clone https://github.com/OlivierLB/Sulcata.git
cd Sulcata
.\install.cmd                # ou double-clic sur install.cmd dans l’Explorateur
```

`install.cmd` lance `install.ps1` sans modifier la stratégie d’exécution PowerShell ; les deux fonctionnent avec le Windows PowerShell 5.1 fourni avec Windows 10 et 11. Sans Git, téléchargez le dépôt en ZIP depuis GitHub et lancez l’installeur depuis le dossier extrait.

L’installeur met en place tout ce qu’il faut pour **faire tourner** Sulcata (rien pour le développement) et peut être relancé à tout moment ; chaque étape saute ce qui est déjà en place :

1. **Go et Node** (utiles seulement pour compiler) : réutilisés depuis le `PATH` (ou `~/.local/go`) s’ils sont assez récents ; sinon les archives officielles sont téléchargées, leur somme SHA-256 vérifiée, puis décompressées dans `~/.local/sulcata-toolchain` (Windows : `%LOCALAPPDATA%\sulcata-toolchain` ; modifiable avec `SULCATA_TOOLCHAIN_DIR`). Aucun droit administrateur ; le profil du shell et le `PATH` ne sont pas modifiés.
2. **Outils système** (Linux et macOS) : `curl`, `tar`, `gzip` et les certificats racine sont installés par le gestionnaire de paquets (apt, dnf, yum, pacman, zypper, apk ou Homebrew) s’ils manquent. Sur Alpine et les autres systèmes musl, Node vient du paquet système (22.12+ requis, Alpine 3.21 ou plus récent). Windows n’a besoin de rien de plus.
3. **Compilation** : l’interface, puis le binaire `bin/sulcata` (`.exe` sous Windows), sautée si le binaire est plus récent que toutes les sources (`--rebuild` / `-Rebuild` la force).
4. **Première connexion** : si `sulcata.toml` n’existe pas, l’installeur demande une base (type, hôte, port, nom, utilisateur, mot de passe) et écrit `sulcata.toml`, ainsi que le mot de passe dans `.env` (droits 600 sous Linux et macOS). Les fichiers existants ne sont jamais écrasés ; ajoutez les autres connexions ensuite depuis la page **Connexions**.
5. **Outils des sauvegardes** : si la configuration déclare des sources de sauvegarde, les outils qu’elles utilisent sont installés aussi : l’AWS CLI pour les sources S3, et `psql`, `mysql` ou les décompresseurs cités dans `restore_command` (gestionnaire de paquets sous Linux et macOS, `winget` sous Windows). Après avoir ajouté une source de sauvegarde dans l’interface, relancez l’installeur. Configurez vous-même les accès S3 (`aws configure` ou votre profil habituel).
6. **Vérification** : chaque connexion est testée en lecture seule (`sulcata check`), puis l’installeur propose de démarrer le serveur (`--start` / `-Start` le démarre sans demander).

Les installations de paquets système et `winget` sont affichées et confirmées avant exécution ; elles peuvent demander votre mot de passe `sudo`. `--yes` / `-Yes` les accepte sans demander. `--non-interactive` / `-NonInteractive` ne pose aucune question : il copie la configuration d’exemple à compléter, puis on relance l’installeur.

| Option | `install.sh` | `install.ps1` / `install.cmd` |
| --- | --- | --- |
| Démarrer le serveur à la fin | `--start` | `-Start` |
| Forcer la compilation | `--rebuild` | `-Rebuild` |
| Installer les paquets manquants sans demander | `--yes` | `-Yes` |
| Ne jamais poser de question | `--non-interactive` | `-NonInteractive` |
| Poser les questions même avec une entrée redirigée | `--configure` | `-Configure` |

#### Compilation manuelle

```sh
make frontend build          # npm ci + build Vite, puis go build dans bin/
cp sulcata.example.toml sulcata.toml
cp .env.example .env
```

Adaptez `sulcata.toml` à une base existante et définissez son mot de passe dans `.env` ou dans l’environnement du shell. Commencez avec un compte de base de données limité au strict nécessaire, idéalement SELECT et accès au catalogue.

```sh
bin/sulcata --config sulcata.toml
# Ouvrir http://localhost:8765
```

Au démarrage, le serveur exporte les catalogues de chaque connexion ; cliquez sur **Actualiser** pour les rafraîchir ensuite. Si un export échoue, le dernier catalogue complet est conservé et l’erreur s’affiche dans le bandeau d’état.

## Ligne de commande

```text
sulcata [--config FICHIER] [--port N] [--bind ADRESSE] [--open] [--headless]
                                                                      démarre (commande par défaut) : fenêtre dans l’application de bureau, serveur sinon
sulcata check   --config sulcata.toml                         teste chaque connexion, en lecture seule
sulcata inspect --config sulcata.toml                         affiche un résumé de configuration sans identifiants (JSON)
sulcata export  --config sulcata.toml                         exporte les catalogues sans démarrer le serveur
sulcata dumps   --config sulcata.toml list                    liste les sauvegardes des sources configurées
sulcata dumps   --config sulcata.toml apply CONNEXION VERSION   restaure une version de sauvegarde
sulcata version                                             affiche la version
```

Sans `--config`, le serveur utilise `sulcata.toml` du dossier courant s’il existe, sinon le fichier de l’utilisateur (`~/.config/sulcata/sulcata.toml`, `%APPDATA%\sulcata\sulcata.toml`, `~/Library/Application Support/sulcata/sulcata.toml`), créé vide au premier démarrage. `--open` ouvre l’interface dans le navigateur ; `--headless` lance l’application de bureau comme un simple serveur. Un deuxième lancement de l’application de bureau (ou avec `--open`) pendant qu’elle tourne affiche l’instance en cours au lieu d’échouer.

`--port` et `--bind` remplacent les valeurs de `[server]`. L’adresse d’écoute doit être `127.0.0.1`, `localhost` ou `0.0.0.0` (conteneurs uniquement).

## Configuration

La configuration est un fichier TOML (`sulcata.toml` par défaut). Un fichier `.env` situé à côté est lu automatiquement ; les chemins relatifs sont résolus depuis le dossier du fichier de configuration.

### Serveur

```toml
[server]
bind = "127.0.0.1"
port = 8765
allowed_db_hosts = ["127.0.0.1", "localhost"]
data_dir = ".local/data"            # catalogues générés et historique
session_idle_seconds = 1800
export_timeout_seconds = 300
dump_list_timeout_seconds = 120
restore_timeout_seconds = 3600
refresh_dumps_on_start = true
```

Toutes les clés sont optionnelles ; les valeurs ci-dessus sont les valeurs par défaut. Les hôtes de base de données doivent figurer dans `allowed_db_hosts`. Les délais doivent être des entiers positifs.

### Connexions

Ajoutez autant de sections `[[connections]]` que nécessaire :

```toml
[[connections]]
id = "library"
label = "Library"
driver = "postgresql"               # ou "mysql"
host = "127.0.0.1"
port = 5432                         # 5432 (PostgreSQL) ou 3306 (MySQL) par défaut
database = "pagila"
user = "reader"
password_env = "LIBRARY_DB_PASSWORD"
default_schema = "public"           # "public" par défaut, ou le nom de la base pour MySQL
color = "#8ab4f8"
allow_writes = false
```

- `id` est en minuscules (`[a-z][a-z0-9_-]`, 64 caractères au plus), unique et indépendant du pilote : deux sources PostgreSQL peuvent avoir des noms et des couleurs différents. `sql`, `events`, `data` et `api` sont réservés.
- Les mots de passe sont dans des variables d’environnement, jamais dans le TOML : une clé `password` est refusée. Le lecteur `.env` accepte `CLE=valeur`, un préfixe `export` optionnel, les valeurs entre guillemets et les lignes de commentaire ; il n’effectue ni interpolation, ni évaluation shell, ni interprétation des échappements. Les variables d’environnement existantes sont prioritaires.
- Chaque onglet du navigateur possède ses propres connexions persistantes, sérialisées par source. Recharger un onglet conserve sa session ; le dupliquer en crée une nouvelle.

### Fichiers locaux et Git

`sulcata.toml`, `.env`, `private/`, `local/`, `.local/`, les dumps, journaux, artefacts de test et catalogues générés sont ignorés par Git. Gardez-y tout réglage propre à votre organisation ; `sulcata.example.toml` et `.env.example` ne contiennent que des exemples publics. N’ajoutez jamais de fichier local de force : `scripts/check_public.py` refuse toute configuration locale ou donnée générée suivie par Git.

### Modifier les connexions depuis l’interface

**Connexions et sauvegardes** (`#/connexions`) regroupe chaque base avec sa source de sauvegarde optionnelle. Ajoutez, modifiez ou supprimez une connexion ou une source de sauvegarde depuis sa carte.

- Les modifications sont validées par le même chargeur qu’au démarrage, puis écrites dans le fichier de configuration. La version précédente est conservée en `*.previous` ; les commentaires TOML ne sont pas préservés.
- Un mot de passe saisi dans le formulaire est écrit dans le fichier `.env` voisin (mode 600) et n’est jamais renvoyé au navigateur. Laissez le champ vide pour conserver le mot de passe actuel.
- Les identifiants de connexion sont immuables, car catalogues et historique en dépendent. Supprimer une connexion supprime aussi sa source de sauvegarde ; la dernière connexion peut aussi être supprimée (l’application démarre alors vide). Une connexion possède au plus une source de sauvegarde.
- `allowed_db_hosts`, les commandes de restauration et les chemins de dump locaux sont affichés mais ne se modifient que dans le fichier : une requête web ne doit jamais pouvoir choisir un hôte hors liste, une commande shell ou un fichier à écraser.
- **Appliquer et redémarrer** relance le serveur sur place ; c’est refusé, avec un message, tant qu’une transaction, une requête ou une tâche de maintenance est en cours. Sous Linux et macOS, l’identifiant de processus reste le même, les lanceurs continuent donc de le suivre ; sous Windows, un nouveau processus serveur démarre dans la même console et l’ancien s’arrête. La fenêtre de l’application de bureau se ferme puis se rouvre.

L’API de paramétrage expose cette relation via `connections[].backup` (ou `null`) ; la liste à plat `dumpSources` reste disponible pour compatibilité.

## SQL, données et transactions

La lecture seule est le mode par défaut. Elle est appliquée dans l’API et configurée sur la session de base de données ; les changements de session qui pourraient la contourner sont refusés. Utilisez en plus un compte réellement limité : le classificateur SQL est une protection prudente, pas un analyseur SQL complet.

Activez `allow_writes = true` sur une connexion pour autoriser les modifications. Une écriture ouvre une transaction qui reste en attente jusqu’à **Valider** (commit) ou **Annuler** (rollback). L’arrêt du serveur, l’expiration ou la fermeture de la session annulent les transactions en attente.

- Les instructions PostgreSQL et les lectures de la grille utilisent des points de sauvegarde (savepoints) pour se remettre d’une erreur dans une transaction.
- Une instruction DDL MySQL valide implicitement la transaction en cours, même si elle échoue ; l’interface indique l’état obtenu. Certains moteurs de stockage MySQL ne sont pas transactionnels. Les appels de routines peuvent avoir des effets que l’analyse statique ne peut pas prévoir.
- L’édition dans la grille génère un `UPDATE … WHERE <clé primaire> = ?` paramétré et nécessite une clé primaire.

Limites :

| Élément | Limite |
| --- | --- |
| Lignes de résultat de la console | 500 |
| Taille de page de la grille | 100 par défaut, jusqu’à 500 |
| Cellule texte | 2 000 premiers caractères ; les cellules binaires affichent leur taille en octets |
| Délai d’instruction PostgreSQL | 15 s pour la grille, 120 s pour la console |
| MySQL | `max_execution_time` sur les SELECT éligibles, plus des délais socket de 125 s en lecture/écriture |

Les valeurs sont affichées telles que la base les imprime (JSON en JSON, tableaux PostgreSQL en `{…}`, booléens en `true`/`false`).

### Sessions

Une session navigateur expire après `session_idle_seconds` (30 minutes par défaut) sans opération en base ni renouvellement explicite ; le rafraîchissement de l’état ne la maintient pas en vie. L’interface prévient cinq minutes avant l’expiration. L’expiration et l’arrêt du serveur ferment les connexions et annulent les transactions en attente.

La reprise après rechargement utilise l’API Web Locks du navigateur ; un navigateur qui ne la propose pas reçoit une nouvelle session isolée et l’ancienne expire normalement. Un navigateur déconnecté n’annule ni ne relance une opération SQL : si son issue est incertaine, vérifiez la session et les données avant de continuer.

## Analyser un créneau d’événements

Dans **Événements**, le mode **Environnement de référence** lit les fichiers SQL du dépôt configuré, même si la base locale ne contient aucun événement. Choisis le fuseau supposé de cet environnement, puis une date, des heures ou toute une journée et **Analyser le créneau**. La simulation suppose les événements et le planificateur activés, y compris quand le script déclare `DISABLE`. Aucune définition SQL n’est exécutée et aucun catalogue privé n’est enregistré dans le dépôt public.

Les dates littérales des clauses `STARTS`, `ENDS` et `AT`, ainsi que les intervalles simples sont interprétés. Le fuseau du formulaire sert lorsque le script ne précise pas `SET time_zone`. Les heures de saisie et d’affichage utilisent le fuseau du navigateur. Sans départ explicite, avec une expression de date non prise en charge, un intervalle composite ou un changement d’heure, l’événement reste visible dans les horaires non calculables : son heure de lancement ne peut pas être déduite sans informations supplémentaires sur le déploiement.

Le mode **Base locale** conserve l’analyse du dernier catalogue exporté et respecte ses états activé/désactivé. Actualise le catalogue pour obtenir les horaires structurés.

Les résultats regroupent les départs prévus, ceux juste avant le créneau, les tables partagées et les procédures appelées consultables dans l’inspecteur. Clique sur une barre pour préciser la période. Ce sont des pistes de lenteur : ni les durées, ni les exécutions réelles, ni une cause ne sont prouvées. Une simulation des sources actuelles ne reconstitue pas les anciennes versions déployées. L’API limite les créneaux à sept jours et la période précédente à 24 heures.

## Modules optionnels

### Dépôt de sources des routines

Comparez les programmes stockés à des fichiers SQL d’un dépôt local :

```toml
[routines_repository]
path = "../my-routines"
pattern = "{schema}/{kind}s/{name}.sql"
connections = ["library"]           # identifiants des connexions concernées
scan_definitions = true             # chercher les CREATE dans des fichiers SQL partagés
```

Les fichiers UTF-8 et Latin-1 sont pris en charge. La comparaison ignore les espaces, commentaires et différences de guillemets.

L’analyse d’impact déduit lectures, écritures, appels et écritures indirectes à partir du texte SQL ; les références construites en SQL dynamique sont marquées *probables*. Les nombres de lignes sont des estimations du catalogue ; une table PostgreSQL non vide jamais analysée affiche un nombre inconnu.

### Sauvegardes

Source de type dossier :

```toml
[[dump_sources]]
connection = "library"
type = "directory"
path = "./backups"
pattern = "*.sql.gz"
local_path = ".local/dumps/library.sql.gz"
restore_command = 'gzip -dc "$SULCATA_DUMP_PATH" | psql --dbname=pagila'
```

Source S3 : utilisez `type = "s3-versioned"` avec `bucket` et `key` à la place de `path` et `pattern`. Seules les versions de cette clé exacte sont proposées, et la pagination de l’AWS CLI est suivie. L’installeur installe l’AWS CLI quand une telle source est configurée ; configurez vous-même ses accès.

Avec `[server].refresh_dumps_on_start = false`, le cache local des sauvegardes est chargé au démarrage et la liste distante n’est interrogée que lorsque vous l’actualisez dans l’interface. Ce réglage ne déclenche jamais de restauration. Rien n’est téléchargé ni restauré sans demande. Les fonctions de sauvegarde et de sources de routines restent masquées tant que leur module n’est pas configuré.

**La restauration remplace le contenu de la base.** `restore_command` est une commande shell de confiance issue de votre configuration locale, exécutée avec vos droits utilisateur. Le chemin du fichier téléchargé est transmis dans `SULCATA_DUMP_PATH` et l’identifiant de connexion dans `SULCATA_CONNECTION`. La commande doit lire ce fichier et renvoyer un code non nul en cas d’échec. Aucune commande fournie par une requête n’est jamais exécutée.

Sous Linux et macOS, la commande passe par `/bin/sh`. Sous Windows, elle passe par `cmd.exe` : les variables s’écrivent `%SULCATA_DUMP_PATH%`, par exemple `restore_command = 'psql --dbname=pagila --file="%SULCATA_DUMP_PATH%"'` pour un dump non compressé ; Windows n’a pas `gzip` par défaut.

Avant restauration, l’espace disque libre et la taille sont vérifiés ; le dump local précédent est conservé jusqu’à la réussite ; une transaction ouverte ou une requête active bloque la restauration. Une commande échouée ou annulée peut néanmoins laisser une base partiellement restaurée : ce n’est ni un système de sauvegarde ni une restauration atomique. L’annulation arrête tout l’arbre de processus : par groupe de processus sous Linux, macOS et dans l’image Docker, et via `taskkill /T` sous Windows.

## Docker

```sh
docker build -t sulcata .
mkdir -p .local
export SULCATA_UID=$(id -u) SULCATA_GID=$(id -g)
docker compose up
```

L’image ne contient que le binaire et un shell (pour les `restore_command` de confiance) ; ni Node ni Python. Le service Compose tourne en lecture seule, monte `sulcata.toml` (en lecture seule) et `.env`, stocke les données générées dans `.local/` et ne publie que `127.0.0.1:8765`.

Dans un conteneur, `127.0.0.1` désigne le conteneur lui-même. Sous Linux, utilisez `host.docker.internal` (associé à la passerelle de l’hôte dans l’exemple) et ajoutez-le à `allowed_db_hosts`. Rattachez explicitement d’autres réseaux Docker si besoin.

## Modèle de sécurité

- L’écoute par défaut se fait sur localhost. `0.0.0.0` existe pour les conteneurs ; ne publiez le port que sur localhost.
- Le serveur vérifie l’en-tête HTTP `Host` et exige `X-Sulcata: 1` sur les requêtes POST pour limiter le DNS rebinding et les requêtes cross-origin.
- Il n’y a **ni authentification ni TLS** : ne l’exposez jamais à Internet ou à un réseau non fiable.
- Les mises à jour ne s’installent qu’après un clic dans l’interface, depuis les versions GitHub du projet, et seulement si la somme SHA-256 du fichier correspond au `SHA256SUMS` de la version. `SULCATA_NO_UPDATE_CHECK=1` désactive cette vérification, la seule requête de Sulcata en dehors de vos bases et sources de sauvegarde configurées.
- Seuls l’interface embarquée et les fichiers de catalogue générés sont servis. Hôtes, utilisateurs et mots de passe n’atteignent jamais le navigateur via la configuration publique, et les messages d’erreur comme les journaux de tâches sont expurgés.

## Développement

Le backend utilise la bibliothèque HTTP standard de Go, `pgx/pgconn` et `go-mysql`. Le frontend utilise React 19, TypeScript strict, TanStack Query, CodeMirror et Vite.

```sh
go run ./cmd/sulcata --config sulcata.toml   # terminal 1
npm run dev --prefix frontend                        # terminal 2, Vite relaie l’API vers Go
```

- Compilez le frontend avant `go build` : le binaire embarque `web/dist/app`.
- `frontend/openapi.json` est le contrat HTTP. Modifiez-le avec les handlers Go, lancez `npm run api:generate --prefix frontend` et committez les deux. Les tests Go échouent si une route du contrat n’est pas servie ou si un champ de réponse n’est pas déclaré.

### Application de bureau et publication

L’application de bureau est le même binaire compilé avec l’étiquette `desktop` : elle démarre le serveur local puis l’affiche dans une fenêtre [Wails](https://wails.io) (WebView2 sous Windows, WebKitGTK 4.1 sous Linux). La compilation par défaut reste le simple serveur.

```sh
go build -tags desktop,production,webkit2_41 ./cmd/sulcata     # Linux : nécessite libgtk-3-dev et libwebkit2gtk-4.1-dev
GOOS=windows go build -tags desktop,production -ldflags -H=windowsgui ./cmd/sulcata
scripts/desktop_smoke.sh      # Docker : compile, démarre depuis un profil vide sous Xvfb, capture d’écran, installe une fausse mise à jour
scripts/release.sh 1.2.3      # tous les fichiers de la version dans dist/ (Linux ; nécessite l’interface compilée, gcc, libgtk-3-dev, libwebkit2gtk-4.1-dev, nsis, zip, et le réseau pour go-winres sauf SKIP_WINRES=1)
scripts/test_get_matrix.sh dist   # get.sh + fenêtre sous Ubuntu 22.04/24.04, Debian 12/13, Fedora, Arch, openSUSE (Docker)
scripts/local_install.sh      # installe cette copie de travail sur votre machine via get.sh, comme un utilisateur
```

Pousser une étiquette `vX.Y.Z` lance `.github/workflows/release.yml` : il fabrique les fichiers avec `scripts/release.sh` sous Ubuntu 22.04 (la glibc la plus ancienne prise en charge), lance `scripts/test_get_matrix.sh` sur sept distributions Linux, installe l’application sur une machine Windows, ouvre sa fenêtre, déroule une mise à jour intégrée complète puis la désinstalle, puis publie la version GitHub qu’utilisent la mise à jour intégrée et `get.sh`. Les noms des fichiers sont fixes (`sulcata_linux_amd64.tar.gz`, `sulcata_windows_amd64_setup.exe`, `SHA256SUMS`), car la mise à jour les cherche par leur nom.

### Tests

```sh
make frontend
make test                                  # go vet, gofmt, go test -race
npm run typecheck --prefix frontend
npm run lint --prefix frontend
npm test --prefix frontend
pip install -r tests/requirements.txt      # outillage des tests navigateur uniquement
python -m playwright install firefox
bash tests/run.sh                          # ou : make integration
python scripts/check_distribution.py bin/sulcata
docker build -t sulcata:test .
python scripts/check_docker.py
```

`make check` enchaîne `make test`, la compilation, le contrôle de contenu public et la vérification de distribution.

`tests/run.sh` démarre un projet Compose séparé, `sulcata-test`, avec PostgreSQL 18, PostgreSQL 10 et MySQL 8.4 sur les ports locaux 55432, 55433 et 53306, sur un stockage tmpfs jetable. Pagila 3.0 et un miroir public de Sakila sont téléchargés à des commits figés, avec vérification SHA-256, dans un dossier ignoré. Les tests ajoutent de petites tables sondes, des déclencheurs, une procédure et un événement, et couvrent transactions, savepoints, mode lecture seule côté base, commits implicites MySQL, configuration, sauvegardes optionnelles, protections HTTP et comportement de l’interface sous Firefox avec six identifiants de source. Le script n’arrête que ce projet de test en sortant.

Les bases d’exemple conservent leurs licences : [Pagila](https://github.com/devrimgunduz/pagila) et [Sakila (BSD)](https://dev.mysql.com/doc/sakila/en/sakila-license.html). Elles sont téléchargées pour les tests et ne sont pas distribuées.

Avant de committer, lancez `python scripts/check_public.py`. La CI l’exécute avec les contrôles frontend, les contrôles Go, les tests d’intégration et navigateur, la vérification du binaire autonome et celle de l’image Docker. `scripts/bench_backend.py` mesure le démarrage, la mémoire et la latence de l’API d’un build en cours d’exécution. Voir [CONTRIBUTING.md](CONTRIBUTING.md) (en anglais).

## Licence

MIT. Les mentions des composants tiers de l’interface (React, CodeMirror, sql-formatter…) sont générées dans `THIRD_PARTY_LICENSES.txt` par le build frontend et embarquées dans le binaire. Les dépendances Go sont listées dans `go.mod`.
