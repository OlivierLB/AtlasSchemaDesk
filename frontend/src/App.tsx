import { useEffect, useMemo, useReducer, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { initializeSession, replaceSession, request } from "./api";
import { Atlas } from "./context";
import type { SqlState } from "./context";
import {
  errorText,
  href,
  keyOf,
  parseHash,
  save,
  stored,
  tableHref,
} from "./model";
import type {
  Program,
  Programs,
  PublicConfig,
  Schema,
  Session,
  Status,
  Table,
} from "./model";
import { Explorer, ProgramDetail } from "./Exploration";
import { Maintenance } from "./Maintenance";
import { Navigation } from "./Navigation";
import { Icon } from "./Icon";
import { UpdateBanner } from "./UpdateBanner";

function sqlReducer(
  state: Record<string, SqlState>,
  action: { engine: string; patch: Partial<SqlState> },
): Record<string, SqlState> {
  return {
    ...state,
    [action.engine]: {
      ...(state[action.engine] || { text: "", running: false }),
      ...action.patch,
    },
  };
}
export function App() {
  const client = useQueryClient();
  const [sessionId, setSessionId] = useState("");
  const [expired, setExpired] = useState(false);
  const [message, setMessage] = useState("");
  const [route, setRoute] = useState(() => parseHash(location.hash));
  const [filter, setFilter] = useState("");
  const [navigationOpen, setNavigationOpen] = useState(false);
  const [palette, setPalette] = useState(false);
  const [search, setSearch] = useState("");
  const [selection, setSelection] = useState(0);
  const [program, setProgram] = useState<Program | null>(null);
  const [programCatalog, setProgramCatalog] = useState<Programs>();
  const [versionsOpen, setVersionsOpen] = useState(false);
  const [selectedVersions, setSelectedVersions] = useState<
    Record<string, string>
  >({});
  const [historical, setHistorical] = useState<
    Record<string, Omit<Table, "engine">[]>
  >({});
  const [sql, dispatchSql] = useReducer(sqlReducer, {});
  useEffect(() => {
    const content = document.getElementById("main");
    if (content) {
      content.scrollTop = 0;
      content.scrollLeft = 0;
    }
  }, [route.view, route.engine, route.schema, route.table, route.tab]);
  useEffect(() => {
    let mounted = true;
    void initializeSession()
      .then((id) => {
        if (mounted) {
          setSessionId(id);
          setExpired(false);
        }
      })
      .catch((error) => {
        if (mounted) setMessage(errorText(error));
      });
    const change = () => {
      setRoute(parseHash(location.hash));
      setProgram(null);
      setNavigationOpen(false);
    };
    const expiration = () => setExpired(true);
    addEventListener("hashchange", change);
    addEventListener("atlas-session-expired", expiration);
    return () => {
      mounted = false;
      removeEventListener("hashchange", change);
      removeEventListener("atlas-session-expired", expiration);
    };
  }, []);
  const config = useQuery({
    queryKey: ["config"],
    queryFn: () => request<PublicConfig>("/api/config", undefined, false),
  });
  const status = useQuery({
    queryKey: ["status", sessionId],
    queryFn: () => request<Status>("/api/status"),
    refetchInterval: 1000,
    enabled: Boolean(sessionId) && !expired,
  });
  const session = useQuery({
    queryKey: ["session", sessionId],
    queryFn: () => request<Session>("/api/sessions/current"),
    refetchInterval: 10_000,
    enabled: Boolean(sessionId) && !expired,
  });
  const schema = useQuery({
    queryKey: ["schema"],
    queryFn: () =>
      request<Schema>(`/data/schema.json?t=${Date.now()}`, undefined, false),
    refetchInterval: (query) => (query.state.data ? false : 1000),
  });
  const programs = useQuery({
    queryKey: ["programs"],
    queryFn: () =>
      request<Programs>(
        `/data/programs.json?t=${Date.now()}`,
        undefined,
        false,
      ),
    refetchInterval: (query) => (query.state.data ? false : 1000),
  });
  const exportFinished = status.data?.finishedAt;
  const restoreFinished = status.data?.apply.finishedAt;
  useEffect(() => {
    if (!exportFinished && !restoreFinished) return;
    void client.invalidateQueries({ queryKey: ["schema"] });
    void client.invalidateQueries({ queryKey: ["programs"] });
    void client.invalidateQueries({ queryKey: ["rows"] });
  }, [client, exportFinished, restoreFinished]);
  const tables = useMemo(
    () =>
      Object.entries(schema.data?.sources || {}).flatMap(([engine, tables]) =>
        (historical[engine] || tables).map((table) => ({ ...table, engine })),
      ),
    [schema.data, historical],
  );
  const allPrograms = useMemo(
    () =>
      Object.values(programs.data?.engines || {}).flatMap(
        (source) => source.programs || [],
      ),
    [programs.data],
  );
  const ready = Boolean(sessionId) && !expired;
  const publicConfig = config.data || {
    connections: [],
    modules: { dumps: false, routines: false },
  };
  const refreshSession = async () => {
    await Promise.all([
      client.invalidateQueries({ queryKey: ["session"] }),
      client.invalidateQueries({ queryKey: ["status"] }),
    ]);
  };
  const restartSession = async () => {
    try {
      const id = await replaceSession();
      setSessionId(id);
      setExpired(false);
      setMessage(
        "Nouvelle session. Les anciennes transactions ont été annulées.",
      );
      for (const engine of Object.keys(sql))
        dispatchSql({
          engine,
          patch: {
            response: undefined,
            error: undefined,
            uncertain: false,
            running: false,
          },
        });
      await client.invalidateQueries({ queryKey: ["rows"] });
    } catch (error) {
      setMessage(errorText(error));
    }
  };
  const selectVersion = async (engine: string, hash: string) => {
    try {
      const snapshot = hash
        ? await request<Omit<Table, "engine">[]>(
            `/data/snapshots/${encodeURIComponent(engine)}/${hash}.json`,
            undefined,
            false,
          )
        : null;
      setSelectedVersions((previous) => {
        const next = { ...previous };
        if (hash) next[engine] = hash;
        else delete next[engine];
        return next;
      });
      setHistorical((previous) => {
        const next = { ...previous };
        if (snapshot) next[engine] = snapshot;
        else delete next[engine];
        return next;
      });
    } catch (error) {
      setMessage(errorText(error));
    }
  };
  const searchResults = useMemo(() => {
    const needle = search.toLocaleLowerCase("fr");
    return [
      ...tables
        .filter((table) =>
          `${table.engine} ${table.schema}.${table.name} ${table.columns.map((c) => c.name).join(" ")}`
            .toLocaleLowerCase("fr")
            .includes(needle),
        )
        .map((table) => ({
          id: keyOf(table),
          label: `${table.schema}.${table.name}`,
          detail: table.engine,
          href: tableHref(table),
          program: null,
        })),
      ...allPrograms
        .filter((program) =>
          `${program.schema}.${program.name}`
            .toLocaleLowerCase("fr")
            .includes(needle),
        )
        .map((program) => ({
          id: program.id,
          label: program.name,
          detail: program.kind,
          href: "",
          program,
        })),
    ].slice(0, 50);
  }, [tables, allPrograms, search]);
  useEffect(() => {
    const keydown = (event: KeyboardEvent) => {
      if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === "k") {
        event.preventDefault();
        if (versionsOpen) return;
        setPalette((open) => !open);
        setSelection(0);
      }
      if (event.key === "Escape") {
        setPalette(false);
        setProgram(null);
        setVersionsOpen(false);
        setNavigationOpen(false);
      }
    };
    addEventListener("keydown", keydown);
    return () => removeEventListener("keydown", keydown);
  }, [versionsOpen]);
  useEffect(() => {
    const beforeUnload = (event: BeforeUnloadEvent) => {
      if (
        Object.values(status.data?.transactions || {}).some(Boolean) ||
        Object.values(sql).some((state) => state.running)
      )
        event.preventDefault();
    };
    addEventListener("beforeunload", beforeUnload);
    return () => removeEventListener("beforeunload", beforeUnload);
  }, [status.data?.transactions, sql]);
  useEffect(() => {
    if (route.view !== "table") return;
    const recent = stored<{ engine: string; schema: string; table: string }[]>(
      "db-atlas-recent-tables",
      [],
    );
    save(
      "db-atlas-recent-tables",
      [
        route,
        ...recent.filter(
          (item) =>
            item.engine !== route.engine ||
            item.schema !== route.schema ||
            item.table !== route.table,
        ),
      ].slice(0, 8),
    );
  }, [route]);
  const openResult = (index: number) => {
    const result = searchResults[index];
    if (!result) return;
    if (result.program) {
      setProgramCatalog(undefined);
      setProgram(result.program);
    } else location.hash = result.href;
    setPalette(false);
  };
  const transactionEngines = Object.keys(
    status.data?.transactions || {},
  ).filter((engine) => status.data?.transactions[engine]);
  return (
    <Atlas.Provider
      value={{
        config: publicConfig,
        schema: schema.data,
        programs: programs.data,
        tables,
        route,
        status: status.data,
        sessionId,
        expired,
        selectedVersions,
        selectVersion: (engine, hash) => {
          void selectVersion(engine, hash);
        },
        sql,
        updateSql: (engine, patch) => dispatchSql({ engine, patch }),
        connection: (id) => publicConfig.connections.find((c) => c.id === id),
        impact: (table) =>
          programs.data?.engines[table.engine]?.tableImpacts[
            `${table.schema}.${table.name}`
          ],
        openProgram: (selected, catalog) => {
          setProgramCatalog(catalog);
          setProgram(selected);
        },
        notice: setMessage,
        refreshSession,
      }}
    >
      <div className="app">
        <a
          className="skip-link"
          href="#main"
          onClick={(event) => {
            event.preventDefault();
            document.getElementById("main")?.focus();
          }}
        >
          Aller au contenu
        </a>
        <header className="topbar">
          <button
            className="button mobile-menu"
            aria-label={
              navigationOpen ? "Fermer la navigation" : "Ouvrir la navigation"
            }
            aria-expanded={navigationOpen}
            aria-controls="database-navigation"
            onClick={() => setNavigationOpen((open) => !open)}
          >
            <Icon name={navigationOpen ? "close" : "menu"} />
          </button>
          <a className="brand" href="#/">
            <span className="brand-mark" aria-hidden="true" />
            <div>
              <h1 className="brand-name">Atlas Schema Desk</h1>
              <p className="brand-subtitle">Votre espace de données</p>
            </div>
          </a>
          <button
            className="search-trigger global-search"
            id="palette-open"
            aria-label="Rechercher dans le catalogue"
            onClick={() => {
              setPalette(true);
              setSelection(0);
            }}
          >
            <Icon name="search" />
            <span>Table, colonne, programme…</span>
            <kbd>Ctrl K</kbd>
          </button>
          <div className="topbar-right">
            {transactionEngines.length > 0 && (
              <a
                className="button tx-indicator"
                id="tx-indicator"
                href={href({ view: "sql", engine: transactionEngines[0] })}
              >
                Transaction : {transactionEngines.join(", ")}
              </a>
            )}
            <span className="catalog-status">
              <i className={`status-dot ${schema.data ? "" : "loading"}`} />
              {tables.length} tables
            </span>
            <button
              className="button"
              id="refresh"
              disabled={!ready || status.data?.state === "running"}
              onClick={() => {
                void request("/api/refresh", {})
                  .then(() =>
                    client.invalidateQueries({ queryKey: ["status"] }),
                  )
                  .catch((error) => setMessage(errorText(error)));
              }}
            >
              <Icon name="refresh" />
              {status.data?.state === "running"
                ? "Actualisation…"
                : "Actualiser"}
            </button>
          </div>
        </header>
        <UpdateBanner />
        {(expired ||
          (session.data && session.data.expiresInSeconds <= 300)) && (
          <div className="session-warning" role="alert">
            {expired
              ? "Session expirée : les modifications non validées ont été annulées."
              : `Session inactive : expiration dans ${Math.ceil((session.data?.expiresInSeconds || 0) / 60)} min.`}{" "}
            <button
              className="button"
              onClick={() => {
                if (expired) void restartSession();
                else
                  void request("/api/sessions/renew", {})
                    .then(refreshSession)
                    .catch((error) => setMessage(errorText(error)));
              }}
            >
              {expired ? "Nouvelle session" : "Prolonger la session"}
            </button>
          </div>
        )}
        {message && (
          <div className="session-warning" role="status">
            {message}{" "}
            <button
              className="link-button"
              aria-label="Fermer le message"
              onClick={() => setMessage("")}
            >
              ✕
            </button>
          </div>
        )}
        {status.data?.transactionsLostAt && (
          <div role="alert" className="session-warning">
            Connexion perdue : une transaction a été interrompue. Vérifie les
            données avant de continuer.
          </div>
        )}
        {status.error && !expired && (
          <div className="session-warning" role="alert">
            Serveur inaccessible : les états en cours restent à confirmer.{" "}
            {errorText(status.error)}
          </div>
        )}
        {status.data &&
          ["error", "cancelled", "timed_out"].includes(
            status.data.apply.state,
          ) && (
            <div className="session-warning" role="alert">
              Restauration interrompue ou échouée : la base peut être
              partiellement restaurée. Actualise et vérifie les données avant de
              continuer.{" "}
              <button className="button" onClick={() => setVersionsOpen(true)}>
                Détails
              </button>
            </div>
          )}
        {status.data &&
          ["error", "cancelled", "timed_out"].includes(status.data.state) && (
            <div className="session-warning" role="alert">
              Le dernier export du catalogue n’a pas abouti.{" "}
              <button className="button" onClick={() => setVersionsOpen(true)}>
                Détails
              </button>
            </div>
          )}
        {versionsOpen && <Maintenance onClose={() => setVersionsOpen(false)} />}
        <main className={`workspace ${program ? "" : "no-selection"}`}>
          {navigationOpen && (
            <button
              className="navigation-backdrop"
              aria-label="Fermer la navigation"
              onClick={() => setNavigationOpen(false)}
            />
          )}
          <aside
            id="database-navigation"
            className={`sidebar ${navigationOpen ? "navigation-open" : ""}`}
            aria-label="Navigation dans les bases"
          >
            <Navigation
              filter={filter}
              onFilter={setFilter}
              versionsOpen={versionsOpen}
              onVersions={() => setVersionsOpen((open) => !open)}
              onNavigate={() => setNavigationOpen(false)}
            />
          </aside>
          <section className="main-panel" aria-label="Contenu">
            <div className="main-bar">
              <nav className="crumbs" id="crumbs" aria-label="Fil d’Ariane">
                <a href="#/">Vue d’ensemble</a>
                {["sql", "events", "connections"].includes(route.view) && (
                  <>
                    <span>/</span>
                    <span>
                      {route.view === "sql"
                        ? "Console SQL"
                        : route.view === "events"
                          ? "Événements"
                          : "Connexions et sauvegardes"}
                    </span>
                  </>
                )}
                {route.engine && (
                  <>
                    <span>/</span>
                    <span>
                      {publicConfig.connections.find(
                        (c) => c.id === route.engine,
                      )?.label || route.engine}
                    </span>
                  </>
                )}
                {route.schema && (
                  <>
                    <span>/</span>
                    <a
                      href={href({
                        view: "schema",
                        engine: route.engine,
                        schema: route.schema,
                      })}
                    >
                      {route.schema}
                    </a>
                  </>
                )}
                {route.table && <span>/ {route.table}</span>}
              </nav>
            </div>
            <div id="main" className="main-scroll" tabIndex={-1}>
              {config.error ? (
                <div role="alert">{errorText(config.error)}</div>
              ) : !ready && !expired ? (
                <div className="page">
                  <p>Initialisation de la session…</p>
                  {message && (
                    <button
                      className="button"
                      onClick={() => {
                        void initializeSession()
                          .then((id) => {
                            setSessionId(id);
                            setExpired(false);
                            setMessage("");
                          })
                          .catch((error) => setMessage(errorText(error)));
                      }}
                    >
                      Réessayer
                    </button>
                  )}
                </div>
              ) : !schema.data ? (
                <div className="page">
                  <h1 className="page-title">Chargement du catalogue</h1>
                  <p>
                    {schema.error
                      ? errorText(schema.error)
                      : "Lecture des métadonnées…"}
                  </p>
                  {status.data?.state === "error" && (
                    <pre>{status.data.log}</pre>
                  )}
                </div>
              ) : (
                <Explorer />
              )}
            </div>
          </section>
          {program && (
            <button
              className="inspector-backdrop"
              tabIndex={-1}
              aria-label="Fermer la fiche du programme"
              onClick={() => setProgram(null)}
            />
          )}
          {program && (
            <aside
              id="inspector"
              className="inspector open"
              aria-label="Fiche du programme"
            >
              <ProgramDetail
                program={program}
                catalog={programCatalog}
                close={() => setProgram(null)}
              />
            </aside>
          )}
        </main>
      </div>
      {palette && (
        <div
          className="palette"
          id="palette"
          role="dialog"
          aria-modal="true"
          aria-label="Recherche"
          onClick={() => setPalette(false)}
        >
          <div
            className="palette-box"
            onKeyDown={(event) => {
              if (event.key !== "Tab") return;
              const controls = [
                ...event.currentTarget.querySelectorAll<HTMLElement>(
                  "input, button",
                ),
              ];
              const first = controls[0],
                last = controls.at(-1);
              if (event.shiftKey && document.activeElement === first) {
                event.preventDefault();
                last?.focus();
              } else if (!event.shiftKey && document.activeElement === last) {
                event.preventDefault();
                first?.focus();
              }
            }}
            onClick={(event) => event.stopPropagation()}
          >
            <input
              id="palette-input"
              className="palette-input"
              autoFocus
              type="search"
              placeholder="Table, colonne ou programme SQL…"
              value={search}
              onChange={(event) => {
                setSearch(event.target.value);
                setSelection(0);
              }}
              onKeyDown={(event) => {
                if (event.key === "ArrowDown") {
                  event.preventDefault();
                  setSelection((index) =>
                    Math.min(index + 1, searchResults.length - 1),
                  );
                }
                if (event.key === "ArrowUp") {
                  event.preventDefault();
                  setSelection((index) => Math.max(0, index - 1));
                }
                if (event.key === "Enter") openResult(selection);
              }}
              aria-controls="palette-results"
              aria-activedescendant={
                searchResults[selection]
                  ? `search-result-${selection}`
                  : undefined
              }
            />
            <div
              className="palette-results"
              id="palette-results"
              role="listbox"
            >
              {!searchResults.length && (
                <p className="palette-empty" role="status">
                  Aucun résultat. Essaie un nom de table, de colonne ou de
                  programme.
                </p>
              )}
              {searchResults.map((result, index) => (
                <button
                  id={`search-result-${index}`}
                  className={`palette-result ${index === selection ? "active" : ""}`}
                  role="option"
                  aria-selected={index === selection}
                  key={result.id}
                  onClick={() => openResult(index)}
                >
                  {result.label} <small>{result.detail}</small>
                </button>
              ))}
            </div>
            <div className="palette-help">
              ↑ ↓ naviguer · Entrée ouvrir · Échap fermer
            </div>
          </div>
        </div>
      )}
    </Atlas.Provider>
  );
}
