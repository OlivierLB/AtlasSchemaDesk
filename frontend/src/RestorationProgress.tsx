import { useEffect, useState } from "react";
import type { Job } from "./model";

const phases = [
  ["preparing", "Préparation"],
  ["downloading", "Téléchargement"],
  ["restoring", "Restauration"],
  ["finalizing", "Finalisation"],
  ["catalog", "Catalogue"],
] as const;
const descriptions: Record<string, string> = {
  preparing: "Vérification de la sauvegarde et de l’espace disponible",
  downloading: "Transfert du fichier de sauvegarde",
  restoring: "Restauration de la base — suivre les détails dans le journal",
  finalizing: "Enregistrement de la sauvegarde restaurée",
  catalog: "Lecture des tables et actualisation du catalogue",
  finished: "Sauvegarde restaurée et catalogue actualisé",
};
function size(bytes: number) {
  if (bytes < 1024 ** 2) return `${(bytes / 1024).toFixed(0)} Kio`;
  if (bytes < 1024 ** 3) return `${(bytes / 1024 ** 2).toFixed(1)} Mio`;
  return `${(bytes / 1024 ** 3).toFixed(2)} Gio`;
}

export function RestorationProgress({
  job,
  compact = false,
}: {
  job: Job;
  compact?: boolean;
}) {
  const active = job.state === "running" || job.state === "cancelling";
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (!active) return;
    const timer = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(timer);
  }, [active, job.startedAt]);
  const phase = job.context?.phase || "preparing";
  const current =
    job.state === "success"
      ? phases.length
      : phases.findIndex(([key]) => key === phase);
  const total = Number(job.context?.totalBytes || 0);
  const bytes = Math.min(Number(job.context?.bytes || 0), total);
  const seconds =
    job.durationSeconds ??
    (job.startedAt
      ? Math.max(0, Math.floor((now - Date.parse(job.startedAt)) / 1000))
      : 0);
  const elapsed =
    seconds < 60
      ? `${Math.floor(seconds)} s`
      : `${Math.floor(seconds / 60)} min ${Math.floor(seconds % 60)} s`;
  const description =
    job.state === "cancelling"
      ? "Arrêt des processus et nettoyage des fichiers temporaires…"
      : job.state === "success"
        ? descriptions.finished
        : descriptions[phase] || "Démarrage de la restauration";
  return (
    <div className={`restoration-progress ${compact ? "compact" : ""}`}>
      {!compact && (
        <ol className="restore-steps" aria-label="Étapes de la restauration">
          {phases.map(([key, label], index) => (
            <li
              key={key}
              className={
                index < current ? "done" : index === current ? "current" : ""
              }
              aria-current={index === current && active ? "step" : undefined}
            >
              <span aria-hidden="true">
                {index < current ? "✓" : index + 1}
              </span>
              {label}
            </li>
          ))}
        </ol>
      )}
      <div className="restore-phase">
        <span role="status">{description}</span>
        <time>{elapsed}</time>
      </div>
      {active &&
        (phase === "downloading" && total > 0 ? (
          <>
            <progress
              max={total}
              value={bytes}
              aria-label="Transfert de la sauvegarde"
            />
            <span className="restore-transfer">
              {size(bytes)} / {size(total)} ·{" "}
              {Math.floor((bytes / total) * 100)} %
            </span>
          </>
        ) : (
          <progress aria-label="Restauration en cours, progression non mesurable pour cette étape" />
        ))}
      {!compact && active && phase === "restoring" && (
        <p className="section-help">
          Le fichier est prêt. Cette étape peut rester silencieuse pendant le
          travail du moteur de base de données. Le temps écoulé et le journal
          restent actualisés. Une autorisation éventuelle s’affiche dans une
          fenêtre système.
        </p>
      )}
    </div>
  );
}
