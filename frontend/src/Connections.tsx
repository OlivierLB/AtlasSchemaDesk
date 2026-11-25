import { useEffect, useRef, useState } from "react";
import type { CSSProperties } from "react";
import { Icon } from "./Icon";
import type { FormEvent } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { request } from "./api";
import { useSulcata } from "./context";
import { errorText } from "./model";
import type {
  ConnectionInput,
  ConnectionsOverview,
  DumpSourceInput,
  Dumps,
  SaveResult,
} from "./model";

type ConnectionForm = { isNew: boolean; value: ConnectionInput };
type SourceForm = { isNew: boolean; value: DumpSourceInput };

const defaultPort = { postgresql: 5432, mysql: 3306 } as const;

function emptyConnection(): ConnectionInput {
  return {
    id: "",
    label: "",
    driver: "postgresql",
    host: "127.0.0.1",
    port: defaultPort.postgresql,
    database: "",
    user: "",
    defaultSchema: "",
    color: "#8ab4f8",
    allowWrites: false,
    passwordEnv: "",
    password: "",
  };
}

const passwordVariable = (id: string) =>
  `SULCATA_${id.toUpperCase().replace(/[^A-Z0-9]/g, "_")}_PASSWORD`;

async function waitForRestart() {
  // The server re-executes itself; wait until it answers with the new
  // configuration, then reload so every query and session starts fresh.
  await new Promise((resolve) => setTimeout(resolve, 800));
  for (let attempt = 0; attempt < 150; attempt++) {
    try {
      const overview = await request<ConnectionsOverview>(
        "/api/connections",
        undefined,
        false,
      );
      if (!overview.pendingRestart) break;
    } catch {
      /* server still restarting */
    }
    await new Promise((resolve) => setTimeout(resolve, 400));
  }
  location.reload();
}

