import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { request } from "./api";
import { useAtlas } from "./context";
import { errorText, tableHref } from "./model";
import type { EventWindowReport, Program, Programs } from "./model";
import {
  localDate,
  localTime,
  makeEventWindow,
  sharedEventTables,
} from "./eventWindow";
import { Icon } from "./Icon";
import "./EventWindow.css";

export function EventWindow({
  entries,
  catalog,
  source = "local",
  timeZone = "UTC",
}: {
  entries: { engine: string; program: Program }[];
  catalog?: Programs;
  source?: "reference" | "local";
  timeZone?: string;
}) {
  const {
    programs: localPrograms,
    config,
    openProgram: open,
    tables,
  } = useAtlas();
  const programs = catalog || localPrograms;
  const openProgram = (program: Program) => open(program, catalog);
  const now = new Date();
  const [date, setDate] = useState(() => localDate(new Date()));
  const [start, setStart] = useState(
    () => `${String(new Date().getHours()).padStart(2, "0")}:00`,
  );
  const [end, setEnd] = useState(
    () => `${String((new Date().getHours() + 1) % 24).padStart(2, "0")}:00`,
  );
  const [wholeDay, setWholeDay] = useState(false);
  const [lookback, setLookback] = useState(30);
  const [submitted, setSubmitted] = useState<{
    from: string;
    to: string;
    lookbackMinutes: number;
  } | null>(null);
  const [inputError, setInputError] = useState("");
  const query = useQuery({
    queryKey: [
      "event-window",
      source,
      timeZone,
      submitted,
      programs?.generatedAt,
    ],
    queryFn: () =>
      request<EventWindowReport>(
        "/api/events/window",
        { ...submitted, source, timeZone },
        false,
      ),
    enabled: submitted !== null,
  });
  const selected = new Map(entries.map((entry) => [entry.program.id, entry]));
  const report = query.data;
  const matches = (report?.events || []).filter((match) =>
    selected.has(match.programId),
  );
  const unknown = (report?.unknown || []).filter((issue) =>
    selected.has(issue.programId),
  );
  const excluded = (report?.excluded || []).filter((issue) =>
    selected.has(issue.programId),
  );
  const shared = sharedEventTables(
    matches.flatMap((match) => {
      const entry = selected.get(match.programId);
      return entry ? [entry] : [];
    }),
  );
  const buckets = (report?.buckets || []).map((bucket) => ({
    ...bucket,
    count: Object.entries(bucket.counts).reduce(
      (sum, [id, n]) => sum + (selected.has(id) ? n : 0),
      0,
    ),
  }));
  const peak = Math.max(1, ...buckets.map((b) => b.count));
  const format = (value: string) =>
    new Date(value).toLocaleString("fr", {
      day: "2-digit",
      month: "2-digit",
      year: "numeric",
      hour: "2-digit",
      minute: "2-digit",
    });
  const clock = (value: string) =>
    new Date(value).toLocaleTimeString("fr", {
      hour: "2-digit",
      minute: "2-digit",
      second: "2-digit",
    });
  const zone = Intl.DateTimeFormat().resolvedOptions().timeZone;
  const incomplete =
    programs && source === "local"
      ? config.connections.filter(
          (c) => !programs.engines[c.id] || programs.engines[c.id]?.error,
        )
      : [];
  const analyze = () => {
    try {
      const window = makeEventWindow(date, start, end, wholeDay);
      setInputError("");
      setSubmitted({ ...window, lookbackMinutes: lookback });
    } catch (error) {
      setInputError(errorText(error));
    }
  };
  const name = (id: string) => selected.get(id)?.program.name || id;
  return (
    <section className="event-window" aria-label="Analyser un créneau">
      <header className="window-heading">
        <div>
          <h2>Analyser une période</h2>
          <p>
            Identifiez les événements prévus et les tables sollicitées sur le
            créneau.
          </p>
        </div>
        <Icon name="activity" />
      </header>
      {incomplete.length > 0 && (
        <div className="info-callout warning">
          Catalogue partiel : {incomplete.map((c) => c.label).join(", ")} :
          certains événements peuvent manquer.
        </div>
      )}
      <form
        className="window-form"
        onSubmit={(event) => {
          event.preventDefault();
          analyze();
        }}
      >
        <label>
          Date
          <input
            aria-label="Jour à analyser"
            type="date"
            required
            value={date}
            onChange={(event) => setDate(event.target.value)}
          />
        </label>
        <label>
          Début
          <input
            aria-label="Heure de début"
            type="time"
            required
            disabled={wholeDay}
            value={start}
            onChange={(event) => setStart(event.target.value)}
          />
        </label>
        <label>
          Fin
          <input
            aria-label="Heure de fin"
            type="time"
            required
            disabled={wholeDay}
            value={end}
            onChange={(event) => setEnd(event.target.value)}
          />
        </label>
        <label className="window-day">
          <input
            type="checkbox"
            checked={wholeDay}
            onChange={(event) => setWholeDay(event.target.checked)}
          />
          Toute la journée
        </label>
        <label>
          Départs antérieurs
          <select
            aria-label="Inclure les lancements précédents"
            value={lookback}
            onChange={(event) => setLookback(Number(event.target.value))}
          >
            {[0, 15, 30, 60, 180, 1440].map((minutes) => (
              <option key={minutes} value={minutes}>
                {minutes === 0
                  ? "Ne pas inclure"
                  : minutes === 1440
                    ? "24 heures avant"
                    : `${minutes} minutes avant`}
              </option>
            ))}
          </select>
        </label>
        <button
          className="button primary"
          type="submit"
          disabled={query.isFetching}
        >
          {query.isFetching ? "Analyse…" : "Analyser le créneau"}
          <Icon name="arrow" />
        </button>
      </form>
      <p className="window-timezone">
        Fuseau d’affichage : {zone || "fuseau du navigateur"}.
        {!wholeDay && end <= start && " La fin est le lendemain."}{" "}
        <button
          className="link-button"
          onClick={() => {
            setDate(localDate(now));
            setStart(localTime(now));
            setEnd(localTime(new Date(now.getTime() + 60 * 60_000)));
            setWholeDay(false);
          }}
        >
          Maintenant
        </button>
      </p>
      {inputError && (
        <p className="feedback-card error" role="alert">
          {inputError}
        </p>
      )}
      {query.error && (
        <p className="feedback-card error" role="alert">
          {errorText(query.error)}
        </p>
      )}
      {query.isFetching && (
        <p className="section-help" role="status">
          Calcul des horaires depuis{" "}
          {source === "reference" ? "les sources SQL" : "le catalogue local"}…
        </p>
      )}
      {report && (
        <div className="window-results">
          <div className="window-result-heading">
            <div>
              <h3>
                Du {format(report.from)} au {format(report.to)}
              </h3>
              <p>
                {source === "reference"
                  ? "Sources SQL actualisées"
                  : "Catalogue exporté"}{" "}
                {report.generatedAt
                  ? `le ${format(report.generatedAt)}`
                  : "disponible"}{" "}
                · {matches.length} événement{matches.length === 1 ? "" : "s"}{" "}
                identifié{matches.length === 1 ? "" : "s"}.
              </p>
            </div>
            <span className="state-badge neutral">Planification théorique</span>
          </div>
          <div className="window-metrics">
            <div>
              <strong>
                {matches.reduce((n, m) => n + m.starts, 0).toLocaleString("fr")}
              </strong>
              <span>lancements prévus</span>
            </div>
            <div>
              <strong>{matches.filter((m) => m.previousAt).length}</strong>
              <span>départs antérieurs</span>
            </div>
            <div>
              <strong>{shared.length}</strong>
              <span>tables en commun</span>
            </div>
          </div>
          <p className="window-result-note">
            Horaires théoriques. Les durées et les exécutions réelles ne sont
            pas connues.
          </p>
          {buckets.length > 0 && (
            <div className="window-timeline">
              <div className="section-heading">
                <h3 className="section-title">Répartition des lancements</h3>
                <span className="section-help">
                  Sélectionnez une période pour affiner l’analyse
                </span>
              </div>
              <div
                className="window-bars"
                aria-label="Lancements prévus par période"
              >
                {buckets.map((bucket) => (
                  <button
                    key={bucket.from}
                    aria-label={`${format(bucket.from)} : ${bucket.count} lancements prévus`}
                    title={`${format(bucket.from)} → ${format(bucket.to)} : ${bucket.count} lancements`}
                    onClick={() => {
                      const a = new Date(bucket.from),
                        b = new Date(bucket.to);
                      setDate(localDate(a));
                      setStart(localTime(a));
                      setEnd(localTime(b));
                      setWholeDay(false);
                      setSubmitted({
                        from: bucket.from,
                        to: bucket.to,
                        lookbackMinutes: report.lookbackMinutes,
                      });
                    }}
                  >
                    <span
                      style={{
                        height: `${Math.max(3, (bucket.count / peak) * 100)}%`,
                      }}
                    />
                    <small>
                      {new Date(bucket.from).toLocaleTimeString("fr", {
                        hour: "2-digit",
                        minute: "2-digit",
                      })}
                    </small>
                  </button>
                ))}
              </div>
            </div>
          )}
          {matches.length ? (
            <div className="table-scroll">
              <table className="list-table window-events">
                <thead>
                  <tr>
                    <th>Événement / base</th>
                    <th>Lancements prévus</th>
                    <th>Dernier départ antérieur</th>
                    <th>Tables concernées</th>
                    <th>
                      <span className="sr-only">Détails</span>
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {matches.map((match) => {
                    const entry = selected.get(match.programId)!;
                    const p = entry.program;
                    const affected = [
                      ...new Set([
                        ...(p.impact.writes || []),
                        ...(p.impact.indirectWrites || []),
                        ...(p.impact.reads || []),
                      ]),
                    ];
                    return (
                      <tr key={match.programId}>
                        <td>
                          <button
                            className="event-name link-button"
                            onClick={() => openProgram(p)}
                          >
                            {p.name}
                          </button>
                          <span className="cell-secondary">
                            {config.connections.find(
                              (c) => c.id === match.engine,
                            )?.label ||
                              (match.engine === "reference"
                                ? "Sources SQL"
                                : match.engine)}{" "}
                            / {p.schema}
                          </span>
                          <span className="cell-secondary">
                            Fuseau de l’événement : {match.timeZone}
                          </span>
                          {!match.scheduler && (
                            <span className="cell-secondary">
                              État du planificateur non renseigné
                            </span>
                          )}
                        </td>
                        <td>
                          {match.starts > 0 ? (
                            <>
                              <strong>
                                {match.starts.toLocaleString("fr")}
                              </strong>
                              <span className="cell-secondary">
                                {match.firstAt && clock(match.firstAt)}
                                {match.starts > 1 &&
                                  match.lastAt &&
                                  ` → ${clock(match.lastAt)}`}
                              </span>
                            </>
                          ) : (
                            <span className="state-badge warning">
                              Départ antérieur
                            </span>
                          )}
                        </td>
                        <td>
                          {match.previousAt ? (
                            <>
                              <strong>{format(match.previousAt)}</strong>
                              <span className="cell-secondary">
                                Durée d’exécution inconnue
                              </span>
                            </>
                          ) : (
                            "—"
                          )}
                        </td>
                        <td>
                          <strong>{affected.length}</strong>
                          <span className="cell-secondary">
                            {affected.slice(0, 3).join(", ") ||
                              "Aucune table identifiée"}
                            {affected.length > 3 ? "…" : ""}
                          </span>
                          {p.impact.dynamicSql && (
                            <span className="cell-secondary">
                              SQL dynamique · impacts à confirmer
                            </span>
                          )}
                        </td>
                        <td>
                          <button
                            className="button"
                            aria-label={`Examiner ${p.name}`}
                            onClick={() => openProgram(p)}
                          >
                            Examiner <Icon name="arrow" />
                          </button>
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>
          ) : (
            <div className="compact-empty">
              Aucun lancement identifié sur cette période. Consultez les
              horaires indéterminés ou élargissez le créneau.
            </div>
          )}
          {shared.length > 0 && (
            <section className="window-shared">
              <h3 className="section-title">
                Tables sollicitées par plusieurs événements
              </h3>
              <p className="section-help">
                Les accès à une même table peuvent orienter la recherche de
                contention. La simultanéité des exécutions reste à vérifier.
              </p>
              {shared.map((group) => {
                const table = tables.find(
                  (t) =>
                    t.engine === group.engine &&
                    `${t.schema}.${t.name}` === group.table,
                );
                return (
                  <div
                    className="window-shared-row"
                    key={`${group.engine}:${group.table}`}
                  >
                    <div>
                      {table ? (
                        <a href={tableHref(table)}>{group.table}</a>
                      ) : (
                        <strong>{group.table}</strong>
                      )}
                      <small>
                        {config.connections.find((c) => c.id === group.engine)
                          ?.label ||
                          (group.engine === "reference"
                            ? "Sources SQL"
                            : group.engine)}{" "}
                        · {group.readers.size} lectures · {group.writers.size}{" "}
                        écritures
                        {group.probable.size
                          ? ` · ${group.probable.size} impacts probables`
                          : ""}
                      </small>
                    </div>
                    <div className="window-event-links">
                      {[...group.programs].map((id) => (
                        <button
                          className="link-button"
                          key={id}
                          onClick={() => {
                            const p = selected.get(id)?.program;
                            if (p) openProgram(p);
                          }}
                        >
                          {name(id)}
                        </button>
                      ))}
                    </div>
                  </div>
                );
              })}
            </section>
          )}
          {unknown.length > 0 && (
            <details className="window-issues" open>
              <summary>
                Horaires indéterminés{" "}
                <span className="count-badge">{unknown.length}</span>
              </summary>
              {unknown.map((issue) => (
                <p key={issue.programId}>
                  <button
                    className="link-button"
                    onClick={() =>
                      openProgram(selected.get(issue.programId)!.program)
                    }
                  >
                    {name(issue.programId)}
                  </button>
                  <span>{issue.reason}</span>
                </p>
              ))}
            </details>
          )}
          {excluded.length > 0 && (
            <details className="window-issues">
              <summary>
                Événements exclus{" "}
                <span className="count-badge">{excluded.length}</span>
              </summary>
              {excluded.map((issue) => (
                <p key={issue.programId}>
                  <button
                    className="link-button"
                    onClick={() =>
                      openProgram(selected.get(issue.programId)!.program)
                    }
                  >
                    {name(issue.programId)}
                  </button>
                  <span>{issue.reason}</span>
                </p>
              ))}
            </details>
          )}
        </div>
      )}
    </section>
  );
}
