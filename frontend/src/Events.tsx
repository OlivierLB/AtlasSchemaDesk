import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { request } from "./api";
import { errorText } from "./model";
import type { Programs } from "./model";
import { useAtlas } from "./context";
import { Icon } from "./Icon";
import { EventWindow } from "./EventWindow";

export function Events() {
  const { programs, config, openProgram } = useAtlas();
  const [source, setSource] = useState<"reference" | "local">("reference");
  const [timeZone, setTimeZone] = useState(
    Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC",
  );
  const reference = useQuery({
    queryKey: ["event-reference", timeZone],
    queryFn: () =>
      request<Programs & { warnings: string[]; filesRead: number }>(
        `/api/events/reference?timeZone=${encodeURIComponent(timeZone)}`,
        undefined,
        false,
      ),
    enabled: source === "reference",
  });
  const catalog = source === "reference" ? reference.data : programs;
  const [view, setView] = useState<"analysis" | "catalog">("analysis");
  const [search, setSearch] = useState("");
  const [engine, setEngine] = useState("");
  const [status, setStatus] = useState("");
  const all = Object.entries(catalog?.engines || {}).flatMap(
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
        <h1 className="page-title">
          Événements planifiés <span className="count-badge">{all.length}</span>
        </h1>
        <p className="page-subtitle">
          Analysez les tâches planifiées sur une période pour rechercher une
          origine aux ralentissements.
        </p>
      </header>
      <nav className="event-views" aria-label="Vues des événements">
        <button
          className={view === "analysis" ? "active" : ""}
          aria-pressed={view === "analysis"}
          onClick={() => setView("analysis")}
        >
          <Icon name="activity" /> Analyse par créneau
        </button>
        <button
          aria-label="Catalogue des événements"
          className={view === "catalog" ? "active" : ""}
          aria-pressed={view === "catalog"}
          onClick={() => setView("catalog")}
        >
          <Icon name="sql" /> Catalogue{" "}
          <span className="count-badge">{all.length}</span>
        </button>
      </nav>
      <section className="event-source" aria-label="Environnement analysé">
        <div className="event-source-fields">
          <label>
            Source des événements
            <select
              aria-label="Environnement analysé"
              value={source}
              onChange={(event) => {
                setSource(event.target.value as "reference" | "local");
                setEngine("");
                setStatus("");
              }}
            >
              <option value="reference">Environnement de référence</option>
              <option value="local">Catalogue local</option>
            </select>
          </label>
          {source === "reference" && (
            <label>
              Fuseau de référence
              <input
                aria-label="Fuseau de référence"
                list="reference-time-zones"
                defaultValue={timeZone}
                onBlur={(event) => setTimeZone(event.target.value.trim())}
              />
              <datalist id="reference-time-zones">
                {[
                  ...new Set([
                    Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC",
                    "UTC",
                    "Europe/Paris",
                    "America/New_York",
                    "Asia/Tokyo",
                  ]),
                ].map((zone) => (
                  <option key={zone} value={zone} />
                ))}
              </datalist>
            </label>
          )}
          <span className="event-source-status" role="status">
            <span className="event-source-dot" />
            {source === "reference" && reference.isFetching
              ? "Lecture des sources…"
              : `${all.length} événement${all.length === 1 ? "" : "s"} disponible${all.length === 1 ? "" : "s"}`}
          </span>
        </div>
        <details className="event-assumptions">
          <summary>
            {source === "reference"
              ? "Sources SQL · hypothèses de calcul"
              : "Catalogue local · périmètre de l’analyse"}
          </summary>
          {source === "reference" ? (
            <>
              <p>
                Les définitions proviennent du dépôt SQL configuré. La
                simulation suppose les événements et le planificateur activés,
                quel que soit leur état local ou l’état déclaré dans les
                scripts.
              </p>
              <p>
                Le fuseau de référence s’applique lorsqu’aucun fuseau n’est
                défini dans le script. Les horaires sans date de départ
                exploitable sont signalés comme indéterminés. Aucune définition
                SQL n’est exécutée.
              </p>
              {reference.data && (
                <p>
                  {reference.data.filesRead.toLocaleString("fr")} fichiers SQL
                  analysés.
                </p>
              )}
            </>
          ) : (
            <p>
              L’analyse utilise les horaires et les états enregistrés au dernier
              export du catalogue. Elle ne reconstitue pas l’historique des
              exécutions.
            </p>
          )}
        </details>
        {source === "reference" && reference.error && (
          <p className="feedback-card error" role="alert">
            {errorText(reference.error)}
          </p>
        )}
        {source === "reference" &&
          [...new Set(reference.data?.warnings || [])].map((warning) => (
            <p className="info-callout warning" key={warning}>
              {warning}
            </p>
          ))}
      </section>
      <div hidden={view !== "analysis"}>
        <EventWindow
          entries={all}
          catalog={catalog}
          source={source}
          timeZone={timeZone}
        />
      </div>
      <div hidden={view !== "catalog"}>
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
                ...(source === "reference"
                  ? [{ id: "reference", label: "Sources SQL" }]
                  : config.connections
                ).map((c) => (
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
        <div className="section-heading">
          <h2 className="section-title">Catalogue des événements</h2>
          <span className="section-help">
            {source === "reference"
              ? "Définitions issues du dépôt SQL"
              : "Définitions du dernier export"}
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
                        onClick={() => openProgram(program, catalog)}
                      >
                        {program.name}
                      </button>
                      <span className="cell-secondary">
                        {config.connections.find((c) => c.id === engine)
                          ?.label ||
                          (engine === "reference"
                            ? "Sources SQL"
                            : engine)}{" "}
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
                        {program.impact.indirectWrites?.length || 0} via
                        d’autres programmes
                      </span>
                    </td>
                    <td>{program.lastExecuted || "Non renseignée"}</td>
                    <td>
                      <button
                        className="button"
                        aria-label={`Inspecter ${program.name}`}
                        onClick={() => openProgram(program, catalog)}
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
                  ? "Ajustez les filtres pour afficher d’autres événements."
                  : source === "reference"
                    ? "Associez un dépôt SQL contenant les définitions des événements dans les paramètres de connexion."
                    : "Aucun événement n’a été trouvé dans le dernier catalogue exporté."}
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
    </div>
  );
}
