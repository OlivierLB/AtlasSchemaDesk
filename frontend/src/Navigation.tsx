import { useEffect, useRef, useState } from "react";
import type { CSSProperties } from "react";
import { useSulcata } from "./context";
import { href, keyOf, tableHref } from "./model";
import { Icon } from "./Icon";

export function Navigation({
  filter,
  onFilter,
  versionsOpen,
  onVersions,
  onNavigate,
}: {
  filter: string;
  onFilter: (value: string) => void;
  versionsOpen: boolean;
  onVersions: () => void;
  onNavigate: () => void;
}) {
  const { route, config, tables } = useSulcata();
  const routeKey = `${route.engine || ""}:${route.schema || ""}:${route.table || ""}`;
  const [expanded, setExpanded] = useState<{
    routeKey: string;
    engine: string | null;
  }>({ routeKey, engine: route.engine || null });
  const expandedEngine =
    expanded.routeKey === routeKey ? expanded.engine : route.engine || null;
  const needle = filter.trim().toLocaleLowerCase("fr");
  const tree = useRef<HTMLElement>(null);
  useEffect(() => {
    const active = tree.current?.querySelector<HTMLElement>(
      '.tree-table[aria-current="page"]',
    );
    const body = active?.closest<HTMLElement>(".tree-database-body");
    if (!active || !body) return;
    const bounds = active.getBoundingClientRect();
    const offset = bounds.top - body.getBoundingClientRect().top;
    if (offset < 0 || offset + bounds.height > body.clientHeight)
      body.scrollTop += offset - body.clientHeight / 2 + bounds.height / 2;
  }, [routeKey, tables, needle, expandedEngine]);
  const visible = tables.filter((table) =>
    `${config.connections.find((c) => c.id === table.engine)?.label} ${table.engine} ${table.schema}.${table.name}`
      .toLocaleLowerCase("fr")
      .includes(needle),
  );
  const links = [
    {
      view: "home",
      href: "#/",
      icon: "home",
      label: "Vue d’ensemble",
      id: "home-open",
    },
    {
      view: "sql",
      href: href({
        view: "sql",
        engine: route.engine || config.connections[0]?.id || "",
      }),
      icon: "sql",
      label: "Console SQL",
      id: "sql-open",
    },
    {
      view: "events",
      href: "#/events",
      icon: "activity",
      label: "Événements",
      id: "events-toggle",
    },
  ] as const;
  return (
    <>
      <nav className="primary-nav" aria-label="Navigation principale">
        {links.map((link) => (
          <a
            key={link.view}
            id={link.id}
            href={link.href}
            className={`nav-item ${route.view === link.view ? "active" : ""}`}
            aria-current={route.view === link.view ? "page" : undefined}
            onClick={onNavigate}
          >
            <Icon name={link.icon} />
            {link.label}
          </a>
        ))}
      </nav>
      <div className="sidebar-controls">
        <label className="sidebar-label" htmlFor="tree-filter">
          Bases de données <span>{config.connections.length}</span>
        </label>
        <div className="filter-field">
          <Icon name="search" />
          <input
            className="tree-filter"
            id="tree-filter"
            type="search"
            placeholder="Filtrer les bases et tables…"
            value={filter}
            onChange={(event) => onFilter(event.target.value)}
          />
        </div>
      </div>
      <nav
        ref={tree}
        id="tree"
        className="tree"
        aria-label="Bases, schémas et tables"
      >
        {config.connections
          .filter((c) => !needle || visible.some((t) => t.engine === c.id))
          .map((connection) => {
            const list = visible.filter((t) => t.engine === connection.id);
            const schemas = [...new Set(list.map((t) => t.schema))].sort();
            const isOpen = Boolean(needle) || expandedEngine === connection.id;
            return (
              <section
                key={connection.id}
                style={
                  { "--connection-color": connection.color } as CSSProperties
                }
              >
                <h2 className="tree-engine">
                  <button
                    className="tree-engine-toggle"
                    aria-expanded={isOpen}
                    aria-controls={`tree-database-${connection.id}`}
                    onClick={() =>
                      setExpanded({
                        routeKey,
                        engine: isOpen ? null : connection.id,
                      })
                    }
                    disabled={Boolean(needle)}
                  >
                    <span className="tree-chevron" aria-hidden="true">
                      {isOpen ? "▾" : "▸"}
                    </span>
                    <Icon name="database" />
                    <span className="tree-engine-name">{connection.label}</span>
                    <span className="tree-count">{list.length}</span>
                  </button>
                </h2>
                <div
                  hidden={!isOpen}
                  className="tree-database-body"
                  id={`tree-database-${connection.id}`}
                >
                  {isOpen && (
                    <>
                      <a
                        className="tree-sql-link"
                        href={href({ view: "sql", engine: connection.id })}
                        onClick={onNavigate}
                      >
                        <Icon name="sql" />
                        Ouvrir SQL
                      </a>
                      {schemas.map((schema) => (
                        <details
                          key={`${schema}:${Boolean(needle)}`}
                          open={
                            Boolean(needle) ||
                            route.schema === schema ||
                            (!route.schema && schema === schemas[0])
                          }
                        >
                          <summary>
                            <a
                              href={href({
                                view: "schema",
                                engine: connection.id,
                                schema,
                              })}
                              onClick={onNavigate}
                            >
                              {schema}
                            </a>
                            <span className="tree-count">
                              {list.filter((t) => t.schema === schema).length}
                            </span>
                          </summary>
                          {list
                            .filter((t) => t.schema === schema)
                            .map((table) => (
                              <a
                                key={keyOf(table)}
                                href={tableHref(table)}
                                className={`tree-table ${route.table === table.name && route.engine === table.engine && route.schema === table.schema ? "active" : ""}`}
                                aria-current={
                                  route.table === table.name &&
                                  route.engine === table.engine &&
                                  route.schema === table.schema
                                    ? "page"
                                    : undefined
                                }
                                onClick={onNavigate}
                              >
                                <Icon name="table" />
                                <span>{table.name}</span>
                              </a>
                            ))}
                        </details>
                      ))}
                      {!list.length && (
                        <p className="tree-empty">
                          Aucune table dans le catalogue.
                        </p>
                      )}
                    </>
                  )}
                </div>
              </section>
            );
          })}
        {needle && !visible.length && (
          <div className="tree-empty" role="status">
            Aucun résultat pour « {filter} ».
            <button className="link-button" onClick={() => onFilter("")}>
              Effacer le filtre
            </button>
          </div>
        )}
      </nav>
      <nav className="sidebar-footer" aria-label="Configuration et sauvegardes">
        <a
          id="connections-open"
          className={`nav-item ${route.view === "connections" ? "active" : ""}`}
          aria-current={route.view === "connections" ? "page" : undefined}
          href="#/connexions"
          onClick={onNavigate}
        >
          <Icon name="settings" />
          Connexions et sauvegardes
        </a>
        <button
          id="versions-toggle"
          className={`nav-item ${versionsOpen ? "active" : ""}`}
          aria-expanded={versionsOpen}
          onClick={() => {
            onVersions();
            onNavigate();
          }}
        >
          <Icon name="backup" />
          Versions et restauration
        </button>
      </nav>
    </>
  );
}
