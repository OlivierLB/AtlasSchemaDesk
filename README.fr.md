# Atlas Schema Desk

[English](README.md) · [Français](README.fr.md)

Un explorateur local pour bases PostgreSQL et MySQL. Parcourez schémas, tables et clés étrangères, examinez les programmes stockés et leur impact sur les tables, lisez et filtrez les données, et exécutez du SQL dans une console avec validation et annulation explicites. Des modules optionnels comparent les routines à un dépôt SQL local et listent ou restaurent des sauvegardes.

Atlas Schema Desk est un binaire Go unique qui embarque son interface React. Il tourne entièrement sur votre machine, sans compte cloud ni télémétrie. PostgreSQL 10+ et MySQL 8 sont testés (la CI utilise PostgreSQL 10, PostgreSQL 18 et MySQL 8.4).

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

Les libellés de l’interface sont actuellement en français.

## Prérequis

| Usage | Nécessite |
| --- | --- |
| Lancer un binaire compilé | rien d’autre (pilotes et interface sont intégrés) |
| Lancer l’image Docker | Docker |
| Compiler depuis les sources | Go 1.27+ et Node 22.12+ |
| Tests navigateur et d’intégration | Python 3, Playwright avec Firefox, Docker Compose |
| Sources de sauvegarde S3 | AWS CLI, installée et configurée à part |

## Démarrage rapide

```sh
git clone https://github.com/OlivierLB/AtlasSchemaDesk.git
cd AtlasSchemaDesk
make frontend build          # npm ci + build Vite, puis go build dans bin/
cp atlas.example.toml atlas.toml
cp .env.example .env
```

Adaptez `atlas.toml` à une base existante et définissez son mot de passe dans `.env` ou dans l’environnement du shell. Commencez avec un compte de base de données limité au strict nécessaire, idéalement SELECT et accès au catalogue.

```sh
bin/atlas-schema-desk --config atlas.toml
# Ouvrir http://localhost:8765
```

Au démarrage, le serveur exporte les catalogues de chaque connexion ; cliquez sur **Actualiser** pour les rafraîchir ensuite. Si un export échoue, le dernier catalogue complet est conservé et l’erreur s’affiche dans le bandeau d’état.

## Ligne de commande

```text
atlas-schema-desk [--config atlas.toml] [--port N] [--bind ADRESSE]   démarre le serveur (commande par défaut)
atlas-schema-desk check   --config atlas.toml                         teste chaque connexion, en lecture seule
atlas-schema-desk inspect --config atlas.toml                         affiche un résumé de configuration sans identifiants (JSON)
atlas-schema-desk export  --config atlas.toml                         exporte les catalogues sans démarrer le serveur
atlas-schema-desk dumps   --config atlas.toml list                    liste les sauvegardes des sources configurées
atlas-schema-desk dumps   --config atlas.toml apply CONNEXION VERSION   restaure une version de sauvegarde
atlas-schema-desk version                                             affiche la version
```

`--port` et `--bind` remplacent les valeurs de `[server]`. L’adresse d’écoute doit être `127.0.0.1`, `localhost` ou `0.0.0.0` (conteneurs uniquement).

## Configuration

La configuration est un fichier TOML (`atlas.toml` par défaut). Un fichier `.env` situé à côté est lu automatiquement ; les chemins relatifs sont résolus depuis le dossier du fichier de configuration.

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

`atlas.toml`, `.env`, `private/`, `local/`, `.local/`, les dumps, journaux, artefacts de test et catalogues générés sont ignorés par Git. Gardez-y tout réglage propre à votre organisation ; `atlas.example.toml` et `.env.example` ne contiennent que des exemples publics. N’ajoutez jamais de fichier local de force : `scripts/check_public.py` refuse toute configuration locale ou donnée générée suivie par Git.

### Modifier les connexions depuis l’interface

**Connexions et sauvegardes** (`#/connexions`) regroupe chaque base avec sa source de sauvegarde optionnelle. Ajoutez, modifiez ou supprimez une connexion ou une source de sauvegarde depuis sa carte.

