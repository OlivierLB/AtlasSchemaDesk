"""SQL console helpers for DB Atlas: split a script into statements, classify them,
and list the reasons that require an explicit confirmation before running.

Pure functions (no database access), unit-tested by tests/test_sql_console.py.
"""

import re

# Statement kinds: read (no transaction needed), write (runs in the open transaction),
# ddl (structure; implicit commit on MySQL), session (SET/USE), control (refused: use the buttons).
READ_WORDS = {"SELECT", "WITH", "SHOW", "EXPLAIN", "DESCRIBE", "DESC", "VALUES", "TABLE", "HELP"}
WRITE_WORDS = {"INSERT", "UPDATE", "DELETE", "REPLACE", "MERGE", "CALL", "DO", "LOAD", "COPY", "HANDLER", "IMPORT"}
DDL_WORDS = {"CREATE", "ALTER", "DROP", "TRUNCATE", "RENAME", "GRANT", "REVOKE", "COMMENT", "ANALYZE", "OPTIMIZE",
             "REPAIR", "VACUUM", "REINDEX", "CLUSTER", "REFRESH", "SECURITY", "CHECKPOINT"}
SESSION_WORDS = {"SET", "USE", "RESET", "DISCARD", "PREPARE", "EXECUTE", "DEALLOCATE"}
CONTROL_WORDS = {"BEGIN", "START", "COMMIT", "ROLLBACK", "SAVEPOINT", "RELEASE", "END", "ABORT", "LOCK", "UNLOCK", "XA"}
ROUTINE_DEFINITION = re.compile(r"^\s*CREATE\s+(?:OR\s+REPLACE\s+)?(?:DEFINER\s*=\s*\S+\s+)?(?:PROCEDURE|FUNCTION|TRIGGER|EVENT)\b", re.I)
WRITE_IN_WITH = re.compile(r"\b(INSERT|UPDATE|DELETE|MERGE)\b", re.I)


def _mask_literals(sql: str, engine: str) -> str:
    """Same length as `sql`, with comments and string/identifier literals replaced by spaces."""
    out = list(sql)
    i, length = 0, len(sql)
    while i < length:
        char = sql[i]
        two = sql[i:i + 2]
        end = None
        if two == "--" or (char == "#" and engine == "mysql"):
            end = sql.find("\n", i)
            end = length if end < 0 else end
        elif two == "/*":
            end = sql.find("*/", i + 2)
            end = length if end < 0 else end + 2
        elif char in ("'", '"', "`"):
            j = i + 1
            while j < length:
                if sql[j] == "\\" and engine == "mysql" and char != "`":
                    j += 2
                    continue
                if sql[j] == char:
                    if j + 1 < length and sql[j + 1] == char:  # doubled quote
                        j += 2
                        continue
                    break
                j += 1
            end = min(j + 1, length)
        elif char == "$" and engine != "mysql":
            match = re.match(r"\$([A-Za-z_]\w*)?\$", sql[i:])
            if match:
                tag = match.group(0)
                close = sql.find(tag, i + len(tag))
                end = length if close < 0 else close + len(tag)
        if end is not None:
            for k in range(i, end):
                if out[k] != "\n":
                    out[k] = " "
            i = end
            continue
        i += 1
    return "".join(out)


def split_statements(sql: str, engine: str) -> list:
    """Split on top-level semicolons. A MySQL routine/trigger/event definition (BEGIN … END
    with inner semicolons) is kept whole, since the console has no DELIMITER support."""
    if ROUTINE_DEFINITION.match(_mask_literals(sql, engine)):
        statement = sql.strip().rstrip(";").strip()
        return [statement] if statement else []
    masked = _mask_literals(sql, engine)
    statements, start = [], 0
    for index, char in enumerate(masked):
        if char == ";":
            statements.append(sql[start:index])
            start = index + 1
    statements.append(sql[start:])
    return [s.strip() for s in statements if _mask_literals(s, engine).strip()]


def _first_words(statement: str, engine: str) -> list:
    masked = _mask_literals(statement, engine)
    return re.findall(r"[A-Za-z_]+", masked.lstrip(" \n\t("))[:3]


def classify(statement: str, engine: str) -> str:
    words = [word.upper() for word in _first_words(statement, engine)]
    if not words:
        return "read"
    first = words[0]
    masked = _mask_literals(statement, engine)
    if first == "EXPLAIN":
        # EXPLAIN ANALYZE really executes the statement.
        return "write" if re.search(r"\bANALYZE\b", masked, re.I) and WRITE_IN_WITH.search(masked) else "read"
    if first == "WITH":
        return "write" if WRITE_IN_WITH.search(masked) else "read"
    if first == "SELECT" and re.search(r"\bINTO\s+(OUTFILE|DUMPFILE)\b", masked, re.I):
        return "write"
    if first == "SELECT" and engine != "mysql" and re.search(r"\bINTO\b", masked, re.I):
        return "ddl"
    if first in READ_WORDS:
        return "read"
    if first in CONTROL_WORDS:
        return "control"
    if first in DDL_WORDS:
        return "ddl"
    if first in SESSION_WORDS:
        return "session"
    return "write"  # unknown: safest is to run it inside the transaction


def confirmation_reasons(statement: str, engine: str, kind: str, transaction_open: bool) -> list:
    """Why this statement needs an explicit confirmation (empty list: run directly)."""
    masked = _mask_literals(statement, engine)
    words = [word.upper() for word in _first_words(statement, engine)]
    first = words[0] if words else ""
    reasons = []
    if first in ("UPDATE", "DELETE") and not re.search(r"\bWHERE\b", masked, re.I):
        reasons.append(f"{first} sans WHERE : toutes les lignes de la table sont concernées")
    if first == "DROP":
        reasons.append("DROP : supprime définitivement l'objet")
    if first == "TRUNCATE":
        reasons.append("TRUNCATE : vide la table" + (" (validation immédiate, non annulable sous MySQL)" if engine == "mysql" else ""))
    if kind == "ddl" and engine == "mysql":
        reasons.append("instruction de structure sous MySQL : validation implicite, non annulable"
                       + (" ; la transaction en cours sera validée avec elle" if transaction_open else ""))
    return reasons
