import { useEffect, useRef, useState } from "react";
import { Icon } from "./Icon";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ApiError, request } from "./api";
import { useSulcata } from "./context";
import { display, errorText, keyOf, tableHref } from "./model";
import type { RefsResponse, RowsResponse, SqlResponse, Table } from "./model";

export function DataView({ table }: { table: Table }) {
  const {
    route,
    connection,
    sessionId,
    expired,
    notice,
    refreshSession,
    status,
    updateSql,
  } = useSulcata();
  const source = connection(table.engine);
  const client = useQueryClient();
  const [filters, setFilters] = useState<Record<string, string>>(
    route.params || {},
  );
  const [draftFilters, setDraftFilters] = useState<Record<string, string>>(
    route.params || {},
  );
  const [page, setPage] = useState(0);
  const [pageSize, setPageSize] = useState(100);
  const [sort, setSort] = useState<{
    column: string;
    dir: "asc" | "desc";
  } | null>(null);
  const [row, setRow] = useState<Record<string, unknown> | null>(null);
  const [edit, setEdit] = useState<{
    row: Record<string, unknown>;
    column: string;
    value: string;
    isNull: boolean;
    preview: boolean;
  } | null>(null);
  const [uncertain, setUncertain] = useState(false);
  const rowSelection = useRef<ReturnType<typeof setTimeout> | null>(null);
  useEffect(
    () => () => {
      if (rowSelection.current) clearTimeout(rowSelection.current);
    },
    [page, pageSize, sort, filters],
  );
  const cellInput = useRef<HTMLInputElement>(null);
  useEffect(() => {
    cellInput.current?.scrollIntoView({ block: "nearest" });
    cellInput.current?.focus({ preventScroll: true });
  }, [edit?.row, edit?.column]);
  const query = useQuery({
    queryKey: ["rows", sessionId, keyOf(table), filters, page, pageSize, sort],
    queryFn: () =>
      request<RowsResponse>("/api/data/rows", {
        engine: table.engine,
        schema: table.schema,
        table: table.name,
        filters,
        page,
        pageSize,
        sort,
      }),
    enabled: Boolean(sessionId) && !expired,
  });
  const quote = (name: string) =>
    source?.driver === "mysql"
      ? `\`${name.replaceAll("`", "``")}\``
      : `"${name.replaceAll('"', '""')}"`;
  const primary = table.columns.filter((column) => column.primary);
  const editingSql = edit
    ? `UPDATE ${quote(table.schema)}.${quote(table.name)} SET ${quote(edit.column)} = ? WHERE ${primary.map((column) => `${quote(column.name)} = ?`).join(" AND ")};`
    : "";
  const mutation = useMutation({
    mutationFn: async () => {
      if (!edit || !primary.length) throw new Error("Clé primaire requise");
      const response = await request<SqlResponse>("/api/sql", {
        engine: table.engine,
        sql: editingSql,
        params: [
          edit.isNull ? null : edit.value,
          ...primary.map((column) => edit.row[column.name]),
        ],
      });
      if (response.error) throw new ApiError(response.error.message, 400);
      return response;
    },
    onSuccess: async (response) => {
      if (response.results?.[0]?.affected !== 1)
        notice(
          `${response.results?.[0]?.affected} ligne(s) concernée(s) au lieu d’une. Vérifie ou annule la transaction.`,
        );
      setEdit(null);
      setRow(null);
      await client.invalidateQueries({ queryKey: ["rows", sessionId] });
      await refreshSession();
    },
    onError: (error) => {
      const unknown = !(error instanceof ApiError) || error.status >= 500;
      if (unknown) {
        setUncertain(true);
        updateSql(table.engine, { uncertain: true });
      }
      notice(
        `${errorText(error)}${unknown ? " — résultat indéterminé. Vérifier la session dans la console SQL ; ne pas relancer automatiquement." : ""}`,
      );
      void refreshSession();
    },
  });
  const end = useMutation({
    mutationFn: (action: "commit" | "rollback") =>
      request(`/api/sql/${action}`, { engine: table.engine }),
    onSuccess: async () => {
      await client.invalidateQueries({ queryKey: ["rows", sessionId] });
      await refreshSession();
    },
    onError: (error) => {
      notice(`${errorText(error)} — vérifie la session dans la console SQL.`);
      updateSql(table.engine, { uncertain: true });
      setUncertain(true);
    },
  });
  const blocked = expired || mutation.isPending || end.isPending || uncertain;
  return (
    <div className="data-view data-workbench">
      <div className="data-toolbar">
        <div className="action-group">
          <button
            className="button"
            disabled={query.isFetching || expired}
            onClick={() => {
              void query.refetch();
            }}
          >
            <Icon name="refresh" />
            Recharger
          </button>
          <label className="page-size-field">
            Lignes par page
            <select
              aria-label="Lignes par page"
              value={pageSize}
              onChange={(event) => {
                setPageSize(Number(event.target.value));
                setPage(0);
                setRow(null);
              }}
            >
              {[25, 100, 500].map((size) => (
                <option key={size}>{size}</option>
              ))}
            </select>
          </label>
        </div>
        <span className="data-result-count" aria-live="polite">
          {query.isFetching
            ? "Chargement…"
            : `${query.data?.rows.length || 0} lignes affichées`}
          {query.data?.durationMs != null && !query.isFetching && (
            <small>{query.data.durationMs} ms</small>
          )}
        </span>
        <div className="pagination-group" aria-label="Pagination des données">
          <button
            className="button"
            disabled={page === 0 || query.isFetching}
            onClick={() => {
              setPage((p) => p - 1);
              setRow(null);
            }}
          >
            Précédent
          </button>
          <span>Page {page + 1}</span>
          <button
            className="button"
            disabled={!query.data?.hasMore || query.isFetching}
            onClick={() => {
              setPage((p) => p + 1);
              setRow(null);
            }}
          >
            Suivant
          </button>
        </div>
      </div>
      {status?.transactions[table.engine] && (
        <div className="transaction-card is-open">
          <div>
            <strong>Transaction ouverte</strong>
            <p>Les modifications restent en attente de validation.</p>
          </div>
          <div className="action-group">
            <button
              className="button sql-commit"
              disabled={blocked}
              onClick={() => end.mutate("commit")}
            >
              Valider
            </button>
            <button
              className="button danger"
              disabled={blocked}
              onClick={() => end.mutate("rollback")}
            >
              Annuler
            </button>
          </div>
        </div>
      )}
      <p className="section-help filter-help">
        Filtres : texte, =valeur, &gt;valeur, null ou !null. Entrée pour
        appliquer.{" "}
        {source?.allow_writes
          ? "Double-clic sur une cellule pour modifier (clé primaire requise)."
          : "Lecture seule."}
      </p>
      {query.error && (
        <p role="alert" className="data-error">
          {errorText(query.error)}
        </p>
      )}
      {query.isFetching && <p role="status">Chargement des lignes…</p>}
      {Object.values(draftFilters).some(Boolean) && (
        <button
          className="button clear-filters"
          onClick={() => {
            setFilters({});
            setDraftFilters({});
            setPage(0);
            setRow(null);
            history.replaceState(null, "", tableHref(table, "donnees"));
          }}
        >
          Effacer les filtres
        </button>
      )}
      <div className={`data-content-grid ${row ? "with-row-detail" : ""}`}>
        <div className="data-scroll">
          <table className="data-table">
            <thead>
              <tr>
                {table.columns.map((column) => (
                  <th
                    key={column.name}
                    aria-sort={
                      sort?.column === column.name
                        ? sort.dir === "asc"
                          ? "ascending"
                          : "descending"
                        : "none"
                    }
                  >
                    <button
                      className="link-button"
                      onClick={() => {
                        setSort({
                          column: column.name,
                          dir:
                            sort?.column === column.name && sort.dir === "asc"
                              ? "desc"
                              : "asc",
                        });
                        setPage(0);
                      }}
                    >
                      {column.primary ? "🔑 " : ""}
                      {column.name}
                      {sort?.column === column.name
                        ? sort.dir === "asc"
                          ? " ↑"
                          : " ↓"
                        : ""}
                    </button>
                  </th>
                ))}
              </tr>
              <tr>
                {table.columns.map((column) => (
                  <th key={column.name}>
                    <input
                      className="column-filter"
                      aria-label={`Filtrer ${column.name}`}
                      placeholder="Filtrer…"
                      value={draftFilters[column.name] || ""}
                      onChange={(event) =>
                        setDraftFilters((previous) => ({
                          ...previous,
                          [column.name]: event.target.value,
                        }))
                      }
                      onKeyDown={(event) => {
                        if (event.key === "Enter") {
                          const next = draftFilters;
                          setRow(null);
                          setFilters(next);
                          setPage(0);
                          const hash = tableHref(table, "donnees", next);
                          history.replaceState(null, "", hash);
                        }
                      }}
                    />
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {query.data?.rows.map((record, index) => (
                <tr
                  key={index}
                  tabIndex={0}
                  aria-selected={row === record}
                  onKeyDown={(event) => {
                    if (
                      event.target === event.currentTarget &&
                      (event.key === "Enter" || event.key === " ")
                    ) {
                      event.preventDefault();
                      if (rowSelection.current)
                        clearTimeout(rowSelection.current);
                      setRow(record);
                    }
                  }}
                  className={row === record ? "selected" : ""}
                  onClick={(event) => {
                    if (rowSelection.current)
                      clearTimeout(rowSelection.current);
                    // Wait for the second click before resizing the table for its
                    // inspector, otherwise the cell can move during a double-click.
                    if (event.detail === 1)
                      rowSelection.current = setTimeout(
                        () => setRow(record),
                        500,
                      );
                  }}
                >
                  {table.columns.map((column) => {
                    const value = record[column.name];
                    const editable =
                      source?.allow_writes &&
                      primary.length > 0 &&
                      primary.every(
                        (pk) =>
                          record[pk.name] != null &&
                          typeof record[pk.name] !== "object",
                      ) &&
                      !/blob|binary|bytea/i.test(column.type) &&
                      (value == null || typeof value !== "object");
                    const fk = table.foreignKeys.find(
                      (fk) => fk.column === column.name,
                    );
                    return (
                      <td
                        key={column.name}
                        title={editable ? "Double-clic : modifier" : undefined}
                        onDoubleClick={() => {
                          if (rowSelection.current)
                            clearTimeout(rowSelection.current);
                          setRow(record);
                          if (editable && !blocked)
                            setEdit({
                              row: record,
                              column: column.name,
                              value: value == null ? "" : String(value),
                              isNull: value == null,
                              preview: false,
                            });
                        }}
                      >
                        {fk && value != null ? (
                          <a
                            className="data-fk"
                            href={tableHref(
                              {
                                ...table,
                                schema: fk.targetSchema,
                                name: fk.targetTable,
                              },
                              "donnees",
                              { [fk.targetColumn]: `=${display(value)}` },
                            )}
                          >
                            {display(value)}
                          </a>
                        ) : (
                          display(value)
                        )}
                      </td>
                    );
                  })}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
        {row && (
          <RowDetail
            table={table}
            row={row}
            close={() => setRow(null)}
            editColumn={
              source?.allow_writes &&
              !blocked &&
              primary.length > 0 &&
              primary.every(
                (column) =>
                  row[column.name] !== null && row[column.name] !== undefined,
              )
                ? (column) =>
                    setEdit({
                      row,
                      column: column.name,
                      value:
                        row[column.name] == null
                          ? ""
                          : String(row[column.name]),
                      isNull: row[column.name] === null,
                      preview: false,
                    })
                : undefined
            }
          />
        )}
      </div>
      {!query.isFetching && query.data?.rows.length === 0 && (
        <p>Aucune ligne.</p>
      )}
      {edit && (
        <div
          className="edit-preview sql-confirm"
          role="dialog"
          aria-label="Modifier une cellule"
        >
          <h3>Modifier une cellule</h3>
          <p className="section-help">
            Vérifie la valeur et la ligne ciblée avant de prévisualiser la
            requête.
          </p>
          <label>
            {edit.column}
            <input
              id="cell-input"
              ref={cellInput}
              className="cell-input"
              value={edit.value}
              onChange={(event) =>
                setEdit({
                  ...edit,
                  value: event.target.value,
                  isNull: false,
                  preview: false,
                })
              }
              onKeyDown={(event) => {
                if (event.key === "Enter") setEdit({ ...edit, preview: true });
                if (event.key === "Escape") setEdit(null);
              }}
            />
          </label>
          <label>
            <input
              type="checkbox"
              checked={edit.isNull}
              onChange={(event) =>
                setEdit({
                  ...edit,
                  isNull: event.target.checked,
                  preview: false,
                })
              }
            />
            NULL
          </label>
          <code>{editingSql}</code>
          <p>
            Paramètres :{" "}
            {JSON.stringify([
              edit.isNull ? null : edit.value,
              ...primary.map((pk) => edit.row[pk.name]),
            ])}
          </p>
          {edit.preview ? (
            <button
              className="button"
              id="edit-run"
              disabled={blocked}
              onClick={() => mutation.mutate()}
            >
              Exécuter la modification
            </button>
          ) : (
            <button
              className="button"
              disabled={blocked}
              onClick={() => setEdit({ ...edit, preview: true })}
            >
              Prévisualiser
            </button>
          )}
          <button
            className="button"
            id="edit-cancel"
            disabled={mutation.isPending}
            onClick={() => setEdit(null)}
          >
            Fermer
          </button>
        </div>
      )}
      {query.data && (
        <details className="read-query-details">
          <summary>SQL de lecture</summary>
          <pre>{query.data.sql}</pre>
        </details>
      )}
    </div>
  );
}
function RowDetail({
  table,
  row,
  close,
  editColumn,
}: {
  table: Table;
  row: Record<string, unknown>;
  close: () => void;
  editColumn?: (column: Table["columns"][number]) => void;
}) {
  const { sessionId, expired, notice } = useSulcata();
  const refs = useQuery({
    queryKey: ["refs", sessionId, keyOf(table), row],
    queryFn: () =>
      request<RefsResponse>("/api/data/refs", {
        engine: table.engine,
        schema: table.schema,
        table: table.name,
        row,
      }),
    enabled: !expired,
  });
  return (
    <section
      className="row-detail selected-row-card"
      aria-label="Détail de la ligne"
    >
      <header>
        <div>
          <div className="eyebrow">DONNÉES</div>
          <h2>Détail de la ligne</h2>
        </div>
        <button
          className="button icon-button"
          onClick={close}
          aria-label="Fermer le détail de la ligne"
        >
          <Icon name="close" />
        </button>
      </header>
      <button
        className="button"
        onClick={() => {
          void navigator.clipboard
            .writeText(JSON.stringify(row, null, 2))
            .then(() => notice("Ligne copiée en JSON"))
            .catch(() => notice("Copie impossible"));
        }}
      >
        Copier en JSON
      </button>
      <h3>Référencée par</h3>
      {refs.isPending && <p>Recherche des lignes liées…</p>}
      {refs.error && <p role="alert">{errorText(refs.error)}</p>}
      {refs.data?.refs.map((ref, index) => (
        <p key={index}>
          {ref.error ? (
            display(ref.error)
          ) : (
            <a
              href={tableHref(
                {
                  ...table,
                  schema: String(ref.schema),
                  name: String(ref.table),
                },
                "donnees",
                { [String(ref.column)]: `=${display(ref.value)}` },
              )}
            >
              {String(ref.schema)}.{String(ref.table)}.{String(ref.column)} ·{" "}
              {display(ref.count)}
              {ref.capped ? "+" : ""} lignes
            </a>
          )}
        </p>
      ))}
      {refs.data?.refs.length === 0 && <p>Aucune relation entrante.</p>}
      <h3>Valeurs des colonnes</h3>
      <dl>
        {table.columns.map((column) => (
          <div className="row-field" key={column.name}>
            <dt className="row-field-name">
              <span>{column.name}</span>
              {editColumn && !/blob|binary|bytea/i.test(column.type) && (
                <button
                  className="link-button"
                  aria-label={`Modifier ${column.name}`}
                  onClick={() => editColumn(column)}
                >
                  Modifier
                </button>
              )}
            </dt>
            <dd className="row-field-value">{display(row[column.name])}</dd>
          </div>
        ))}
      </dl>
    </section>
  );
}
