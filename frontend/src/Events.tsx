import { useState } from "react";
import { useAtlas } from "./context";
import { Icon } from "./Icon";

export function Events() {
  const { programs, config, openProgram } = useAtlas();
  const [search, setSearch] = useState("");
  const [engine, setEngine] = useState("");
  const [status, setStatus] = useState("");
  const all = Object.entries(programs?.engines || {}).flatMap(
    ([engine, source]) =>
      source.programs
        .filter((p) => p.kind === "event")
        .map((program) => ({ program, engine })),
  );
  const list = all
    .filter(
      ({ program, engine: owner }) =>
        (!engine || engine === owner) &&
        (!status || program.status?.toUpperCase() === status) &&
        `${program.schema}.${program.name} ${program.comment || ""}`
          .toLocaleLowerCase("fr")
          .includes(search.toLocaleLowerCase("fr")),
    )
    .sort((a, b) => a.program.name.localeCompare(b.program.name, "fr"));
  const statuses = [
    ...new Set(
      all.flatMap(({ program }) =>
        program.status ? [program.status.toUpperCase()] : [],
      ),
    ),
  ];
  const statusLabel = (value?: string) =>
    value?.toUpperCase() === "ENABLED"
      ? "Activé"
      : value?.toUpperCase() === "DISABLED"
        ? "Désactivé"
        : value?.toUpperCase() === "SLAVESIDE_DISABLED"
          ? "Désactivé sur réplique"
          : value || "Non renseigné";
  return (
    <div className="page events-page">
      <header className="page-header">
        <div className="eyebrow">AUTOMATISATIONS SQL</div>
        <h1 className="page-title">Événements planifiés</h1>
        <p className="page-subtitle">
          Comprends ce qui s’exécute automatiquement : sa fréquence, son état et
          les tables concernées.
        </p>
      </header>
      <div className="overview-strip">
        <div>
          <strong>{all.length}</strong>
          <span>événements au catalogue</span>
        </div>
        <div>
          <strong>
            {
              all.filter(
                ({ program }) => program.status?.toUpperCase() === "ENABLED",
              ).length
            }
          </strong>
          <span>activés dans le catalogue</span>
        </div>
        <p>
          <Icon name="activity" />
          Les informations viennent du dernier export. Elles ne confirment pas
          l’état du planificateur en temps réel.
        </p>
      </div>
      <div className="filter-toolbar">
        <label className="search-field">
          <Icon name="search" />
          <input
            type="search"
            aria-label="Rechercher un événement"
            placeholder="Nom, schéma ou description…"
            value={search}
            onChange={(event) => setSearch(event.target.value)}
          />
        </label>
        <label>
          Base
          <select
            value={engine}
            onChange={(event) => setEngine(event.target.value)}
          >
            {[
              <option key="" value="">
                Toutes les bases
              </option>,
              ...config.connections.map((c) => (
                <option key={c.id} value={c.id}>
                  {c.label}
                </option>
              )),
            ]}
          </select>
        </label>
        <label>
          État
          <select
            value={status}
            onChange={(event) => setStatus(event.target.value)}
          >
            <option value="">Tous les états</option>
            {statuses.map((value) => (
              <option key={value} value={value}>
                {statusLabel(value)}
              </option>
            ))}
          </select>
        </label>
        <span className="result-count" role="status">
          {list.length} résultat{list.length === 1 ? "" : "s"}
        </span>
      </div>
      {list.length ? (
        <div className="table-scroll">
          <table className="list-table events-table">
            <thead>
              <tr>
                <th>Événement / base</th>
                <th>Planification</th>
                <th>État</th>
                <th>Tables modifiées</th>
                <th>Dernière exécution</th>
                <th>
                  <span className="sr-only">Détails</span>
                </th>
              </tr>
            </thead>
            <tbody>
              {list.map(({ program, engine }) => (
                <tr key={program.id}>
                  <td>
                    <button
                      className="event-name link-button"
                      onClick={() => openProgram(program)}
                    >
                      {program.name}
                    </button>
                    <span className="cell-secondary">
                      {config.connections.find((c) => c.id === engine)?.label ||
                        engine}{" "}
                      / {program.schema}
                    </span>
                  </td>
                  <td>
                    <span>
                      {program.schedule || "Planification non renseignée"}
                    </span>
                    {program.starts && (
                      <span className="cell-secondary">
                        Début : {program.starts}
                      </span>
                    )}
                  </td>
                  <td>
                    <span
                      className={`state-badge ${program.status?.toUpperCase() === "ENABLED" ? "success" : "neutral"}`}
                    >
                      {statusLabel(program.status)}
                    </span>
                  </td>
                  <td>
                    <strong>{program.impact.writes?.length || 0}</strong>{" "}
                    directes
                    <span className="cell-secondary">
                      {program.impact.indirectWrites?.length || 0} via d’autres
                      programmes
                    </span>
                  </td>
                  <td>{program.lastExecuted || "Non renseignée"}</td>
                  <td>
                    <button
                      className="button"
                      aria-label={`Inspecter ${program.name}`}
                      onClick={() => openProgram(program)}
                    >
                      Inspecter <Icon name="arrow" />
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : (
        <div className="empty-guidance">
          <Icon name="activity" />
          <div>
            <h2>
              {all.length
                ? "Aucun événement ne correspond"
                : "Aucun événement planifié au catalogue"}
            </h2>
            <p>
              {all.length
                ? "Modifie les filtres pour retrouver un événement."
                : "Les événements MySQL apparaîtront ici après l’export du catalogue, si la connexion en expose."}
            </p>
            {all.length > 0 && (
              <button
                className="button"
                onClick={() => {
                  setSearch("");
                  setEngine("");
                  setStatus("");
                }}
              >
                Réinitialiser les filtres
              </button>
            )}
          </div>
        </div>
      )}
    </div>
  );
}
