import { describe, expect, it } from "vitest";
import { display, href, parseHash } from "./model";

describe("existing navigation contract", () => {
  it("roundtrips qualified names and data filters without losing special characters", () => {
    const route = {
      view: "table",
      engine: "library",
      schema: "public schema",
      table: "a/b",
      tab: "donnees",
      params: { name: "=a&b", empty: "" },
    } as const;
    expect(parseHash(href(route))).toEqual({
      ...route,
      params: { name: "=a&b" },
    });
  });
  it("keeps old table, SQL and event URLs usable", () => {
    expect(parseHash("#/library/public/actor/donnees").table).toBe("actor");
    expect(parseHash("#/sql/store")).toEqual({ view: "sql", engine: "store" });
    expect(parseHash("#/events")).toEqual({ view: "events" });
    expect(parseHash("#/bad%XX/public/table")).toEqual({ view: "home" });
  });
  it("opens settings on the database that owns a backup", () => {
    expect(parseHash("#/connexions?connection=library")).toEqual({
      view: "connections",
      params: { connection: "library" },
    });
  });
});
describe("database values", () => {
  it("distinguishes null, binary, truncated and scalar values", () => {
    expect(display(null)).toBe("∅");
    expect(display(0)).toBe("0");
    expect(display({ binary: true, length: 12 })).toContain("12 octets");
    expect(display({ truncated: true, value: "long text", length: 4000 })).toBe(
      "long text…",
    );
  });
});
