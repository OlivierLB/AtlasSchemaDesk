import type { components } from "./api.generated";

export type PublicConfig = components["schemas"]["PublicConfig"];
export type Connection = components["schemas"]["ConnectionPublic"];
export type Session = components["schemas"]["SessionStatus"];
export type NewSession = components["schemas"]["NewSession"];
export type Job = components["schemas"]["JobStatus"];
export type UpdateStatus = components["schemas"]["UpdateStatus"];
export type Status = components["schemas"]["Status"];
export type SqlResponse = components["schemas"]["SqlResponse"];
export type RowsResponse = components["schemas"]["RowsResponse"];
export type RefsResponse = components["schemas"]["RefsResponse"];
export type ConnectionsOverview = components["schemas"]["ConnectionsOverview"];
export type ConnectionInput = components["schemas"]["ConnectionInput"];
export type DumpSourceInput = components["schemas"]["DumpSourceInput"];
export type SaveResult = components["schemas"]["SaveResult"];
export type Json =
  null | boolean | number | string | Json[] | { [key: string]: Json };
export interface Column {
  name: string;
  type: string;
  nullable?: boolean;
  primary?: boolean;
  default?: string | null;
  comment?: string;
}
export interface ForeignKey {
  column: string;
  targetSchema: string;
  targetTable: string;
  targetColumn: string;
}
export interface Table {
  engine: string;
  schema: string;
  name: string;
  columns: Column[];
  foreignKeys: ForeignKey[];
  uniqueConstraints?: string[][];
  comment?: string;
}
export type Schema = {
  generatedAt: string;
  sources: Record<string, Omit<Table, "engine">[]>;
};
export interface Impact {
  triggers?: string[];
  writes?: string[];
  reads?: string[];
  indirectWrites?: string[];
  probableWrites?: string[];
  probableReads?: string[];
  calls?: string[];
  calledBy?: string[];
  dynamicSql?: boolean;
}
export interface Program {
  id: string;
  kind: string;
  schema: string;
  name: string;
  body: string;
  signature?: string;
  returns?: string;
  language?: string;
  comment?: string;
  schedule?: string;
  starts?: string;
  eventSchedule?: {
    type: string;
    timeZone: string;
    systemTimeZone: string;
    scheduler: string;
    executeAt: string | null;
    starts: string | null;
    ends: string | null;
    created: string | null;
    intervalValue: string | null;
    intervalField: string | null;
  };
  status?: string;
  lastExecuted?: string;
  timing?: string;
  events?: string[];
  table?: { schema: string; name: string };
  impact: Impact;
  source?: {
    status: string;
    path?: string;
    absolutePath?: string;
    line?: number;
  };
}
export type EventWindowReport = components["schemas"]["EventWindowReport"];
export type EventWindowMatch = components["schemas"]["EventWindowMatch"];
export interface Programs {
  generatedAt: string;
  engines: Record<
    string,
    {
      programs: Program[];
      tableImpacts: Record<string, Impact>;
      tableStats: Record<
        string,
        { rows?: number | null; bytes?: number | null }
      >;
      error?: { message: string };
      sourceRepository?: { available: boolean; counts: Record<string, number> };
    }
  >;
}
export interface VersionIndex {
  versions: {
    generatedAt: string;
    origin: string;
    sources: Record<string, { hash: string; tables: number }>;
    changed: string[];
  }[];
}
export interface Dumps {
  targets: {
    engine: string;
    label: string;
    error?: { message: string };
    versions: { versionId: string; lastModified: string; size: number }[];
  }[];
}
export const tabs = [
  "colonnes",
  "donnees",
  "relations",
  "programmes",
  "voisinage",
  "sql",
] as const;
export type Tab = (typeof tabs)[number];
export interface Route {
  view: "home" | "schema" | "table" | "sql" | "events" | "connections";
  engine?: string;
  schema?: string;
  table?: string;
  tab?: Tab;
  params?: Record<string, string>;
}
export function parseHash(hash: string): Route {
  try {
    const [path, query] = hash.replace(/^#\/?/, "").split("?");
    const parts = path.split("/").filter(Boolean).map(decodeURIComponent);
    if (parts[0] === "events") return { view: "events" };
    if (parts[0] === "connexions")
      return {
        view: "connections",
        params: Object.fromEntries(new URLSearchParams(query)),
      };
    if (parts[0] === "sql") return { view: "sql", engine: parts[1] };
    if (parts.length >= 3)
      return {
        view: "table",
        engine: parts[0],
        schema: parts[1],
        table: parts[2],
        tab: tabs.includes(parts[3] as Tab) ? (parts[3] as Tab) : "colonnes",
        params: Object.fromEntries(new URLSearchParams(query)),
      };
    if (parts.length === 2)
      return { view: "schema", engine: parts[0], schema: parts[1] };
  } catch {
    /* A malformed external hash must not crash the application. */
  }
  return { view: "home" };
}
export function href(route: Route): string {
  const enc = encodeURIComponent;
  if (route.view === "events") return "#/events";
  if (route.view === "connections") return "#/connexions";
  if (route.view === "sql") return `#/sql/${enc(route.engine || "")}`;
  if (route.view === "schema")
    return `#/${enc(route.engine || "")}/${enc(route.schema || "")}`;
  if (route.view === "table") {
    const query = new URLSearchParams(
      Object.entries(route.params || {}).filter(([, v]) => v !== ""),
    ).toString();
    return `#/${enc(route.engine || "")}/${enc(route.schema || "")}/${enc(route.table || "")}/${route.tab || "colonnes"}${query ? `?${query}` : ""}`;
  }
  return "#/";
}
export const tableHref = (
  table: Table,
  tab: Tab = "colonnes",
  params?: Record<string, string>,
) =>
  href({
    view: "table",
    engine: table.engine,
    schema: table.schema,
    table: table.name,
    tab,
    params,
  });
export const keyOf = (table: Pick<Table, "engine" | "schema" | "name">) =>
  `${table.engine}\u0000${table.schema}\u0000${table.name}`;
export const display = (value: unknown): string => {
  if (value === null || value === undefined) return "∅";
  if (typeof value === "object") {
    if ("binary" in value && "length" in value)
      return `[binaire : ${String(value.length)} octets]`;
    if ("truncated" in value && "value" in value)
      return `${String(value.value)}…`;
    return JSON.stringify(value);
  }
  return String(value);
};
export const errorText = (error: unknown) =>
  error instanceof Error ? error.message : String(error);
export function stored<T>(key: string, fallback: T): T {
  try {
    const parsed: unknown = JSON.parse(localStorage.getItem(key) || "null");
    if (parsed === null || typeof parsed !== typeof fallback) return fallback;
    if (Array.isArray(fallback))
      return Array.isArray(parsed) ? (parsed as T) : fallback;
    if (typeof fallback === "object") {
      if (Array.isArray(parsed)) return fallback;
      const record = parsed as Record<string, unknown>;
      if (
        Object.entries(fallback as Record<string, unknown>).some(
          ([name, value]) => typeof record[name] !== typeof value,
        )
      )
        return fallback;
    }
    return parsed as T;
  } catch {
    return fallback;
  }
}
export function save(key: string, value: unknown) {
  try {
    localStorage.setItem(key, JSON.stringify(value));
  } catch {
    /* Storage can be disabled. */
  }
}