- Les modifications sont validées par le même chargeur qu’au démarrage, puis écrites dans le fichier de configuration. La version précédente est conservée en `*.previous` ; les commentaires TOML ne sont pas préservés.
- Un mot de passe saisi dans le formulaire est écrit dans le fichier `.env` voisin (mode 600) et n’est jamais renvoyé au navigateur. Laissez le champ vide pour conserver le mot de passe actuel.
- Les identifiants de connexion sont immuables, car catalogues et historique en dépendent. Supprimer une connexion supprime aussi sa source de sauvegarde. Une connexion possède au plus une source de sauvegarde.
- `allowed_db_hosts`, les commandes de restauration et les chemins de dump locaux sont affichés mais ne se modifient que dans le fichier : une requête web ne doit jamais pouvoir choisir un hôte hors liste, une commande shell ou un fichier à écraser.
- **Appliquer et redémarrer** relance le serveur sur place (même identifiant de processus, les lanceurs continuent donc de le suivre) dès qu’aucune transaction, requête ni tâche de maintenance n’est en cours.

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

Dans **Événements**, choisis une date et une plage horaire, ou toute la journée, puis **Analyser le créneau**. Le calcul utilise le catalogue local, sans requête vers les bases. Les saisies et résultats utilisent le fuseau du navigateur ; chaque événement est calculé dans son fuseau exporté. Une heure de fin antérieure au début signifie le lendemain.

Les résultats regroupent les lancements prévus, le dernier départ dans la période précédente choisie et les tables touchées par plusieurs événements. Clique sur une barre de la chronologie pour préciser le créneau. Ces éléments sont des pistes d’enquête : ils ne mesurent ni les durées, ni la concurrence réelle, ni l’historique des exécutions et ne prouvent pas une cause de lenteur. Les états du planificateur et des événements sont ceux du dernier export, même pour une date passée.

