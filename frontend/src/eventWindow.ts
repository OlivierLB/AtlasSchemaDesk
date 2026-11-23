import type { Program } from "./model";

const pad = (value: number) => String(value).padStart(2, "0");
export const localDate = (value: Date) =>
  `${value.getFullYear()}-${pad(value.getMonth() + 1)}-${pad(value.getDate())}`;
export const localTime = (value: Date) =>
  `${pad(value.getHours())}:${pad(value.getMinutes())}`;

export function makeEventWindow(
  date: string,
  start: string,
  end: string,
  wholeDay: boolean,
) {
  if (
    !/^\d{4}-\d{2}-\d{2}$/.test(date) ||
    (!wholeDay && (!/^\d{2}:\d{2}$/.test(start) || !/^\d{2}:\d{2}$/.test(end)))
  )
    throw new Error("Renseigne un jour et deux heures valides.");
  const [year, month, day] = date.split("-").map(Number);
  if (year < 1970 || year > 9999)
    throw new Error("Choisis une année entre 1970 et 9999.");
  const parse = (clock: string, nextDay = false) => {
    const [hour, minute] = clock.split(":").map(Number);
    if (hour > 23 || minute > 59)
      throw new Error("L’heure doit être comprise entre 00:00 et 23:59.");
    const value = new Date(
      year,
      month - 1,
      day + Number(nextDay),
      hour,
      minute,
      0,
      0,
    );
    const expected = new Date(year, month - 1, day + Number(nextDay), 12, 0);
    if (
      value.getHours() !== hour ||
      value.getMinutes() !== minute ||
      localDate(value) !== localDate(expected) ||
      (!nextDay && localDate(value) !== date)
    )
      throw new Error(
        "Ce jour ou cette heure n’existe pas dans le fuseau du navigateur, notamment lors d’un changement d’heure.",
      );
    return value;
  };
  const from = parse(wholeDay ? "00:00" : start);
  const to = parse(wholeDay ? "00:00" : end, wholeDay || end <= start);
  if (to.getFullYear() > 9999)
    throw new Error("La fin du créneau dépasse l’année 9999.");
  return { from: from.toISOString(), to: to.toISOString() };
}

export function sharedEventTables(
  entries: { engine: string; program: Program }[],
) {
  const groups = new Map<
    string,
    {
      engine: string;
      table: string;
      programs: Set<string>;
      readers: Set<string>;
      writers: Set<string>;
      probable: Set<string>;
    }
  >();
  for (const { engine, program } of entries) {
    for (const [kind, names] of Object.entries({
      reads: program.impact.reads || [],
      writes: [
        ...(program.impact.writes || []),
        ...(program.impact.indirectWrites || []),
      ],
      probable: [
        ...(program.impact.probableReads || []),
        ...(program.impact.probableWrites || []),
      ],
    })) {
      for (const table of names) {
        const key = `${engine}\0${table}`;
        const group = groups.get(key) || {
          engine,
          table,
          programs: new Set<string>(),
          readers: new Set<string>(),
          writers: new Set<string>(),
          probable: new Set<string>(),
        };
        group.programs.add(program.id);
        (kind === "reads"
          ? group.readers
          : kind === "writes"
            ? group.writers
            : group.probable
        ).add(program.id);
        groups.set(key, group);
      }
    }
  }
  return [...groups.values()]
    .filter((group) => group.programs.size > 1)
    .sort(
      (a, b) =>
        b.programs.size - a.programs.size || a.table.localeCompare(b.table),
    );
}
