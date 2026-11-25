import { useEffect, useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { request } from "./api";
import { useSulcata } from "./context";
import { errorText } from "./model";
import type { ConnectionsOverview, Dumps, Job, VersionIndex } from "./model";
import { Icon } from "./Icon";
import { RestorationProgress } from "./RestorationProgress";

const jobLabels: Record<Job["state"], string> = {
  idle: "En attente",
  running: "En cours",
  cancelling: "Annulation en cours",
  success: "Terminé",
  error: "Échec",
  cancelled: "Annulé",
  timed_out: "Délai dépassé",
};
const inProgress = (job?: Job) =>
  job?.state === "running" || job?.state === "cancelling";

export function Maintenance({ onClose }: { onClose: () => void }) {
  const { config, status, selectedVersions, selectVersion, expired, route } =
    useSulcata();
  const client = useQueryClient();
  const [engine, setEngine] = useState(
    route.engine || config.connections[0]?.id || "",
  );
  const [section, setSection] = useState(() =>
    status?.apply.id && status.apply.state !== "idle" ? "operations" : "schema",
  );
  const [confirm, setConfirm] = useState<{
    engine: string;
    versionId: string;
  } | null>(null);
  const [confirmation, setConfirmation] = useState("");
  const [pending, setPending] = useState(false);
  const [operationError, setOperationError] = useState("");
  const dialog = useRef<HTMLElement>(null);
  const closeButton = useRef<HTMLButtonElement>(null);
  const confirmationInput = useRef<HTMLInputElement>(null);
  useEffect(() => {
    const opener = document.activeElement as HTMLElement | null;
    closeButton.current?.focus();
    return () => {
      if (opener?.isConnected) opener.focus();
      else document.getElementById("versions-toggle")?.focus();
    };
  }, []);
  useEffect(() => {
    if (confirm) confirmationInput.current?.focus();
  }, [confirm]);
  const versions = useQuery({
    queryKey: ["versions", status?.finishedAt],
    queryFn: () =>
      request<VersionIndex>(
        `/data/snapshots/index.json?t=${Date.now()}`,
        undefined,
        false,
      ),
  });
  const dumps = useQuery({
    queryKey: ["dumps", status?.s3.finishedAt],
    queryFn: () =>
      request<Dumps>(`/data/s3-dumps.json?t=${Date.now()}`, undefined, false),
    enabled: config.modules.dumps,
  });
  const overview = useQuery({
    queryKey: ["connections"],
    queryFn: () =>
      request<ConnectionsOverview>("/api/connections", undefined, false),
  });
  const applied = useQuery({
    queryKey: ["applied", status?.apply.finishedAt],
    queryFn: () =>
      request<Record<string, { versionId: string; appliedAt: string }>>(
        "/data/applied-dumps.json",
        undefined,
        false,
      ),
    enabled: config.modules.dumps,
  });
  const operation = async (path: string, body: unknown) => {
    setPending(true);
    setOperationError("");
    try {
      await request(path, body);
      await client.invalidateQueries({ queryKey: ["status"] });
      return true;
    } catch (error) {
      setOperationError(errorText(error));
      return false;
    } finally {
      setPending(false);
    }
  };
  const connection = config.connections.find((c) => c.id === engine);
  const backup = overview.data?.connections.find(
    (c) => c.id === engine,
  )?.backup;
  const target = dumps.data?.targets.find((t) => t.engine === engine);
  const seen = new Set<string>();
  const history = [...(versions.data?.versions || [])]
    .reverse()
    .filter((version) => {
      const hash = version.sources[engine]?.hash;
      if (!hash || seen.has(hash)) return false;
      seen.add(hash);
      return true;
    });
  const lastApplied = applied.data?.[engine];
  const restoreBlocked = pending || expired || inProgress(status?.apply);
  return (
    <div className="maintenance-layer">
      <button
        className="drawer-backdrop"
        tabIndex={-1}
        aria-label="Fermer les versions"
        onClick={onClose}
      />
      <section
        ref={dialog}
        className="maintenance-panel redesigned-maintenance"
        role="dialog"
        aria-modal="true"
        aria-label="Versions des bases"
        onKeyDown={(event) => {
          if (event.key === "Escape") {
            event.stopPropagation();
            if (confirm) setConfirm(null);
            else onClose();
          }
          if (event.key === "Tab") {
            const controls = [
              ...(dialog.current?.querySelectorAll<HTMLElement>(
                "a[href], button:not(:disabled), input:not(:disabled), select:not(:disabled), summary",
              ) || []),
            ].filter((node) => node.getClientRects().length);
            const first = controls[0],
              last = controls.at(-1);
            if (event.shiftKey && document.activeElement === first) {
              event.preventDefault();
              last?.focus();
            } else if (!event.shiftKey && document.activeElement === last) {
              event.preventDefault();
              first?.focus();
            }
          }
        }}
      >
        <header className="drawer-heading">
          <div>
            <div className="eyebrow">MAINTENANCE</div>
            <h2>Versions et restauration</h2>
            <p>Schémas, sauvegardes et opérations de chaque base.</p>
          </div>
          <button ref={closeButton} className="button" onClick={onClose}>
            Fermer <Icon name="close" />
          </button>
        </header>
        <div className="maintenance-context">
          <label htmlFor="maintenance-engine">Base de données</label>
          <select
            id="maintenance-engine"
            value={engine}
            onChange={(event) => {
              setEngine(event.target.value);
              setConfirm(null);
              setConfirmation("");
            }}
          >
            {config.connections.map((c) => (
              <option key={c.id} value={c.id}>
                {c.label}
              </option>
            ))}
          </select>
          <Icon name="database" />
        </div>
        <nav
          className="segmented-control maintenance-tabs"
          aria-label="Sections de maintenance"
        >
          {[
            ["schema", "Schémas"],
            ["backups", "Sauvegardes"],
            ["operations", "Opérations"],
          ].map(([key, label]) => (
            <button
              key={key}
              className={section === key ? "active" : ""}
              aria-pressed={section === key}
              onClick={() => {
                setSection(key);
                setConfirm(null);
              }}
            >
              {label}
              {key === "operations" &&
                (inProgress(status) ||
                  inProgress(status?.s3) ||
                  inProgress(status?.apply)) && (
                  <i className="status-dot loading" />
                )}
            </button>
          ))}
        </nav>
        <div className="drawer-content">
          {operationError && (
            <div className="feedback-card error" role="alert">
              {operationError}
              <button
                className="link-button"
                aria-label="Fermer l’erreur d’opération"
                onClick={() => setOperationError("")}
              >
                Fermer
              </button>
            </div>
          )}
          {section === "schema" && (
            <section aria-label="Historique des schémas">
              <div className="section-heading">
                <h3 className="section-title">
                  Schéma affiché · {connection?.label}
                </h3>
                <span className="state-badge neutral">
                  {selectedVersions[engine] ? "Historique" : "Dernier export"}
                </span>
              </div>
              <div className="info-callout warning">
                Le schéma historique sert à comparer la structure. Les lectures
                et les écritures utilisent toujours les données de la base
                actuelle.
              </div>
              {versions.isPending && (
                <p role="status">Chargement de l’historique…</p>
              )}
              {versions.error && (
                <p className="feedback-card error" role="alert">
                  Historique indisponible : {errorText(versions.error)}
                </p>
              )}
              <label className="stacked-field">
                Version à afficher
                <select
                  aria-label={`Version de ${engine}`}
                  value={selectedVersions[engine] || ""}
                  onChange={(event) =>
                    selectVersion(engine, event.target.value)
                  }
                >
                  <option value="">Dernier export</option>
                  {history.map((version) => (
                    <option
                      key={version.sources[engine].hash}
                      value={version.sources[engine].hash}
                    >
                      {new Date(version.generatedAt).toLocaleString("fr")} ·{" "}
                      {version.sources[engine].tables} tables ·{" "}
                      {version.sources[engine].hash.slice(0, 8)}
                    </option>
                  ))}
                </select>
              </label>
              {selectedVersions[engine] && (
                <button
                  className="button"
                  onClick={() => selectVersion(engine, "")}
                >
                  Revenir au dernier export
                </button>
              )}
              {!versions.isPending && !versions.error && !history.length && (
                <div className="compact-empty">
                  Aucune version historique enregistrée pour cette base.
                </div>
              )}
              <p className="section-help">
                Pour obtenir une nouvelle version du catalogue, utilise
                Actualiser dans la barre supérieure.
              </p>
            </section>
          )}
          {section === "backups" && (
            <section aria-label={`Sauvegardes de ${connection?.label}`}>
              <div className="section-heading">
                <h3 className="section-title">
                  Sauvegardes de {connection?.label}
                </h3>
                <button
                  className="button"
                  disabled={
                    pending ||
                    expired ||
                    inProgress(status?.s3) ||
                    !config.modules.dumps
                  }
                  onClick={() => {
                    void operation("/api/s3/refresh", {});
                  }}
                >
                  <Icon name="refresh" />
                  Actualiser les sauvegardes
                </button>
              </div>
              <div className="backup-source-card">
                <Icon name="backup" />
                <div>
                  <strong>
                    {backup
                      ? backup.type === "s3-versioned"
                        ? "Source S3 liée à cette base"
                        : "Dossier lié à cette base"
                      : "Source de sauvegarde"}
                  </strong>
                  {backup && (
                    <code>
                      {backup.type === "s3-versioned"
                        ? `s3://${backup.bucket}/${backup.key}`
                        : `${backup.path}/${backup.pattern}`}
                    </code>
                  )}
                  <a
                    href={`#/connexions?connection=${encodeURIComponent(engine)}`}
                    onClick={onClose}
                  >
                    Gérer cette connexion et sa sauvegarde <Icon name="arrow" />
                  </a>
                </div>
              </div>
              {overview.error && (
                <p className="feedback-card error" role="alert">
                  Configuration de la source indisponible :{" "}
                  {errorText(overview.error)}
                </p>
              )}
              {lastApplied && (
                <p className="info-callout">
                  Dernière restauration :{" "}
                  {new Date(lastApplied.appliedAt).toLocaleString("fr")}
                </p>
              )}
              {applied.error && (
                <p className="section-help">
                  L’historique des restaurations est indisponible.
                </p>
              )}
              {dumps.isFetching && (
                <p role="status">Lecture des sauvegardes en cache…</p>
              )}
              {inProgress(status?.s3) && (
                <p className="info-callout" role="status">
                  Actualisation des sauvegardes en cours.
                </p>
              )}
              {dumps.error && (
                <p className="feedback-card error" role="alert">
                  Sauvegardes indisponibles : {errorText(dumps.error)}
                </p>
              )}
              {target?.error && (
                <p className="feedback-card error" role="alert">
                  {target.error.message}
                </p>
              )}
              {confirm ? (
                <section className="sql-confirm restore-confirm" role="alert">
                  <div className="eyebrow">REMPLACEMENT DES DONNÉES</div>
                  <h3>Restaurer {connection?.label}</h3>
                  <p>
                    Cette restauration remplace les données de{" "}
                    <strong>{confirm.engine}</strong>. Une interruption peut
                    laisser la base partiellement restaurée. Ferme les
                    transactions de tous les onglets.
                  </p>
                  <p className="section-help">
                    Version : <code>{confirm.versionId}</code>
                  </p>
                  <label className="stacked-field">
                    Saisis {confirm.engine} pour confirmer
                    <input
                      ref={confirmationInput}
                      value={confirmation}
                      autoComplete="off"
                      onChange={(event) => setConfirmation(event.target.value)}
                    />
                  </label>
                  <div className="action-group">
                    <button
                      className="button danger"
                      disabled={
                        confirmation !== confirm.engine || restoreBlocked
                      }
                      onClick={() => {
                        void operation("/api/apply", confirm).then(
                          (started) => {
                            if (started) {
                              setConfirm(null);
                              setSection("operations");
                            }
                          },
                        );
                      }}
                    >
                      Confirmer la restauration
                    </button>
                    <button
                      className="button"
                      disabled={pending}
                      onClick={() => setConfirm(null)}
                    >
                      Renoncer
                    </button>
                  </div>
                </section>
              ) : (
                <div className="backup-version-list">
                  {target?.versions.map((version) => (
                    <article className="backup-version" key={version.versionId}>
                      <div>
                        <strong>
                          {new Date(version.lastModified).toLocaleString("fr")}
                        </strong>
                        <span>
                          {(version.size / 1024 ** 2).toFixed(1)} Mo ·{" "}
                          <code title={version.versionId}>
                            {version.versionId}
                          </code>
                        </span>
                      </div>
                      <button
                        className="button"
                        disabled={restoreBlocked}
                        onClick={() => {
                          setConfirm({ engine, versionId: version.versionId });
                          setConfirmation("");
                        }}
                      >
                        Restaurer
                      </button>
                    </article>
                  ))}
                </div>
              )}
              {!confirm &&
                !dumps.isFetching &&
                !target?.versions.length &&
                !target?.error &&
                !dumps.error && (
                  <div className="compact-empty">
                    {backup || target
                      ? "Aucune sauvegarde listée pour cette base. Actualise la liste pour interroger sa source."
                      : "Associe une source S3 ou un dossier depuis la fiche de cette base pour retrouver ses sauvegardes ici."}
                  </div>
                )}
              {inProgress(status?.apply) && (
                <button
                  className="button"
                  onClick={() => setSection("operations")}
                >
                  Suivre la restauration
                </button>
              )}
            </section>
          )}
          {section === "operations" && (
            <section aria-label="Suivi des opérations">
              <h3 className="section-title">Opérations du serveur</h3>
              <p className="section-help">
                Le catalogue et les sauvegardes sont partagés entre les onglets.
                Les journaux donnent l’état des dernières opérations.
              </p>
              {status ? (
                <div className="jobs-list">
                  <JobView
                    label="Restauration"
                    job={status.apply}
                    pending={pending}
                    cancel={(id) => {
                      void operation(`/api/jobs/${id}/cancel`, {});
                    }}
                  />
                  <JobView
                    label="Export du catalogue"
                    job={status}
                    pending={pending}
                    cancel={(id) => {
                      void operation(`/api/jobs/${id}/cancel`, {});
                    }}
                  />
                  <JobView
                    label="Liste des sauvegardes"
                    job={status.s3}
                    pending={pending}
                    cancel={(id) => {
                      void operation(`/api/jobs/${id}/cancel`, {});
                    }}
                  />
                </div>
              ) : (
                <p role="status">État des opérations indisponible.</p>
              )}
              <section className="session-close-card">
                <h3>Session de cet onglet</h3>
                <p>
                  Fermer cette session annule ses transactions non validées. Une
                  nouvelle session sera nécessaire pour continuer.
                </p>
                <button
                  className="button danger"
                  disabled={pending || expired}
                  onClick={() => {
                    void operation("/api/sessions/close", {}).then((closed) => {
                      if (closed) {
                        window.dispatchEvent(
                          new Event("sulcata-session-expired"),
                        );
                        onClose();
                      }
                    });
                  }}
                >
                  Annuler les transactions et fermer la session
                </button>
              </section>
            </section>
          )}
        </div>
      </section>
    </div>
  );
}

function JobView({
  label,
  job,
  pending,
  cancel,
}: {
  label: string;
  job: Job;
  pending: boolean;
  cancel: (id: string) => void;
}) {
  const log = useRef<HTMLPreElement>(null);
  const follow = useRef(true);
  useEffect(() => {
    follow.current = true;
  }, [job.id]);
  useEffect(() => {
    if (follow.current && log.current)
      log.current.scrollTop = log.current.scrollHeight;
  }, [job.log]);
  return (
    <article
      className={`job-card ${label === "Restauration" && job.state !== "idle" ? "restore-job" : ""}`}
    >
      <header>
        <div>
          <h4>{label}</h4>
          <span className="section-help">
            {job.durationSeconds != null
              ? `${job.durationSeconds} s`
              : inProgress(job)
                ? "Suivi en direct"
                : "Aucune durée disponible"}
            {job.context?.engine ? ` · ${job.context.engine}` : ""}
          </span>
        </div>
        <span
          className={`state-badge ${job.state === "success" ? "success" : ["error", "timed_out", "cancelled"].includes(job.state) ? "error" : inProgress(job) ? "warning" : "neutral"}`}
        >
          {jobLabels[job.state]}
        </span>
      </header>
      {label === "Restauration" && job.state !== "idle" && (
        <RestorationProgress job={job} />
      )}
      {job.id && inProgress(job) && (
        <button
          className="button danger"
          disabled={pending || job.state === "cancelling"}
          onClick={() => cancel(job.id!)}
        >
          Annuler l’opération
        </button>
      )}
      {label === "Restauration" &&
        ["cancelled", "timed_out", "error"].includes(job.state) && (
          <p className="feedback-card error" role="alert">
            La base peut être partiellement restaurée. Actualise le catalogue et
            vérifie les données avant de poursuivre.
          </p>
        )}
      <details
        className="job-details"
        open={
          inProgress(job) || job.state === "error" || job.state === "timed_out"
        }
      >
        <summary>Journal de l’opération</summary>
        <pre
          ref={log}
          className="job-log code-surface"
          aria-label={`Journal : ${label}`}
          tabIndex={0}
          onScroll={(event) => {
            const node = event.currentTarget;
            follow.current =
              node.scrollHeight - node.scrollTop - node.clientHeight < 48;
          }}
        >
          {job.log ||
            (inProgress(job)
              ? "L’opération a démarré. En attente de la première sortie de la commande…"
              : "Aucune sortie.")}
        </pre>
      </details>
    </article>
  );
}
