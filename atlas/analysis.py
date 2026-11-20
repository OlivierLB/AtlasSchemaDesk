"""Stored-program normalization and conservative SQL impact analysis."""

import re

def pg_programs(engine: str, catalog: dict) -> list:
    programs = []
    for row in catalog["functions"]:
        programs.append({
            "kind": "trigger_function" if row["is_trigger_function"] else "function",
            "schema": row["schema_name"], "name": row["name"],
            "signature": f"{row['name']}({row['arguments']})", "returns": row["returns"],
            "language": row["language"], "body": row["body"] or "",
        })
    for row in catalog["triggers"]:
        definition = row["definition"]
        timing = re.search(r"\b(BEFORE|AFTER|INSTEAD OF)\b", definition)
        events = re.findall(r"\b(INSERT|UPDATE|DELETE|TRUNCATE)\b", definition.split(" ON ", 1)[0])
        programs.append({
            "kind": "trigger", "schema": row["schema_name"], "name": row["name"],
            "table": {"schema": row["schema_name"], "name": row["table_name"]},
            "timing": timing.group(1) if timing else None, "events": events,
            "function": {"schema": row["function_schema"], "name": row["function_name"]},
            "body": definition,
        })
    for row in catalog["views"]:
        programs.append({"kind": "materialized_view" if row["materialized"] else "view",
                         "schema": row["schema_name"], "name": row["name"], "body": row["body"] or ""})
    return programs

def mysql_programs(catalog: dict) -> list:
    parameters = {}
    for row in catalog["parameters"]:
        parameters.setdefault((row["schema_name"], row["name"], row["type"]), []).append(
            " ".join(part for part in (row["mode"], row["parameter"], row["data_type"]) if part))
    programs = []
    for row in catalog["routines"]:
        params = parameters.get((row["schema_name"], row["name"], row["type"]), [])
        programs.append({
            "kind": row["type"].lower(), "schema": row["schema_name"], "name": row["name"],
            "signature": f"{row['name']}({', '.join(params)})", "returns": row["returns"],
            "lastAltered": row["last_altered"], "comment": row["comment"] or None,
            "body": row["body"] or "",
        })
    for row in catalog["triggers"]:
        programs.append({
            "kind": "trigger", "schema": row["schema_name"], "name": row["name"],
            "table": {"schema": row["table_schema"], "name": row["table_name"]},
            "timing": row["timing"], "events": [row["event"]], "body": row["body"] or "",
        })
    for row in catalog["views"]:
        programs.append({"kind": "view", "schema": row["schema_name"], "name": row["name"], "body": row["body"] or ""})
    for row in catalog["events"]:
        if row["event_type"] == "RECURRING":
            schedule = f"toutes les {row['interval_value']} {row['interval_field']}"
        else:
            schedule = f"une fois le {row['execute_at']}"
        programs.append({
            "kind": "event", "schema": row["schema_name"], "name": row["name"],
            "status": row["status"], "schedule": schedule, "starts": row.get("starts"),
            "lastExecuted": row["last_executed"], "body": row["body"] or "",
        })
    return programs

def table_stats(catalog: dict) -> dict:
    """Catalog row/size estimates keyed by "schema.name" (no table data read)."""
    stats = {}
    for row in catalog.get("table_stats", []):
        rows = int(row["estimated_rows"]) if row["estimated_rows"] is not None else None
        if rows is not None and rows < 0:
            rows = None
        if rows == 0 and "heap_bytes" in row and int(row["heap_bytes"] or 0) > 0:
            # PostgreSQL: reltuples stays 0 when data is loaded after the indexes and the table
            # was never analysed (e.g. a fresh restore). Data exists on disk: unknown, not empty.
            rows = None
        stats[f"{row['schema_name']}.{row['name']}"] = {
            "rows": rows,
            "bytes": int(row["total_bytes"]) if row["total_bytes"] is not None else None,
        }
    return stats

IDENT = r"(?:[A-Za-z_][\w$]*\.)?[A-Za-z_][\w$]*"

