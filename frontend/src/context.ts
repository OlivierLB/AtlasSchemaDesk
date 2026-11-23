import { createContext, useContext } from "react";
import type {
  Connection,
  Impact,
  Program,
  Programs,
  PublicConfig,
  Route,
  Schema,
  SqlResponse,
  Status,
  Table,
} from "./model";

export interface SqlState {
  text: string;
  response?: SqlResponse;
  error?: string;
  running: boolean;
  uncertain?: boolean;
}
export interface AtlasContext {
  config: PublicConfig;
  schema?: Schema;
  programs?: Programs;
  tables: Table[];
  route: Route;
  status?: Status;
  sessionId: string;
  expired: boolean;
  selectedVersions: Record<string, string>;
  selectVersion: (engine: string, hash: string) => void;
  sql: Record<string, SqlState>;
  updateSql: (engine: string, patch: Partial<SqlState>) => void;
  connection: (id: string) => Connection | undefined;
  impact: (table: Table) => Impact | undefined;
  openProgram: (program: Program, catalog?: Programs) => void;
  notice: (message: string) => void;
  refreshSession: () => Promise<void>;
}
export const Atlas = createContext<AtlasContext | null>(null);
export function useAtlas() {
  const context = useContext(Atlas);
  if (!context) throw new Error("Atlas context is missing");
  return context;
}
