import { describe, expect, it } from "vitest";
import { makeEventWindow, sharedEventTables } from "./eventSchedule";
import type { Program } from "./model";

describe("event investigation windows", () => {
  it("treats a finish before the start as the following day", () => {
    const range = makeEventWindow("2026-11-23", "23:30", "00:30", false);
    expect(new Date(range.to).getTime() - new Date(range.from).getTime()).toBe(
      60 * 60_000,
    );
  });
  it("uses complete local days and ignores disabled clock fields", () => {
    const range = makeEventWindow("2026-11-23", "", "", true);
    expect(new Date(range.from).getHours()).toBe(0);
    expect(new Date(range.to).getDate()).toBe(24);
  });
  it("rejects normalized invalid dates and clocks", () => {
    expect(() =>
      makeEventWindow("2026-02-30", "10:00", "11:00", false),
    ).toThrow();
    expect(() =>
      makeEventWindow("2026-11-23", "25:00", "11:00", false),
    ).toThrow();
  });
});
describe("shared table investigation", () => {
  const event = (id: string, impact: Program["impact"]): Program => ({
    id,
    kind: "event",
    schema: "public",
    name: id,
    body: "",
    impact,
  });
  it("deduplicates direct and indirect writes and never mixes databases", () => {
    const groups = sharedEventTables([
      {
        engine: "one",
        program: event("a", {
          writes: ["public.jobs"],
          indirectWrites: ["public.jobs"],
        }),
      },
      { engine: "one", program: event("b", { reads: ["public.jobs"] }) },
      { engine: "two", program: event("c", { writes: ["public.jobs"] }) },
    ]);
    expect(groups).toHaveLength(1);
    expect(groups[0].engine).toBe("one");
    expect(groups[0].programs.size).toBe(2);
    expect(groups[0].writers.size).toBe(1);
    expect(groups[0].readers.size).toBe(1);
  });
  it("keeps probable dependencies explicitly classified", () => {
    const groups = sharedEventTables([
      { engine: "one", program: event("a", { reads: ["public.jobs"] }) },
      {
        engine: "one",
        program: event("b", { probableWrites: ["public.jobs"] }),
      },
    ]);
    expect(groups[0].probable.size).toBe(1);
    expect(groups[0].writers.size).toBe(0);
  });
});