WRITE_PATTERNS = [
    re.compile(rf"\bINSERT\s+(?:LOW_PRIORITY\s+|DELAYED\s+|HIGH_PRIORITY\s+)?(?:IGNORE\s+)?(?:INTO\s+)?({IDENT})", re.I),
    re.compile(rf"\bREPLACE\s+(?:LOW_PRIORITY\s+|DELAYED\s+)?(?:INTO\s+)?({IDENT})", re.I),
    re.compile(rf"(?<!KEY\s)(?<!DO\s)\bUPDATE\s+(?:LOW_PRIORITY\s+)?(?:IGNORE\s+)?(?:ONLY\s+)?({IDENT})", re.I),
    re.compile(rf"\bDELETE\s+(?:LOW_PRIORITY\s+|QUICK\s+|IGNORE\s+)*(?:{IDENT}(?:\s*,\s*{IDENT})*\s+)?FROM\s+(?:ONLY\s+)?({IDENT})", re.I),
    re.compile(rf"\bTRUNCATE\s+(?:TABLE\s+)?(?:ONLY\s+)?({IDENT})", re.I),
]

READ_PATTERN = re.compile(rf"\b(?:FROM|JOIN)\s+({IDENT}(?:\s+(?:AS\s+)?[A-Za-z_]\w*)?(?:\s*,\s*{IDENT}(?:\s+(?:AS\s+)?[A-Za-z_]\w*)?)*)", re.I)

CALL_PATTERN = re.compile(rf"\bCALL\s+({IDENT})", re.I)

FUNCTION_CALL = re.compile(rf"({IDENT})\s*\(")

DYNAMIC_MYSQL = re.compile(r"\bPREPARE\s+\w+\s+FROM\b|\bEXECUTE\s+IMMEDIATE\b", re.I)

DYNAMIC_PG = re.compile(r"\bEXECUTE\s+(?!PROCEDURE\b|FUNCTION\b)(?:format\s*\(|'|\w)", re.I)

def strip_sql(body: str, engine: str) -> str:
    """Remove comments and string literals; unquote identifiers."""
    text = re.sub(r"/\*.*?\*/", " ", body, flags=re.S)
    text = re.sub(r"--[^\n]*", " ", text)
    if engine == "mysql":
        text = re.sub(r"(?m)#[^\n]*", " ", text)
        text = re.sub(r"'(?:[^'\\]|\\.|'')*'", "''", text)
        text = re.sub(r'"(?:[^"\\]|\\.|"")*"', "''", text)
        text = text.replace("`", "")
    else:
        text = re.sub(r"'(?:[^']|'')*'", "''", text)
        text = re.sub(r'"([^"]+)"', r"\1", text)
    return text

def string_literals(body: str, engine: str) -> str:
    """Contents of the string literals (where dynamic SQL is usually assembled)."""
    without_comments = re.sub(r"--[^\n]*", " ", re.sub(r"/\*.*?\*/", " ", body, flags=re.S))
    pattern = r"'((?:[^'\\]|\\.|'')*)'|\"((?:[^\"\\]|\\.|\"\")*)\"" if engine == "mysql" else r"'((?:[^']|'')*)'"
    parts = []
    for match in re.finditer(pattern, without_comments):
        parts.extend(group for group in match.groups() if group)
    return " ".join(parts).replace("`", "")

class Resolver:
    """Maps a SQL name to a known relation key, using the program schema first."""

    def __init__(self, relations: set, default_schemas: tuple) -> None:
        self.relations = relations
        self.by_name = {}
        for schema, name in relations:
            self.by_name.setdefault(name.lower(), []).append((schema, name))
        self.lower = {(s.lower(), n.lower()): (s, n) for s, n in relations}
        self.default_schemas = default_schemas

    def resolve(self, ident: str, schema: str):
        parts = ident.split(".")
        if len(parts) == 2:
            return self.lower.get((parts[0].lower(), parts[1].lower()))
        for candidate in (schema, *self.default_schemas):
            found = self.lower.get((candidate.lower(), parts[0].lower()))
            if found:
                return found
        matches = self.by_name.get(parts[0].lower(), [])
        return matches[0] if len(matches) == 1 else None