Actualise le catalogue après la mise à jour pour charger les horaires structurés. Les métadonnées absentes, fuseaux non résolus, intervalles composites non pris en charge et jours avec changement d’heure sont explicitement signalés comme non calculables. Les événements et planificateurs désactivés sont listés séparément. L’API limite le créneau à sept jours et la période précédente à 24 heures ; les lancements fréquents sont agrégés. Voir la [documentation MySQL des métadonnées d’événements](https://dev.mysql.com/doc/refman/8.4/en/information-schema-events-table.html).

Après compilation du frontend, lance `python tests/ui_event_window.py` dans l’environnement de test pour vérifier cette interface avec des données fictives.

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
restore_command = 'gzip -dc "$ATLAS_DUMP_PATH" | psql --dbname=pagila'
```

Source S3 : utilisez `type = "s3-versioned"` avec `bucket` et `key` à la place de `path` et `pattern`. Seules les versions de cette clé exacte sont proposées, et la pagination de l’AWS CLI est suivie. Installez et configurez l’AWS CLI séparément.

Avec `[server].refresh_dumps_on_start = false`, le cache local des sauvegardes est chargé au démarrage et la liste distante n’est interrogée que lorsque vous l’actualisez dans l’interface. Ce réglage ne déclenche jamais de restauration. Rien n’est téléchargé ni restauré sans demande. Les fonctions de sauvegarde et de sources de routines restent masquées tant que leur module n’est pas configuré.

**La restauration remplace le contenu de la base.** `restore_command` est une commande shell de confiance issue de votre configuration locale, exécutée avec vos droits utilisateur. Le chemin du fichier téléchargé est transmis dans `ATLAS_DUMP_PATH` et l’identifiant de connexion dans `ATLAS_CONNECTION`. La commande doit lire ce fichier et renvoyer un code non nul en cas d’échec. Aucune commande fournie par une requête n’est jamais exécutée.

Avant restauration, l’espace disque libre et la taille sont vérifiés ; le dump local précédent est conservé jusqu’à la réussite ; une transaction ouverte ou une requête active bloque la restauration. Une commande échouée ou annulée peut néanmoins laisser une base partiellement restaurée : ce n’est ni un système de sauvegarde ni une restauration atomique. L’annulation de l’arbre de processus est prise en charge sous Linux et dans l’image Docker.

## Docker

```sh
docker build -t atlas-schema-desk .
mkdir -p .local
export ATLAS_UID=$(id -u) ATLAS_GID=$(id -g)
docker compose up
```

L’image ne contient que le binaire et un shell (pour les `restore_command` de confiance) ; ni Node ni Python. Le service Compose tourne en lecture seule, monte `atlas.toml` (en lecture seule) et `.env`, stocke les données générées dans `.local/` et ne publie que `127.0.0.1:8765`.

Dans un conteneur, `127.0.0.1` désigne le conteneur lui-même. Sous Linux, utilisez `host.docker.internal` (associé à la passerelle de l’hôte dans l’exemple) et ajoutez-le à `allowed_db_hosts`. Rattachez explicitement d’autres réseaux Docker si besoin.

## Modèle de sécurité

- L’écoute par défaut se fait sur localhost. `0.0.0.0` existe pour les conteneurs ; ne publiez le port que sur localhost.
- Le serveur vérifie l’en-tête HTTP `Host` et exige `X-DB-Atlas: 1` sur les requêtes POST pour limiter le DNS rebinding et les requêtes cross-origin.
- Il n’y a **ni authentification ni TLS** : ne l’exposez jamais à Internet ou à un réseau non fiable.
- Seuls l’interface embarquée et les fichiers de catalogue générés sont servis. Hôtes, utilisateurs et mots de passe n’atteignent jamais le navigateur via la configuration publique, et les messages d’erreur comme les journaux de tâches sont expurgés.

## Développement

Le backend utilise la bibliothèque HTTP standard de Go, `pgx/pgconn` et `go-mysql`. Le frontend utilise React 19, TypeScript strict, TanStack Query, CodeMirror et Vite.

```sh
go run ./cmd/atlas-schema-desk --config atlas.toml   # terminal 1
npm run dev --prefix frontend                        # terminal 2, Vite relaie l’API vers Go
```

- Compilez le frontend avant `go build` : le binaire embarque `web/dist/app`.
- `frontend/openapi.json` est le contrat HTTP. Modifiez-le avec les handlers Go, lancez `npm run api:generate --prefix frontend` et committez les deux. Les tests Go échouent si une route du contrat n’est pas servie ou si un champ de réponse n’est pas déclaré.

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
python scripts/check_distribution.py bin/atlas-schema-desk
docker build -t atlas-schema-desk:test .
python scripts/check_docker.py
```

`make check` enchaîne `make test`, la compilation, le contrôle de contenu public et la vérification de distribution.

`tests/run.sh` démarre un projet Compose séparé, `atlas-schema-desk-test`, avec PostgreSQL 18, PostgreSQL 10 et MySQL 8.4 sur les ports locaux 55432, 55433 et 53306, sur un stockage tmpfs jetable. Pagila 3.0 et un miroir public de Sakila sont téléchargés à des commits figés, avec vérification SHA-256, dans un dossier ignoré. Les tests ajoutent de petites tables sondes, des déclencheurs, une procédure et un événement, et couvrent transactions, savepoints, mode lecture seule côté base, commits implicites MySQL, configuration, sauvegardes optionnelles, protections HTTP et comportement de l’interface sous Firefox avec six identifiants de source. Le script n’arrête que ce projet de test en sortant.

Les bases d’exemple conservent leurs licences : [Pagila](https://github.com/devrimgunduz/pagila) et [Sakila (BSD)](https://dev.mysql.com/doc/sakila/en/sakila-license.html). Elles sont téléchargées pour les tests et ne sont pas distribuées.

Avant de committer, lancez `python scripts/check_public.py`. La CI l’exécute avec les contrôles frontend, les contrôles Go, les tests d’intégration et navigateur, la vérification du binaire autonome et celle de l’image Docker. `scripts/bench_backend.py` mesure le démarrage, la mémoire et la latence de l’API d’un build en cours d’exécution. Voir [CONTRIBUTING.md](CONTRIBUTING.md) (en anglais).

## Licence

MIT. Les mentions des composants tiers de l’interface (React, CodeMirror, sql-formatter…) sont générées dans `THIRD_PARTY_LICENSES.txt` par le build frontend et embarquées dans le binaire. Les dépendances Go sont listées dans `go.mod`.
