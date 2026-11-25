import { lazy, Suspense, useState } from "react";
import { useSulcata } from "./context";
import { display, href, keyOf, save, stored, tableHref, tabs } from "./model";
import type { Table } from "./model";
import { DataView } from "./DataView";
import { Connections } from "./Connections";
import { Home } from "./Home";
import { Icon } from "./Icon";
import { Events } from "./Events";
export { ProgramDetail } from "./ProgramDetail";
const SqlConsole = lazy(() =>
  import("./SqlConsole").then((module) => ({ default: module.SqlConsole })),
);

const labels = {
  colonnes: "Structure",
  relations: "Relations",
  donnees: "Données",
  programmes: "Programmes SQL",
  voisinage: "Carte des relations",
  sql: "Requête SQL",
};
const impactLabels = {
  triggers: "Déclencheurs sur cette table",
  writes: "Modifiée par",
  indirectWrites: "Écrite indirectement (via appels)",
  probableWrites: "Probablement écrite (SQL dynamique)",
  reads: "Consultée par",
  probableReads: "Probablement lue (SQL dynamique)",
};
export function Explorer() {
  const { route, tables, config, programs, selectedVersions } = useSulcata();
  const table = tables.find(
    (t) =>
      t.engine === route.engine &&
      t.schema === route.schema &&
      t.name === route.table,
  );
  if (route.view === "sql")
    return (
      <Suspense fallback={<p>Chargement de la console…</p>}>
        <SqlConsole engine={route.engine || config.connections[0]?.id || ""} />
      </Suspense>
    );
  if (route.view === "events") return <Events />;
  if (route.view === "connections") return <Connections />;
  if (route.view === "table") {
    if (!table)
      return (
        <div className="page">
          <h1 className="page-title">Table introuvable</h1>
          <a href="#/">Revenir aux bases</a>
        </div>
      );
    const tab = route.tab || "colonnes";
    const stats =
      programs?.engines[table.engine]?.tableStats[
        `${table.schema}.${table.name}`
      ];
    return (
      <div className="page table-page">
        <header className="page-header table-header">
          <div className="table-kicker">
            {config.connections.find((c) => c.id === table.engine)?.label ||
              table.engine}{" "}
            / {table.schema}
          </div>
          <h1 className="page-title mono">{table.name}</h1>
          <div className="table-badges">
            <span className="badge">{table.columns.length} colonnes</span>
            <span className="badge">
              {config.connections.find((c) => c.id === table.engine)
                ?.allow_writes
                ? "Écritures autorisées"
                : "Lecture seule"}
            </span>
            {stats?.rows != null && (
              <span className="badge">
                ≈ {stats.rows.toLocaleString("fr")} lignes
              </span>
            )}
            {stats?.bytes != null && (
              <span className="badge">{bytes(stats.bytes)}</span>
            )}
            {selectedVersions[table.engine] && (
              <span className="badge">
                Schéma historique — les données restent celles de la base
                actuelle
              </span>
            )}
          </div>
        </header>
        <nav className="tabs" aria-label="Sections de la table">
          {tabs.map((name) => (
            <a
              key={name}
              href={tableHref(table, name)}
              className={`tab ${tab === name ? "active" : ""}`}
              aria-current={tab === name ? "page" : undefined}
            >
              {labels[name]}
            </a>
          ))}
        </nav>
        <div className="tab-body" id="tab-body">
          <p className="tab-description">
            {tab === "colonnes"
              ? "Les colonnes et leurs types. La clé identifie les colonnes de la clé primaire."
              : tab === "relations"
                ? "Suis les liens pour explorer les tables qui référencent celle-ci ou qu’elle utilise."
                : tab === "donnees"
                  ? "Filtre les colonnes pour retrouver une ligne. Clique sur une ligne pour voir son détail."
                  : tab === "programmes"
                    ? "Retrouve les programmes qui lisent ou modifient cette table."
                    : tab === "voisinage"
                      ? "Une vue des tables directement reliées. Clique sur une table pour poursuivre l’exploration."
                      : "Écris une requête pour cette base ; les modifications passent par une transaction explicite."}
          </p>
          {tab === "colonnes" && <Columns table={table} />}
          {tab === "relations" && <Relations table={table} />}
          {tab === "donnees" && (
            <DataView
              key={`${keyOf(table)}:${JSON.stringify(route.params || {})}`}
              table={table}
            />
          )}
          {tab === "programmes" && <TablePrograms table={table} />}
          {tab === "voisinage" && <Neighborhood table={table} />}
          {tab === "sql" && (
            <Suspense fallback={<p>Chargement de la console…</p>}>
              <SqlConsole engine={table.engine} table={table} />
            </Suspense>
          )}
        </div>
      </div>
    );
  }
  if (route.view === "schema")
    return (
      <SchemaList
        key={`${route.engine}:${route.schema}`}
        engine={route.engine || ""}
        schema={route.schema || ""}
      />
    );
  return <Home />;
}
function bytes(value: number) {
  return value >= 1024 ** 3
    ? `${(value / 1024 ** 3).toFixed(1)} Go`
    : value >= 1024 ** 2
      ? `${(value / 1024 ** 2).toFixed(1)} Mo`
      : `${Math.round(value / 1024)} Ko`;
}
function SchemaList({ engine, schema }: { engine: string; schema: string }) {
  const { tables, programs, impact, config } = useSulcata();
  const storageKey = `sulcata-schema-view:${engine}:${schema}`;
  const initial = stored(storageKey, {
    filter: "",
    column: "name",
    descending: false,
  });
  const [filter, setFilter] = useState(initial.filter);
  const [column, setColumn] = useState(initial.column);
  const [descending, setDescending] = useState(initial.descending);
  const metrics = (table: Table): Record<string, string | number> => ({
    name: table.name,
    rows:
      programs?.engines[engine]?.tableStats[`${schema}.${table.name}`]?.rows ??
      -1,
    bytes:
      programs?.engines[engine]?.tableStats[`${schema}.${table.name}`]?.bytes ??
      -1,
    columns: table.columns.length,
    outgoing: table.foreignKeys.length,
    incoming: tables
      .filter((t) => t.engine === engine)
      .flatMap((t) => t.foreignKeys)
      .filter(
        (fk) => fk.targetSchema === schema && fk.targetTable === table.name,
      ).length,
    triggers: impact(table)?.triggers?.length || 0,
    writers:
      (impact(table)?.writes?.length || 0) +
      (impact(table)?.indirectWrites?.length || 0),
  });
  const list = tables
    .filter(
      (t) =>
        t.engine === engine &&
        t.schema === schema &&
        t.name.toLowerCase().includes(filter.toLowerCase()),
    )
    .map((table) => ({ table, metrics: metrics(table) }))
    .sort((a, b) => {
      const left = a.metrics[column],
        right = b.metrics[column];
      return (
        (typeof left === "number" && typeof right === "number"
          ? left - right
          : String(left).localeCompare(String(right), "fr")) *
        (descending ? -1 : 1)
      );
    });
  const headers = {
    name: "Table",
    rows: "≈ Lignes",
    bytes: "Taille",
    columns: "Colonnes",
    outgoing: "Références vers",
    incoming: "Référencée par",
    triggers: "Déclencheurs",
    writers: "Écrite par",
  };
  const persist = (next: {
    filter: string;
    column: string;
    descending: boolean;
  }) => save(storageKey, next);
  return (
    <div className="page">
      <header className="page-header">
        <div className="eyebrow">
          SCHÉMA ·{" "}
          {config.connections.find((c) => c.id === engine)?.label || engine}
        </div>
        <h1 className="page-title">{schema}</h1>
        <p className="page-subtitle">
          {list.length} {list.length === 1 ? "table" : "tables"} · Choisis une
          table pour explorer sa structure et ses données.
        </p>
      </header>
      <div className="schema-actions">
        <a className="button" href={href({ view: "sql", engine })}>
          <Icon name="sql" />
          Ouvrir la console SQL
        </a>
        <span className="section-help">
          Les volumes et les nombres de lignes sont estimés au dernier export.
        </span>
      </div>
      <input
        placeholder="Rechercher une table dans ce schéma…"
        className="page-filter"
        id="schema-filter"
        aria-label="Filtrer les tables du schéma"
        type="search"
        value={filter}
        onChange={(event) => {
          setFilter(event.target.value);
          persist({ filter: event.target.value, column, descending });
        }}
      />
      <div className="table-scroll">
        <table className="list-table sortable">
          <thead>
            <tr>
              {Object.entries(headers).map(([name, label]) => (
                <th
                  key={name}
                  aria-sort={
                    column === name
                      ? descending
                        ? "descending"
                        : "ascending"
                      : "none"
                  }
                >
                  <button
                    onClick={() => {
                      const desc =
                        column === name ? !descending : name !== "name";
                      setColumn(name);
                      setDescending(desc);
                      persist({ filter, column: name, descending: desc });
                    }}
                  >
                    {label}
                    {column === name ? (descending ? " ↓" : " ↑") : ""}
                  </button>
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {list.map(({ table, metrics }) => (
              <tr key={table.name}>
                {Object.keys(headers).map((name) => (
                  <td key={name}>
                    {name === "name" ? (
                      <a href={tableHref(table)}>{table.name}</a>
                    ) : name === "bytes" ? (
                      Number(metrics.bytes) >= 0 ? (
                        bytes(Number(metrics.bytes))
                      ) : (
                        "—"
                      )
                    ) : Number(metrics[name]) < 0 ? (
                      "—"
                    ) : (
                      metrics[name]
                    )}
                  </td>
                ))}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {!list.length && (
        <p className="empty-guidance">Aucune table ne correspond au filtre.</p>
      )}
    </div>
  );
}
function Columns({ table }: { table: Table }) {
  const { notice } = useSulcata();
  return (
    <>
      <p>{table.comment}</p>
      <div className="table-scroll">
        <table className="list-table">
          <thead>
            <tr>
              <th>Colonne</th>
              <th>Type</th>
              <th>Valeur absente autorisée</th>
              <th>Défaut</th>
              <th>Commentaire</th>
            </tr>
          </thead>
          <tbody>
            {table.columns.map((column) => (
              <tr key={column.name}>
                <td>
                  <code>
                    {column.primary ? "🔑 " : ""}
                    {column.name}
                  </code>{" "}
                  <button
                    className="link-button"
                    aria-label={`Copier ${column.name}`}
                    onClick={() => {
                      void navigator.clipboard
                        .writeText(column.name)
                        .catch(() => notice("Copie impossible"));
                    }}
                  >
                    copier
                  </button>
                </td>
                <td>
                  <code>{column.type}</code>
                </td>
                <td>{column.nullable ? "oui" : "non"}</td>
                <td>{display(column.default)}</td>
                <td>{column.comment}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {table.uniqueConstraints?.length ? (
        <p>
          Contraintes uniques :{" "}
          {table.uniqueConstraints
            .map((columns) => columns.join(", "))
            .join(" · ")}
        </p>
      ) : null}
    </>
  );
}
function Relations({ table }: { table: Table }) {
  const { tables } = useSulcata();
  const incoming = tables
    .filter((t) => t.engine === table.engine)
    .flatMap((t) =>
      t.foreignKeys
        .filter(
          (fk) =>
            fk.targetSchema === table.schema && fk.targetTable === table.name,
        )
        .map((fk) => ({ table: t, fk })),
    );
  return (
    <div className="relationship-columns">
      <section className="relationship-group">
        <header>
          <h2>
            Cette table utilise{" "}
            <span className="count-badge">{table.foreignKeys.length}</span>
          </h2>
          <p>Ses clés étrangères pointent vers ces tables.</p>
        </header>
        {table.foreignKeys.map((fk, index) => (
          <a
            className="relationship-card"
            key={index}
            href={href({
              view: "table",
              engine: table.engine,
              schema: fk.targetSchema,
              table: fk.targetTable,
            })}
          >
            <span className="relationship-origin">
              <code>{fk.column}</code>
              <span className="key fk">FK</span>
            </span>
            <Icon name="arrow" />
            <span>
              <strong>
                {fk.targetSchema}.{fk.targetTable}
              </strong>
              <small>Colonne cible : {fk.targetColumn}</small>
            </span>
          </a>
        ))}
        {!table.foreignKeys.length && (
          <div className="compact-empty">Aucune clé étrangère sortante.</div>
        )}
      </section>
      <section className="relationship-group">
        <header>
          <h2>
            Cette table est utilisée par{" "}
            <span className="count-badge">{incoming.length}</span>
          </h2>
          <p>Ces tables référencent sa clé.</p>
        </header>
        {incoming.map(({ table: source, fk }, index) => (
          <a className="relationship-card" key={index} href={tableHref(source)}>
            <span>
              <strong>
                {source.schema}.{source.name}
              </strong>
              <small>Colonne source : {fk.column}</small>
            </span>
            <Icon name="arrow" />
            <code>{fk.targetColumn}</code>
          </a>
        ))}
        {!incoming.length && (
          <div className="compact-empty">Aucune clé étrangère entrante.</div>
        )}
      </section>
    </div>
  );
}
function Neighborhood({ table }: { table: Table }) {
  const { tables } = useSulcata();
  const outgoing = tables.filter(
    (t) =>
      t.engine === table.engine &&
      table.foreignKeys.some(
        (fk) => fk.targetSchema === t.schema && fk.targetTable === t.name,
      ),
  );
  const incoming = tables.filter(
    (t) =>
      t.engine === table.engine &&
      t.foreignKeys.some(
        (fk) =>
          fk.targetSchema === table.schema && fk.targetTable === table.name,
      ),
  );
  const card = (neighbor: Table, direction: "incoming" | "outgoing") => (
    <a
      className="neighbor-card"
      href={tableHref(neighbor, "voisinage")}
      key={keyOf(neighbor)}
    >
      <span>
        <Icon name="table" />
        <strong>{neighbor.name}</strong>
      </span>
      <small>
        {neighbor.schema} ·{" "}
        {direction === "incoming"
          ? neighbor.foreignKeys
              .filter(
                (fk) =>
                  fk.targetSchema === table.schema &&
                  fk.targetTable === table.name,
              )
              .map((fk) => fk.column)
              .join(", ")
          : table.foreignKeys
              .filter(
                (fk) =>
                  fk.targetSchema === neighbor.schema &&
                  fk.targetTable === neighbor.name,
              )
              .map((fk) => fk.column)
              .join(", ")}
      </small>
    </a>
  );
  return (
    <>
      <div
        className="relationship-map"
        aria-label={`Carte des relations de ${table.schema}.${table.name}`}
      >
        <section>
          <h2>
            Référencée par{" "}
            <span className="count-badge">{incoming.length}</span>
          </h2>
          {incoming.map((t) => card(t, "incoming"))}
          {!incoming.length && (
            <p className="compact-empty">Aucune table entrante.</p>
          )}
        </section>
        <div className="map-center">
          <span className="map-direction">
            Les flèches suivent les clés étrangères <Icon name="arrow" />
          </span>
          <article className="selected-table-card">
            <Icon name="table" />
            <h2>{table.name}</h2>
            <p>{table.schema}</p>
            <span>{table.columns.length} colonnes</span>
          </article>
        </div>
        <section>
          <h2>
            Référence <span className="count-badge">{outgoing.length}</span>
          </h2>
          {outgoing.map((t) => card(t, "outgoing"))}
          {!outgoing.length && (
            <p className="compact-empty">Aucune table sortante.</p>
          )}
        </section>
      </div>
      <details className="relationship-details">
        <summary>Voir les colonnes de liaison</summary>
        <Relations table={table} />
      </details>
    </>
  );
}
function TablePrograms({ table }: { table: Table }) {
  const { impact, programs, openProgram } = useSulcata();
  const data = impact(table);
  const all = Object.values(programs?.engines || {}).flatMap(
    (source) => source.programs,
  );
  return (
    <>
      {Object.entries(impactLabels).map(([key, label]) => {
        const ids = data?.[key as keyof typeof impactLabels] || [];
        return (
          <details
            className="dependency-group program-section"
            key={key}
            open={key === "triggers" || key === "writes"}
          >
            <summary>
              {label} <span className="count-badge">{ids.length}</span>
            </summary>
            {key.startsWith("probable") && (
              <p>Déduit du SQL dynamique : à confirmer dans le code.</p>
            )}
            {!ids.length && (
              <p className="compact-empty">
                Aucun programme dans cette catégorie.
              </p>
            )}
            {ids.map((id) => {
              const program = all.find((p) => p.id === id);
              return (
                <button
                  className="dependency-link"
                  disabled={!program}
                  key={id}
                  onClick={() => program && openProgram(program)}
                >
                  {program
                    ? `${program.kind} · ${program.schema}.${program.name}`
                    : id}
                </button>
              );
            })}
          </details>
        );
      })}
      {!data && (
        <p className="info-callout">
          Aucun programme ne référence cette table dans le catalogue.
        </p>
      )}
    </>
  );
}
