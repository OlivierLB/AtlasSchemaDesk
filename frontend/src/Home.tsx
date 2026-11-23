import type { CSSProperties } from "react";
import { useQuery } from "@tanstack/react-query";
import { request } from "./api";
import { useAtlas } from "./context";
import { href, stored, tableHref } from "./model";
import type { ConnectionsOverview } from "./model";
import { Icon } from "./Icon";

export function Home() {
  const { config, tables } = useAtlas();
  const overview = useQuery({
    queryKey: ["connections"],
    queryFn: () =>
      request<ConnectionsOverview>("/api/connections", undefined, false),
  });
  const recent = stored<{ engine: string; schema: string; table: string }[]>(
    "db-atlas-recent-tables",
    [],
  ).flatMap((item) => {
    if (!item || typeof item !== "object") return [];
    const table = tables.find(
      (t) =>
        t.engine === item.engine &&
        t.schema === item.schema &&
        t.name === item.table,
    );
    return table ? [table] : [];
  });
  return (
    <div className="page home-page">
      <header className="page-header home-header">
        <div className="eyebrow">VOTRE ESPACE DE TRAVAIL</div>
        <h1 className="page-title">Vos bases de données</h1>
        <p className="page-subtitle">
          Choisis un schéma pour explorer ses tables, comprendre leurs liens ou
          consulter les données.
        </p>
      </header>
      {recent.length > 0 && (
        <section className="recent-section" aria-label="Tables récentes">
          <h2 className="section-title">Reprendre l’exploration</h2>
          <div className="recent-links">
            {recent.map((table) => (
              <a
                className="recent-link"
                key={`${table.engine}:${table.schema}:${table.name}`}
                href={tableHref(table)}
              >
                <Icon name="table" />
                <span>
                  <small>
                    {
                      config.connections.find((c) => c.id === table.engine)
                        ?.label
                    }{" "}
                    / {table.schema}
                  </small>
                  <strong>{table.name}</strong>
                </span>
                <Icon name="arrow" />
              </a>
            ))}
          </div>
        </section>
      )}
      <div className="section-heading">
        <h2 className="section-title">
          Bases disponibles{" "}
          <span className="count-badge">{config.connections.length}</span>
        </h2>
        <a className="quiet-link" href="#/connexions">
          <Icon name="settings" />
          Gérer les connexions
        </a>
      </div>
      {config.connections.length === 0 && (
        <div className="empty-state first-run">
          <h2>Aucune base configurée</h2>
          <p>
            Ajoute ta première base PostgreSQL ou MySQL : son adresse, son
            utilisateur et son mot de passe suffisent.
          </p>
          <a className="button primary" href="#/connexions?ajouter=1">
            + Ajouter une base
          </a>
        </div>
      )}
      <div className="connection-grid">
        {config.connections.map((connection) => {
          const list = tables.filter((t) => t.engine === connection.id);
          const schemas = [...new Set(list.map((t) => t.schema))].sort();
          const saved = overview.data?.connections.find(
            (c) => c.id === connection.id,
          );
          return (
            <article
              className="connection-card"
              key={connection.id}
              style={
                { "--connection-color": connection.color } as CSSProperties
              }
            >
              <header className="connection-card-header">
                <span className="connection-symbol">
                  <Icon name="database" />
                </span>
                <div>
                  <h3>{connection.label}</h3>
                  <span className="connection-driver">
                    {connection.driver === "postgresql"
                      ? "PostgreSQL"
                      : "MySQL"}
                  </span>
                </div>
                <span
                  className={`access-badge ${connection.allow_writes ? "writable" : ""}`}
                >
                  {connection.allow_writes
                    ? "Écritures autorisées"
                    : "Lecture seule"}
                </span>
              </header>
              <div className="connection-metrics">
                <span>
                  <strong>{list.length}</strong>{" "}
                  {list.length === 1 ? "table" : "tables"}
                </span>
                <span>
                  <strong>{schemas.length}</strong>{" "}
                  {schemas.length === 1 ? "schéma" : "schémas"}
                </span>
                <a href={href({ view: "sql", engine: connection.id })}>
                  <Icon name="sql" />
                  Ouvrir SQL
                </a>
              </div>
              <div className="schema-links">
                {schemas.map((schema) => (
                  <a
                    key={schema}
                    href={href({
                      view: "schema",
                      engine: connection.id,
                      schema,
                    })}
                  >
                    <span className="schema-name">{schema}</span>
                    <span className="schema-table-count">
                      {list.filter((t) => t.schema === schema).length}{" "}
                      {list.filter((t) => t.schema === schema).length === 1
                        ? "table"
                        : "tables"}
                    </span>
                    <span className="schema-explore">
                      Explorer <Icon name="arrow" />
                    </span>
                  </a>
                ))}
                {!schemas.length && (
                  <div className="card-empty">
                    Aucun schéma chargé. Actualise le catalogue ou ouvre la
                    console SQL.
                  </div>
                )}
              </div>
              <a
                className="connection-backup-link"
                href={`#/connexions?connection=${encodeURIComponent(connection.id)}`}
              >
                <Icon name="backup" />
                <span>
                  {saved
                    ? saved.backup
                      ? `Sauvegarde liée · ${saved.backup.type === "s3-versioned" ? "S3" : "Dossier local"}`
                      : "Associer une source de sauvegarde"
                    : "Connexion et sauvegarde"}
                </span>
                <Icon name="arrow" />
              </a>
            </article>
          );
        })}
      </div>
      {!tables.length && config.connections.length > 0 && (
        <div className="empty-guidance">
          <Icon name="database" />
          <div>
            <h2>Aucun schéma chargé</h2>
            <p>
              La console SQL reste disponible. Vérifie tes connexions puis
              actualise le catalogue pour afficher les tables.
            </p>
            <a className="button" href="#/connexions">
              Vérifier les connexions
            </a>
          </div>
        </div>
      )}
      <div className="workflow-guide">
        <span className="guide-step">
          <b>1</b>Choisir un schéma
        </span>
        <Icon name="arrow" />
        <span className="guide-step">
          <b>2</b>Ouvrir une table
        </span>
        <Icon name="arrow" />
        <span className="guide-step">
          <b>3</b>Explorer sa structure et ses données
        </span>
      </div>
    </div>
  );
}
