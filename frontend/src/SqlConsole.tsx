import { useRef, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { ApiError, request } from "./api";
import { useAtlas } from "./context";
import { display, errorText, href, save, stored } from "./model";
import type { Session, SqlResponse, Table } from "./model";
import { Editor } from "./Editor";
import { Icon } from "./Icon";

export function SqlConsole({
  engine,
  table,
}: {
  engine: string;
  table?: Table;
}) {
  const {
    config,
    tables,
    sql,
    updateSql,
    connection,
    status,
    sessionId,
    expired,
    notice,
    refreshSession,
    impact,
    programs,
  } = useAtlas();
  const client = useQueryClient();
  const busy = useRef<Record<string, boolean>>({});
  const state = sql[engine] || { text: "", running: false };
  const source = connection(engine);
  const quote = (value: string) =>
    source?.driver === "mysql"
      ? `\`${value.replaceAll("`", "``")}\``
      : `"${value.replaceAll('"', '""')}"`;
  const defaultSql = table
    ? `SELECT * FROM ${quote(table.schema)}.${quote(table.name)} LIMIT 100;`
    : "SELECT 1;";
  const text = sql[engine]?.text ?? defaultSql;
  const [history, setHistory] = useState(() =>
    stored<{ engine: string; sql: string; at?: string }[]>(
      "db-atlas-sql-history",
      [],
    ),
  );
  const [historySearch, setHistorySearch] = useState("");
  const tx = Boolean(status?.transactions[engine] ?? state.response?.txOpen);
  const run = async (confirmed = false) => {
    if (!source || busy.current[engine] || expired || state.uncertain) return;
    busy.current[engine] = true;
    updateSql(engine, {
      running: true,
      error: undefined,
      response: undefined,
      text,
    });
    try {
      const response = await request<SqlResponse>("/api/sql", {
        engine,
        sql: text,
        confirmed,
      });
      updateSql(engine, { response });
      if (!response.needsConfirmation) {
        const entries = [
          { engine, sql: text, at: new Date().toISOString() },
          ...history.filter(
            (item) => item.engine !== engine || item.sql !== text,
          ),
        ].slice(0, 50);
        save("db-atlas-sql-history", entries);
        setHistory(entries);
        await client.invalidateQueries({ queryKey: ["rows", sessionId] });
      }
    } catch (error) {
      const uncertain = !(error instanceof ApiError) || error.status >= 500;
      updateSql(engine, {
        error: `${errorText(error)}${uncertain ? " — résultat indéterminé : vérifier la session avant toute nouvelle écriture." : ""}`,
        uncertain,
      });
    } finally {
      busy.current[engine] = false;
      updateSql(engine, { running: false });
      await refreshSession();
    }
  };
  const end = async (action: "commit" | "rollback") => {
    if (busy.current[engine] || expired || state.uncertain) return;
    busy.current[engine] = true;
    updateSql(engine, { running: true });
    try {
      await request(`/api/sql/${action}`, { engine });
      updateSql(engine, { response: undefined, error: undefined });
      await client.invalidateQueries({ queryKey: ["rows", sessionId] });
    } catch (error) {
      updateSql(engine, {
        error: `${errorText(error)} — vérifier la session avant de poursuivre.`,
        uncertain: true,
      });
    } finally {
      busy.current[engine] = false;
      updateSql(engine, { running: false });
      await refreshSession();
    }
  };
  const announced = tables
    .filter(
      (t) =>
        t.engine === engine &&
        new RegExp(
          `\\b${t.name.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}\\b`,
          "i",
        ).test(text),
    )
    .flatMap((t) => impact(t)?.triggers || []);
  const triggers = (programs?.engines[engine]?.programs || []).filter(
    (program) => announced.includes(program.id),
  );
  if (!source)
    return (
      <div className="page">
        <h1 className="page-title">Connexion inconnue</h1>
      </div>
    );
  const connectionHistory = history.filter(
    (item) =>
      item &&
      item.engine === engine &&
      typeof item.sql === "string" &&
      item.sql
        .toLocaleLowerCase("fr")
        .includes(historySearch.toLocaleLowerCase("fr")),
  );
  return (
    <div
      className={`sql-console sql-workbench ${table ? "embedded-console" : "page"}`}
    >
      {!table && (
        <header className="page-header">
          <div className="eyebrow">ESPACE DE REQUÊTES</div>
          <h1 className="page-title">Console SQL</h1>
          <p className="page-subtitle">
            Écris une requête, consulte ses résultats et garde le contrôle sur
            les modifications.
          </p>
        </header>
      )}
      <div className="sql-connection-strip">
        <nav
          className="engine-tabs connection-tabs"
          aria-label="Base de la console SQL"
        >
          {config.connections.map((c) => (
            <a
              className={`engine-tab ${c.id === engine ? "active" : ""}`}
              href={href({ view: "sql", engine: c.id })}
              aria-current={c.id === engine ? "page" : undefined}
              key={c.id}
            >
              <Icon name="database" />
              {c.label}
            </a>
          ))}
        </nav>
        <span
          className={`access-badge ${source.allow_writes ? "writable" : ""}`}
        >
          {source.allow_writes ? "Écritures autorisées" : "Lecture seule"}
        </span>
      </div>
      <div className="sql-session-strip">
        <span>
          <i className={`status-dot ${state.uncertain ? "loading" : ""}`} />
          {source.driver === "postgresql" ? "PostgreSQL" : "MySQL"}
          <span className="meta-divider">/</span>
          <code>{source.default_schema}</code>
        </span>
        <span>Session propre à cet onglet</span>
      </div>
      {source.allow_writes && (
        <div className={`transaction-card ${tx ? "is-open" : ""}`}>
          <div>
            <strong>
              {state.uncertain
                ? "État de la session à vérifier"
                : tx
                  ? "Modifications en attente de validation"
                  : "Aucune transaction ouverte"}
            </strong>
            <p>
              Les écritures restent en attente jusqu’à Valider ou Annuler.
              {source.driver === "mysql" &&
                " MySQL peut valider implicitement les opérations de structure."}
            </p>
          </div>
          <div className="action-group">
            <button
              className="button sql-commit"
              id="sql-commit"
              disabled={!tx || state.running || expired || state.uncertain}
              onClick={() => void end("commit")}
            >
              Valider
            </button>
            <button
              className="button danger"
              id="sql-rollback"
              disabled={!tx || state.running || expired || state.uncertain}
              onClick={() => void end("rollback")}
            >
              Annuler
            </button>
          </div>
        </div>
      )}
      <div className="sql-workbench-grid">
        <div className="sql-main-column">
          <section
            className="query-editor-card"
            aria-label="Éditeur de requête"
          >
            <div className="editor-heading">
              <h2>
                <Icon name="sql" />
                Requête SQL
              </h2>
              <span>{text.split("\n").length} lignes</span>
            </div>
            <Editor
              value={text}
              onChange={(value) =>
                updateSql(engine, { text: value, response: undefined })
              }
              onRun={() => {
                void run();
              }}
              driver={source.driver}
              tables={tables.filter((t) => t.engine === engine)}
              defaultSchema={source.default_schema}
              programs={programs?.engines[engine]?.programs || []}
              errorSql={
                state.response?.error &&
                Array.isArray(state.response.statements)
                  ? state.response.statements?.[state.response.error.index]?.sql
                  : undefined
              }
            />
            <div className="query-actions">
              <button
                className="button primary"
                id="sql-run"
                disabled={state.running || expired || state.uncertain}
                onClick={() => {
                  void run();
                }}
              >
                <Icon name="sql" />
                {state.running ? "Exécution…" : "Exécuter"}
                <kbd>Ctrl Entrée</kbd>
              </button>
              <button
                className="button"
                disabled={state.running || !text.trim()}
                onClick={() => {
                  void import("sql-formatter")
                    .then(({ format }) =>
                      updateSql(engine, {
                        text: format(text, {
                          language:
                            source.driver === "mysql" ? "mysql" : "postgresql",
                        }),
                        response: undefined,
                      }),
                    )
                    .catch((error) => notice(errorText(error)));
                }}
              >
                Formater
              </button>
              <span className="query-help">
                {source.allow_writes
                  ? "Les modifications passent par une transaction."
                  : "Seules les requêtes de lecture sont autorisées."}
              </span>
            </div>
          </section>
          {triggers.length > 0 && (
            <details className="info-callout warning">
              <summary>
                {triggers.length} déclencheur(s) potentiellement concerné(s)
              </summary>
              {triggers.map((trigger) => (
                <p key={trigger.id}>
                  {trigger.name} · {trigger.events?.join("/")}
                </p>
              ))}
              <p>
                Indication déduite du texte SQL, à confirmer dans les
                définitions.
              </p>
            </details>
          )}
          {state.error && (
            <div className="feedback-card error" role="alert">
              <strong>La requête n’a pas abouti</strong>
              <p>{state.error}</p>
            </div>
          )}
          {state.uncertain && (
            <div className="info-callout warning">
              <p>Vérifie la session avant de lancer une autre requête.</p>
              <button
                className="button"
                onClick={() => {
                  void request<Session>("/api/sessions/current")
                    .then(async (status) => {
                      if (status.active)
                        notice(
                          "Une requête est encore active. Attends sa fin.",
                        );
                      else {
                        updateSql(engine, {
                          uncertain: false,
                          response: undefined,
                        });
                        notice(
                          status.transactions[engine]
                            ? "Transaction toujours ouverte : examine les données ou annule-la avant de reprendre."
                            : "Aucune transaction ouverte. Vérifie les données avant de relancer une instruction.",
                        );
                        await refreshSession();
                      }
                    })
                    .catch((error) => notice(errorText(error)));
                }}
              >
                Vérifier la session
              </button>
            </div>
          )}
          {state.response?.needsConfirmation && (
            <section className="sql-confirm execution-confirm" role="alert">
              <h3>Confirmer cette modification</h3>
              <p>
                Vérifie les instructions et leurs effets avant de poursuivre.
              </p>
              {state.response.confirmations?.map((confirmation, index) => (
                <div className="sql-confirm-item" key={index}>
                  <code>{display(confirmation.sql)}</code>
                  <p>
                    {Array.isArray(confirmation.reasons)
                      ? confirmation.reasons.map(display).join(" · ")
                      : ""}
                  </p>
                </div>
              ))}
              <div className="action-group">
                <button
                  className="button danger"
                  disabled={state.running || expired}
                  onClick={() => {
                    void run(true);
                  }}
                >
                  Confirmer l’exécution
                </button>
                <button
                  className="button"
                  disabled={state.running}
                  onClick={() => updateSql(engine, { response: undefined })}
                >
                  Renoncer
                </button>
              </div>
            </section>
          )}
          {state.response?.implicitCommit && (
            <p className="info-callout warning" role="status">
              MySQL a validé implicitement la transaction.
            </p>
          )}
          {state.response?.error && (
            <div className="feedback-card error" role="alert">
              <strong>
                Erreur dans l’instruction {state.response.error.index + 1}
              </strong>
              <p>{state.response.error.message}</p>
            </div>
          )}
          <section
            id="sql-results"
            className="query-results-card"
            aria-label="Résultats SQL"
            aria-busy={state.running}
          >
            <header className="results-heading">
              <h2>Résultats</h2>
              <span aria-live="polite">
                {state.running
                  ? "Exécution en cours…"
                  : state.response?.results?.length
                    ? `${state.response.results.length} instruction(s)`
                    : ""}
              </span>
            </header>
            {!state.response?.results?.length && (
              <div className="results-empty">
                <Icon name="table" />
                <strong>
                  {state.running
                    ? "La base traite la requête…"
                    : "Les résultats s’afficheront ici"}
                </strong>
                <p>
                  {state.running
                    ? "Attends la fin de l’exécution pour confirmer l’état des données."
                    : "Lance une requête avec Exécuter ou Ctrl Entrée."}
                </p>
              </div>
            )}
            {state.response?.results?.map((result) => (
              <div className="sql-result" key={result.index}>
                <div className="sql-result-heading">
                  <strong>Instruction {result.index + 1}</strong>
                  <span className="state-badge neutral">
                    {{ read: "Lecture", write: "Écriture", ddl: "Structure" }[
                      result.kind
                    ] || result.kind}
                  </span>
                  <span>{result.durationMs} ms</span>
                  {result.affected != null && (
                    <span>{result.affected} ligne(s) concernée(s)</span>
                  )}
                </div>
                {result.columns && (
                  <div className="data-scroll">
                    <table className="data-table">
                      <thead>
                        <tr>
                          {result.columns.map((column, index) => (
                            <th key={`${column}:${index}`}>{column}</th>
                          ))}
                        </tr>
                      </thead>
                      <tbody>
                        {result.rows?.map((row, index) => (
                          <tr key={index}>
                            {row.map((value, column) => (
                              <td key={column}>{display(value)}</td>
                            ))}
                          </tr>
                        ))}
                      </tbody>
                    </table>
                    {!result.rows?.length && (
                      <p className="compact-empty">
                        La requête ne renvoie aucune ligne.
                      </p>
                    )}
                  </div>
                )}
                {result.hasMore && (
                  <p className="info-callout">
                    Résultat limité à 500 lignes. Ajoute un filtre pour affiner
                    la recherche.
                  </p>
                )}
              </div>
            ))}
          </section>
        </div>
        <aside className="sql-tools-column" aria-label="Aide et historique SQL">
          <details className="sql-history tool-card" open>
            <summary>
              Historique SQL
              <span className="count-badge">
                {history.filter((item) => item?.engine === engine).length}
              </span>
            </summary>
            <p className="section-help">
              Requêtes exécutées sur {source.label}. Sélectionne une requête
              pour la reprendre.
            </p>
            <input
              className="tool-search"
              type="search"
              aria-label="Filtrer l’historique SQL"
              placeholder="Retrouver une requête…"
              value={historySearch}
              onChange={(event) => setHistorySearch(event.target.value)}
            />
            <div className="history-list">
              {connectionHistory.map((item, index) => (
                <button
                  className="history-entry"
                  key={`${item.sql}:${index}`}
                  onClick={() =>
                    updateSql(engine, { text: item.sql, response: undefined })
                  }
                >
                  <code>{item.sql}</code>
                  <small>
                    {item.at
                      ? new Date(item.at).toLocaleString("fr")
                      : "Requête précédente"}
                  </small>
                </button>
              ))}
              {!connectionHistory.length && (
                <div className="compact-empty">
                  {historySearch
                    ? "Aucune requête ne correspond."
                    : "Aucune requête enregistrée pour cette base."}
                </div>
              )}
            </div>
          </details>
          <section className="tool-card keyboard-guide">
            <h2>À portée de clavier</h2>
            <p>
              <span>Exécuter</span>
              <kbd>Ctrl Entrée</kbd>
            </p>
            <p>
              <span>Autocompléter</span>
              <kbd>Ctrl Espace</kbd>
            </p>
            <p>
              <span>Recherche globale</span>
              <kbd>Ctrl K</kbd>
            </p>
            <div className="section-help">
              L’historique est conservé dans ce navigateur. Les transactions
              appartiennent à cet onglet.
            </div>
          </section>
        </aside>
      </div>
    </div>
  );
}