def analyse(engine: str, programs: list, tables: list) -> dict:
    relations = {(t["schema"], t["name"]) for t in tables}
    relations |= {(p["schema"], p["name"]) for p in programs if p["kind"] in ("view", "materialized_view")}
    resolver = Resolver(relations, ("public",) if engine != "mysql" else ())
    routine_kinds = ("function", "procedure", "trigger_function")
    routines = {}
    for program in programs:
        if program["kind"] in routine_kinds:
            routines.setdefault(program["name"].lower(), []).append(program)

    def routine_ref(ident: str, schema: str, kinds: tuple):
        parts = ident.split(".")
        name = parts[-1].lower()
        candidates = [p for p in routines.get(name, []) if p["kind"] in kinds]
        if len(parts) == 2:
            candidates = [p for p in candidates if p["schema"].lower() == parts[0].lower()]
        elif len(candidates) > 1:
            candidates = [p for p in candidates if p["schema"] == schema] or candidates[:1]
        return candidates[0]["id"] if candidates else None

    for program in programs:
        program["id"] = f"{engine}:{program['kind']}:{program['schema']}.{program['name']}"
    def relations_in(text: str, schema: str) -> tuple:
        writes, reads = set(), set()
        for pattern in WRITE_PATTERNS:
            for match in pattern.finditer(text):
                key = resolver.resolve(match.group(1), schema)
                if key:
                    writes.add(key)
        for match in READ_PATTERN.finditer(text):
            for item in match.group(1).split(","):
                ident = item.strip().split()[0] if item.strip() else ""
                key = resolver.resolve(ident, schema) if ident else None
                if key:
                    reads.add(key)
        return writes, reads

    for program in programs:
        text = strip_sql(program["body"], engine)
        if engine != "mysql" and program["kind"] == "trigger":
            text = ""  # a PG trigger only points to its function; impact comes from the function
        calls = set()
        writes, reads = relations_in(text, program["schema"])
        for match in CALL_PATTERN.finditer(text):
            ref = routine_ref(match.group(1), program["schema"], ("procedure",))
            if ref:
                calls.add(ref)
        for match in FUNCTION_CALL.finditer(text):
            ref = routine_ref(match.group(1), program["schema"], ("function",))
            if ref and ref != program["id"]:
                calls.add(ref)
        if program["kind"] == "trigger" and engine != "mysql":
            ref = routine_ref(f"{program['function']['schema']}.{program['function']['name']}", program["schema"], ("trigger_function", "function"))
            if ref:
                calls.add(ref)
        if program.get("table"):
            # The table a trigger fires on is not "read" by it in the impact sense.
            reads.discard((program["table"]["schema"], program["table"]["name"]))
        dynamic_pattern = DYNAMIC_MYSQL if engine == "mysql" else DYNAMIC_PG
        is_dynamic = bool(dynamic_pattern.search(text)) if program["kind"] not in ("view", "materialized_view") else False
        probable_writes, probable_reads = set(), set()
        if is_dynamic:
            # SQL assembled in strings then executed: names found there are probable, not certain.
            probable_writes, probable_reads = relations_in(string_literals(program["body"], engine), program["schema"])
            probable_writes -= writes
            probable_reads -= reads | writes | probable_writes
        as_names = lambda keys: sorted(f"{s}.{n}" for s, n in keys)
        program["impact"] = {
            "writes": as_names(writes),
            "reads": as_names(reads - writes),
            "calls": sorted(calls),
            "dynamicSql": is_dynamic,
            "probableWrites": as_names(probable_writes),
            "probableReads": as_names(probable_reads),
        }
    return build_index(programs)

def build_index(programs: list) -> dict:
    """Reverse index per relation, including writes reached through the call graph."""
    by_id = {p["id"]: p for p in programs}
    callers = {}
    for program in programs:
        for callee in program["impact"]["calls"]:
            callers.setdefault(callee, set()).add(program["id"])

    def transitive_writes(program_id: str, seen: set) -> set:
        if program_id in seen:
            return set()
        seen.add(program_id)
        program = by_id[program_id]
        result = set(program["impact"]["writes"])
        for callee in program["impact"]["calls"]:
            if callee in by_id:
                result |= transitive_writes(callee, seen)
        return result

    index = {}
    entry = lambda key: index.setdefault(key, {"writes": [], "reads": [], "triggers": [], "indirectWrites": [], "probableWrites": [], "probableReads": []})
    for program in programs:
        for kind in ("writes", "reads", "probableWrites", "probableReads"):
            for key in program["impact"][kind]:
                entry(key)[kind].append(program["id"])
        if program.get("table"):
            entry(f"{program['table']['schema']}.{program['table']['name']}")["triggers"].append(program["id"])
        indirect = transitive_writes(program["id"], set()) - set(program["impact"]["writes"])
        program["impact"]["indirectWrites"] = sorted(indirect)
        for key in indirect:
            entry(key)["indirectWrites"].append(program["id"])
    for program in programs:
        program["impact"]["calledBy"] = sorted(callers.get(program["id"], set()))
    return index