export function Connections() {
  const { tables, status, notice, route } = useSulcata();
  const client = useQueryClient();
  const overview = useQuery({
    queryKey: ["connections"],
    queryFn: () =>
      request<ConnectionsOverview>("/api/connections", undefined, false),
  });
  const dumps = useQuery({
    queryKey: ["dumps", status?.s3.finishedAt],
    queryFn: () =>
      request<Dumps>(`/data/s3-dumps.json?t=${Date.now()}`, undefined, false),
    retry: false,
  });
  const [connectionForm, setConnectionForm] = useState<ConnectionForm | null>(
    null,
  );
  const [sourceForm, setSourceForm] = useState<SourceForm | null>(null);
  const connectionRef = useRef<HTMLFormElement>(null);
  const sourceRef = useRef<HTMLFormElement>(null);
  useEffect(() => {
    connectionRef.current?.scrollIntoView({ block: "nearest" });
    connectionRef.current?.focus({ preventScroll: true });
  }, [connectionForm?.isNew, connectionForm?.value.id]);
  useEffect(() => {
    sourceRef.current?.scrollIntoView({ block: "nearest" });
    sourceRef.current?.focus({ preventScroll: true });
  }, [sourceForm?.value.connection]);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [restarting, setRestarting] = useState(false);
  const selectedConnection = route.params?.connection;
  // The home page links here with ?ajouter=1 when no database exists yet.
  const addRequested = route.params?.ajouter === "1";
  useEffect(() => {
    if (addRequested)
      setConnectionForm({ isNew: true, value: emptyConnection() });
  }, [addRequested]);
  useEffect(() => {
    if (overview.data && selectedConnection)
      document
        .getElementById(`connection-${selectedConnection}`)
        ?.scrollIntoView({ block: "start" });
  }, [overview.data, selectedConnection]);

  const run = async (action: () => Promise<SaveResult | undefined>) => {
    setBusy(true);
    setError("");
    try {
      const result = await action();
      result?.warnings.forEach((warning) => notice(warning));
      await client.invalidateQueries({ queryKey: ["connections"] });
      return true;
    } catch (failure) {
      setError(errorText(failure));
      return false;
    } finally {
      setBusy(false);
    }
  };

  const saveConnection = async (event: FormEvent) => {
    event.preventDefault();
    if (!connectionForm) return;
    const ok = await run(() =>
      request<SaveResult>("/api/connections/save", {
        isNew: connectionForm.isNew,
        connection: connectionForm.value,
      }),
    );
    if (ok) setConnectionForm(null);
  };

  const saveSource = async (event: FormEvent) => {
    event.preventDefault();
    if (!sourceForm) return;
    const ok = await run(() =>
      request<SaveResult>("/api/dump-sources/save", {
        source: sourceForm.value,
      }),
    );
    if (ok) setSourceForm(null);
  };

  const restart = async () => {
    if (
      !confirm(
        "Redémarrer le serveur pour appliquer la configuration ? Les onglets ouverts recevront une nouvelle session.",
      )
    )
      return;
    setRestarting(true);
    try {
      await request("/api/restart", {});
      await waitForRestart();
    } catch (failure) {
      setRestarting(false);
      setError(errorText(failure));
    }
  };

  if (overview.error)
    return (
      <div className="page">
        <h1 className="page-title">Connexions</h1>
        <p role="alert">{errorText(overview.error)}</p>
      </div>
    );
  const data = overview.data;
  if (!data)
    return (
      <div className="page">
        <h1 className="page-title">Connexions</h1>
        <p>Lecture de la configuration…</p>
      </div>
    );
  const targets = new Map(
    (dumps.data?.targets || []).map((target) => [target.engine, target]),
  );
  const editConnection = (patch: Partial<ConnectionInput>) =>
    setConnectionForm((form) =>
      form ? { ...form, value: { ...form.value, ...patch } } : form,
    );
  const editSource = (patch: Partial<DumpSourceInput>) =>
    setSourceForm((form) =>
      form ? { ...form, value: { ...form.value, ...patch } } : form,
    );

  const connectionEditor = connectionForm && (
    <form
      ref={connectionRef}
      tabIndex={-1}
      className="settings-form"
      aria-label="Connexion"
      onSubmit={(event) => void saveConnection(event)}
    >
      <h3>
        {connectionForm.isNew
          ? "Nouvelle connexion"
          : `Modifier ${connectionForm.value.label || connectionForm.value.id}`}
      </h3>
      <label>
        Identifiant
        <input
          required
          pattern="[a-z][a-z0-9_\-]{0,63}"
          title="Minuscules, chiffres, - et _ ; commence par une lettre"
          disabled={!connectionForm.isNew}
          value={connectionForm.value.id}
          onChange={(event) =>
            editConnection({
              id: event.target.value,
              passwordEnv:
                connectionForm.isNew &&
                (connectionForm.value.passwordEnv === "" ||
                  connectionForm.value.passwordEnv ===
                    passwordVariable(connectionForm.value.id))
                  ? passwordVariable(event.target.value)
                  : connectionForm.value.passwordEnv,
            })
          }
        />
      </label>
      <label>
        Libellé
        <input
          value={connectionForm.value.label}
          onChange={(event) => editConnection({ label: event.target.value })}
        />
      </label>
      <label>
        Moteur
        <select
          value={connectionForm.value.driver}
          onChange={(event) => {
            const driver = event.target.value as ConnectionInput["driver"];
            editConnection({ driver, port: defaultPort[driver] });
          }}
        >
          <option value="postgresql">PostgreSQL</option>
          <option value="mysql">MySQL</option>
        </select>
      </label>
      <label>
        Hôte
        <input
          required
          list="allowed-db-hosts"
          value={connectionForm.value.host}
          onChange={(event) => editConnection({ host: event.target.value })}
        />
        <datalist id="allowed-db-hosts">
          {data.allowedDbHosts.map((host) => (
            <option key={host} value={host} />
          ))}
        </datalist>
      </label>
      <label>
        Port
        <input
          required
          type="number"
          min={1}
          max={65535}
          value={connectionForm.value.port}
          onChange={(event) =>
            editConnection({ port: Number(event.target.value) })
          }
        />
      </label>
      <label>
        Base
        <input
          required
          value={connectionForm.value.database}
          onChange={(event) => editConnection({ database: event.target.value })}
        />
      </label>
      <label>
        Utilisateur
        <input
          required
          autoComplete="off"
          value={connectionForm.value.user}
          onChange={(event) => editConnection({ user: event.target.value })}
        />
      </label>
      <label>
        Schéma par défaut
        <input
          placeholder={
            connectionForm.value.driver === "mysql"
              ? "nom de la base"
              : "public"
          }
          value={connectionForm.value.defaultSchema}
          onChange={(event) =>
            editConnection({ defaultSchema: event.target.value })
          }
        />
      </label>
      <label>
        Variable du mot de passe
        <input
          required
          pattern="[A-Za-z_][A-Za-z0-9_]*"
          className="mono"
          value={connectionForm.value.passwordEnv}
          onChange={(event) =>
            editConnection({ passwordEnv: event.target.value })
          }
        />
      </label>
      <label>
        Mot de passe
        <input
          type="password"
          autoComplete="new-password"
          required={connectionForm.isNew}
          placeholder={connectionForm.isNew ? "" : "inchangé si laissé vide"}
          value={connectionForm.value.password || ""}
          onChange={(event) => editConnection({ password: event.target.value })}
        />
      </label>
      <label>
        Couleur
        <input
          type="color"
          value={connectionForm.value.color}
          onChange={(event) => editConnection({ color: event.target.value })}
        />
      </label>
      <label className="settings-check">
        <input
          type="checkbox"
          checked={connectionForm.value.allowWrites}
          onChange={(event) =>
            editConnection({ allowWrites: event.target.checked })
          }
        />
        Autoriser les écritures (transactions explicites)
      </label>
      <div className="settings-actions">
        <button className="button" type="submit" disabled={busy}>
          Enregistrer
        </button>
        <button
          className="button"
          type="button"
          onClick={() => setConnectionForm(null)}
        >
          Annuler
        </button>
      </div>
    </form>
  );
  const sourceEditor = sourceForm && (
    <form
      ref={sourceRef}
      tabIndex={-1}
      className="settings-form"
      aria-label="Source de sauvegarde"
      onSubmit={(event) => void saveSource(event)}
    >
      <h3>
        {sourceForm.isNew
          ? "Nouvelle source de sauvegarde"
          : "Modifier la source de sauvegarde"}
      </h3>
      <label>
        Connexion
        <select
          disabled
          value={sourceForm.value.connection}
          onChange={(event) => editSource({ connection: event.target.value })}
        >
          {data.connections.map((connection) => (
            <option key={connection.id} value={connection.id}>
              {connection.label}
            </option>
          ))}
        </select>
      </label>
      <label>
        Type
        <select
          value={sourceForm.value.type}
          onChange={(event) =>
            editSource({
              type: event.target.value as DumpSourceInput["type"],
            })
          }
        >
          <option value="s3-versioned">S3 versionné</option>
          <option value="directory">Dossier local</option>
        </select>
      </label>
      {sourceForm.value.type === "s3-versioned" ? (
        <>
          <label>
            Bucket
            <input
              required
              value={sourceForm.value.bucket || ""}
              onChange={(event) => editSource({ bucket: event.target.value })}
            />
          </label>
          <label>
            Clé de l’objet
            <input
              required
              value={sourceForm.value.key || ""}
              onChange={(event) => editSource({ key: event.target.value })}
            />
          </label>
        </>
      ) : (
        <>
          <label>
            Dossier
            <input
              required
              value={sourceForm.value.path || ""}
              onChange={(event) => editSource({ path: event.target.value })}
            />
          </label>
          <label>
            Motif des fichiers
            <input
              placeholder="*.sql*"
              value={sourceForm.value.pattern || ""}
              onChange={(event) => editSource({ pattern: event.target.value })}
            />
          </label>
        </>
      )}
      <div className="settings-actions">
        <button className="button" type="submit" disabled={busy}>
          Enregistrer
        </button>
        <button
          className="button"
          type="button"
          onClick={() => setSourceForm(null)}
        >
          Annuler
        </button>
      </div>
    </form>
  );
  const startSource = (id: string) => {
    const backup = data.connections.find((c) => c.id === id)?.backup;
    setSourceForm({
      isNew: !backup,
      value: backup
        ? {
            connection: id,
            type: backup.type,
            bucket: backup.bucket,
            key: backup.key,
            path: backup.path,
            pattern: backup.pattern,
          }
        : {
            connection: id,
            type: "s3-versioned",
            bucket: "",
            key: "",
            path: "",
            pattern: "",
          },
    });
  };
  return (
    <div className="page settings-page">
      <header className="page-header settings-header">
        <div>
          <div className="eyebrow">CONFIGURATION</div>
          <h1 className="page-title">Connexions et sauvegardes</h1>
          <p className="page-subtitle">
            Chaque base regroupe sa connexion et la source de ses sauvegardes.
          </p>
        </div>
        <button
          className="button primary"
          id="connection-add"
          disabled={busy}
          onClick={() => {
            setConnectionForm({ isNew: true, value: emptyConnection() });
            setSourceForm(null);
          }}
        >
          + Ajouter une base
        </button>
      </header>
      {data.pendingRestart && (
        <div className="session-warning settings-pending" role="status">
          <span>
            Modifications enregistrées. Redémarre le serveur pour les appliquer.
          </span>
          <button
            className="button primary"
            id="settings-restart"
            disabled={restarting}
            onClick={() => void restart()}
          >
            {restarting ? "Redémarrage…" : "Appliquer et redémarrer"}
          </button>
        </div>
      )}
      {error && (
        <div className="session-warning" role="alert">
          {error}
          <button
            className="link-button"
            aria-label="Fermer l’erreur"
            onClick={() => setError("")}
          >
            ✕
          </button>
        </div>
      )}
      {connectionForm?.isNew && connectionEditor}
      <div className="settings-connections" id="connections-table">
        {data.connections.map((connection) => {
          const backup = connection.backup;
          const target = targets.get(connection.id);
          return (
            <article
              className={`settings-connection ${selectedConnection === connection.id ? "selected-connection" : ""}`}
              id={`connection-${connection.id}`}
              data-connection={connection.id}
              data-driver={connection.driver}
              key={connection.id}
              style={
                { "--connection-color": connection.color } as CSSProperties
              }
              aria-label={connection.label}
            >
              <header className="connection-card-header">
                <span className="connection-symbol">
                  <Icon name="database" />
                </span>
                <div>
                  <h2>{connection.label}</h2>
                  <span className="connection-driver">
                    {connection.driver === "postgresql"
                      ? "PostgreSQL"
                      : "MySQL"}{" "}
                    · <code>{connection.id}</code>
                  </span>
                </div>
                <span
                  className={`access-badge ${connection.allowWrites ? "writable" : ""}`}
                >
                  {connection.allowWrites
                    ? "Écritures autorisées"
                    : "Lecture seule"}
                </span>
                <div className="settings-actions">
                  <button
                    className="button"
                    disabled={busy}
                    onClick={() => {
                      const { backup: _backup, ...value } = connection;
                      void _backup;
                      setConnectionForm({
                        isNew: false,
                        value: { ...value, password: "" },
                      });
                      setSourceForm(null);
                    }}
                  >
                    Modifier la base
                  </button>
                  <button
                    className="button danger"
                    aria-label={`Supprimer la base ${connection.label}`}
                    disabled={busy}
                    onClick={() => {
                      if (
                        confirm(
                          `Supprimer la connexion « ${connection.label} » et sa source de sauvegarde ? Le mot de passe reste dans .env.`,
                        )
                      )
                        void run(() =>
                          request<SaveResult>("/api/connections/delete", {
                            id: connection.id,
                          }),
                        );
                    }}
                  >
                    Supprimer
                  </button>
                </div>
              </header>
              <dl className="connection-details">
                <div>
                  <dt>Serveur</dt>
                  <dd>
                    {connection.host}:{connection.port}
                  </dd>
                </div>
                <div>
                  <dt>Base de données</dt>
                  <dd>{connection.database}</dd>
                </div>
                <div>
                  <dt>Utilisateur</dt>
                  <dd>{connection.user}</dd>
                </div>
                <div>
                  <dt>Catalogue</dt>
                  <dd>
                    {tables.filter((t) => t.engine === connection.id).length}{" "}
                    tables
                  </dd>
                </div>
              </dl>
              {connectionForm &&
                !connectionForm.isNew &&
                connectionForm.value.id === connection.id &&
                connectionEditor}
              <section
                className="linked-backup"
                aria-label={`Sauvegarde de ${connection.label}`}
              >
                <div className="backup-connector" aria-hidden="true" />
                <div className="linked-backup-heading">
                  <span className="backup-symbol">
                    <Icon name="backup" />
                  </span>
                  <div>
                    <h3>Sauvegarde de {connection.label}</h3>
                    <p>
                      {backup
                        ? backup.type === "s3-versioned"
                          ? "Source S3 versionnée"
                          : "Dossier local"
                        : "Aucune source associée à cette base"}
                    </p>
                  </div>
                  <button
                    className="button"
                    disabled={busy}
                    onClick={() => {
                      startSource(connection.id);
                      setConnectionForm(null);
                    }}
                  >
                    {backup ? "Modifier la source" : "Associer une sauvegarde"}
                  </button>
                </div>
                {backup && (
                  <>
                    <div className="backup-location">
                      <code>
                        {backup.type === "s3-versioned"
                          ? `s3://${backup.bucket}/${backup.key}`
                          : `${backup.path}/${backup.pattern}`}
                      </code>
                      <span className="badge">
                        {target
                          ? target.error
                            ? `Échec : ${target.error.message}`
                            : `${target.versions.length} versions en cache`
                          : "Versions non chargées"}
                      </span>
                    </div>
                    <details className="backup-options">
                      <summary>Détails de restauration</summary>
                      <dl>
                        <dt>Dump local</dt>
                        <dd>
                          <code>{backup.localPath || "Chemin par défaut"}</code>
                        </dd>
                        <dt>Commande</dt>
                        <dd>
                          <code>
                            {backup.restoreCommand ||
                              "Non configurée : restauration désactivée"}
                          </code>
                        </dd>
                      </dl>
                      <p className="settings-note">
                        Le chemin et la commande de restauration se règlent dans
                        le fichier de configuration.
                      </p>
                      <button
                        className="button danger"
                        disabled={busy}
                        onClick={() => {
                          if (
                            confirm(
                              `Supprimer la source de sauvegarde de « ${connection.label} » ?`,
                            )
                          )
                            void run(() =>
                              request<SaveResult>("/api/dump-sources/delete", {
                                connection: connection.id,
                              }),
                            );
                        }}
                      >
                        Dissocier la sauvegarde
                      </button>
                    </details>
                  </>
                )}
                {sourceForm?.value.connection === connection.id && sourceEditor}
              </section>
            </article>
          );
        })}
      </div>
      <details className="configuration-details">
        <summary>Fichier de configuration et hôtes autorisés</summary>
        <p>
          <code>{data.configPath}</code>
        </p>
        <p>Hôtes autorisés : {data.allowedDbHosts.join(", ")}</p>
        <p>
          Les mots de passe sont conservés dans le fichier .env voisin et ne
          sont jamais réaffichés.
        </p>
      </details>
    </div>
  );
}
