import { useEffect, useRef, useState } from "react";
import { useAtlas } from "./context";
import { tableHref } from "./model";
import type { Program, Programs } from "./model";
import { Icon } from "./Icon";

const impactLabels = {
  writes: "Tables modifiées",
  indirectWrites: "Modifications via d’autres programmes",
  probableWrites: "Modifications probables · SQL dynamique",
  reads: "Tables consultées",
  probableReads: "Lectures probables · SQL dynamique",
  calls: "Programmes appelés",
  calledBy: "Appelé par",
} as const;

export function ProgramDetail({
  program,
  close,
  catalog,
}: {
  program: Program;
  close: () => void;
  catalog?: Programs;
}) {
  const { programs, tables, config, openProgram, notice } = useAtlas();
  const [tab, setTab] = useState("code");
  const heading = useRef<HTMLHeadingElement>(null);
  useEffect(() => {
    const opener = document.activeElement as HTMLElement | null;
    return () => {
      if (opener?.isConnected) opener.focus();
    };
  }, []);
  useEffect(() => {
    heading.current?.focus();
  }, [program.id]);
  const engine = program.id.split(":")[0];
  const owner =
    config.connections.find((c) => c.id === engine)?.label ||
    (engine === "reference" ? "Sources SQL" : engine);
  const all = Object.values((catalog || programs)?.engines || {}).flatMap(
    (s) => s.programs,
  );
  const groups = Object.entries(impactLabels).flatMap(([key, label]) => {
    const ids = program.impact[key as keyof typeof impactLabels] || [];
    return ids.length ? [{ key, label, ids }] : [];
  });
  const facts = [
    ["Signature", program.signature],
    ["Retour", program.returns],
    ["Langage", program.language],
    [
      "Déclenchement",
      [program.timing, program.events?.join(" / ")].filter(Boolean).join(" · "),
    ],
    ["Planification", program.schedule],
    ["État", program.status],
  ];
  return (
    <div id="detail" className="inspector-body program-inspector">
      <header className="program-heading">
        <div>
          <div className="eyebrow">
            {program.kind === "event"
              ? "ÉVÉNEMENT PLANIFIÉ"
              : program.kind === "trigger"
                ? "DÉCLENCHEUR SQL"
                : "PROGRAMME SQL"}
          </div>
          <h2 ref={heading} tabIndex={-1} className="detail-title">
            {program.name}
          </h2>
          <p>
            {owner} / {program.schema}
          </p>
        </div>
        <button
          className="button icon-button"
          onClick={close}
          aria-label="Fermer la fiche"
        >
          <Icon name="close" />
        </button>
      </header>
      {program.comment && (
        <p className="program-description">{program.comment}</p>
      )}
      <nav
        className="segmented-control inspector-tabs"
        aria-label="Sections du programme"
      >
        {[
          ["code", "Code SQL"],
          ["dependencies", "Dépendances"],
          ["source", "Source"],
        ].map(([key, label]) => (
          <button
            key={key}
            aria-pressed={tab === key}
            className={tab === key ? "active" : ""}
            onClick={() => setTab(key)}
          >
            {label}
          </button>
        ))}
      </nav>
      {program.impact.dynamicSql && (
        <div className="info-callout warning" role="note">
          SQL dynamique : certaines dépendances sont probables et doivent être
          vérifiées dans le code.
        </div>
      )}
      {tab === "code" && (
        <section className="inspector-section">
          <dl className="facts-grid">
            {facts
              .filter(([, value]) => value)
              .map(([label, value]) => (
                <div key={label}>
                  <dt>{label}</dt>
                  <dd>{value}</dd>
                </div>
              ))}
          </dl>
          <div className="section-heading">
            <h3 className="section-title">Définition SQL</h3>
            <button
              className="button"
              disabled={!program.body}
              onClick={() => {
                void navigator.clipboard
                  .writeText(program.body)
                  .then(() => notice("Code copié"))
                  .catch(() => notice("Copie impossible"));
              }}
            >
              Copier le code
            </button>
          </div>
          <pre className="program-body code-surface">
            {program.body || "Aucune définition disponible dans le catalogue."}
          </pre>
        </section>
      )}
      {tab === "dependencies" && (
        <section className="inspector-section">
          <p className="section-help">
            Suis une table ou un programme pour comprendre ses interactions.
          </p>
          {groups.map(({ key, label, ids }) => (
            <details className="dependency-group" key={key} open>
              <summary>
                {label}
                <span className="count-badge">{ids.length}</span>
              </summary>
              <div className="dependency-links">
                {ids.map((id) => {
                  const target = all.find((p) => p.id === id);
                  const table = tables.find(
                    (t) =>
                      t.engine === engine && `${t.schema}.${t.name}` === id,
                  );
                  return target ? (
                    <button
                      className="dependency-link"
                      key={id}
                      onClick={() => openProgram(target, catalog)}
                    >
                      <Icon name="sql" />
                      <span>
                        {target.schema}.{target.name}
                      </span>
                      <Icon name="arrow" />
                    </button>
                  ) : table ? (
                    <a
                      className="dependency-link"
                      key={id}
                      href={tableHref(table)}
                    >
                      <Icon name="table" />
                      <span>{id}</span>
                      <Icon name="arrow" />
                    </a>
                  ) : (
                    <p className="unresolved-reference" key={id}>
                      {id}
                      <small>Référence absente du catalogue</small>
                    </p>
                  );
                })}
              </div>
            </details>
          ))}
          {!groups.length && (
            <div className="compact-empty">
              Aucune dépendance identifiée dans le catalogue.
            </div>
          )}
        </section>
      )}
      {tab === "source" && (
        <section className="inspector-section">
          <h3 className="section-title">Fichier source</h3>
          {program.source ? (
            <>
              <dl className="facts-grid">
                <div>
                  <dt>Chemin</dt>
                  <dd>
                    <code>{program.source.path || "Non renseigné"}</code>
                  </dd>
                </div>
                {program.source.line && (
                  <div>
                    <dt>Ligne</dt>
                    <dd>{program.source.line}</dd>
                  </div>
                )}
                <div>
                  <dt>Correspondance</dt>
                  <dd>{program.source.status}</dd>
                </div>
              </dl>
              {program.source.absolutePath && (
                <button
                  className="button"
                  onClick={() => {
                    void navigator.clipboard
                      .writeText(program.source?.absolutePath || "")
                      .then(() => notice("Chemin copié"))
                      .catch(() => notice("Copie impossible"));
                  }}
                >
                  Copier le chemin
                </button>
              )}
            </>
          ) : (
            <div className="compact-empty">
              Aucun fichier source associé. La définition SQL reste consultable
              dans l’onglet Code SQL.
            </div>
          )}
        </section>
      )}
    </div>
  );
}
