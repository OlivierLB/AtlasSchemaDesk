"""Persistent direct driver connections, serialized per configured source."""
import importlib
import re
import threading
import time
from contextlib import contextmanager
from .sql_console import classify, split_statements, _mask_literals


def cell(value):
    if value is None:
        return None
    if isinstance(value, (bytes, bytearray, memoryview)):
        return {"binary": True, "length": len(value)}
    text = str(value)
    return {"truncated": True, "length": len(text), "value": text[:2000]} if len(text) > 2000 else text


def parameters(sql, driver):
    """Keep the existing console's ? placeholders; protect literal percent signs."""
    masked = _mask_literals(sql, driver)
    return "".join("%s" if c == "?" and masked[i] == "?" else "%%" if c == "%" else c
                   for i,c in enumerate(sql))


class Database:
    def __init__(self, config, options):
        self.config, self.options = config, options
        self.connection = None
        self.lock = threading.RLock()
        self.tx = False

    @property
    def pg(self):
        return self.options.driver == "postgresql"

    def connect(self):
        if self.connection is not None:
            return self.connection
        name = "psycopg" if self.pg else "pymysql"
        try:
            module = importlib.import_module(name)
        except ImportError as error:
            raise RuntimeError(f"Driver {name} is not installed; install atlas-schema-desk[drivers]") from error
        o = self.options
        args = dict(host=o.host, port=o.port, user=o.user, password=o.password,
                    connect_timeout=5, autocommit=True)
        if self.pg:
            args["dbname"] = o.database
        else:
            args.update(database=o.database, charset="utf8mb4", read_timeout=125, write_timeout=125)
        conn = module.connect(**args)
        try:
            with conn.cursor() as cursor:
                if not o.allow_writes:
                    cursor.execute("SET SESSION CHARACTERISTICS AS TRANSACTION READ ONLY" if self.pg else "SET SESSION TRANSACTION READ ONLY")
                cursor.execute("SET statement_timeout = 15000" if self.pg else "SET SESSION max_execution_time = 15000")
                if self.pg:
                    cursor.execute("SELECT set_config('search_path', %s, false)", (self.quote(o.default_schema),))
        except BaseException:
            conn.close()
            raise
        self.connection = conn
        return conn

    def quote(self, name):
        mark = '"' if self.pg else '`'
        return mark + name.replace(mark, mark*2) + mark

    def execute(self, sql, params=None):
        cursor = self.connect().cursor()
        try:
            if params:
                cursor.execute(parameters(sql, self.options.driver), params)
            else:
                cursor.execute(sql)
            return cursor
        except BaseException:
            cursor.close()
            raise

    def query(self, sql, params=None):
        with self.execute(sql, params) as cursor:
            columns = [d[0] for d in cursor.description] if cursor.description else []
            return [dict(zip(columns,row)) for row in cursor.fetchall()] if columns else []

    def command(self, sql):
        with self.execute(sql):
            pass

    def enforce_readonly(self):
        # Read expressions can call functions that change session defaults. Restore
        # the default before every operation, including each statement of a script.
        if not self.options.allow_writes:
            self.command('SET SESSION CHARACTERISTICS AS TRANSACTION READ ONLY' if self.pg else 'SET SESSION TRANSACTION READ ONLY')

    def in_transaction(self):
        if not self.connection:
            return False
        if self.pg:
            return self.connection.info.transaction_status.name != "IDLE"
        # An error packet does not refresh PyMySQL's cached server_status. A
        # failed DDL can still have committed the previous transaction.
        self.command('DO 0')
        return bool(self.connection.server_status & 1)

    @contextmanager
    def guarded(self):
        save = self.pg and self.tx
        if save:
            self.command("SAVEPOINT atlas_read")
        try:
            yield
        except BaseException:
            if save:
                self.command("ROLLBACK TO SAVEPOINT atlas_read")
            raise
        finally:
            if save:
                self.command("RELEASE SAVEPOINT atlas_read")

    def rows(self, body):
        self.enforce_readonly()
        q = self.quote
        sql = f"SELECT * FROM {q(body['schema'])}.{q(body['table'])}"
        clauses, params = [], []
        for f in body.get("filters", []):
            col, mode = q(f['column']), f['mode']
            if mode in ("null", "notnull"):
                clauses.append(col + (" IS NULL" if mode == "null" else " IS NOT NULL"))
                continue
            if mode == "like":
                # Use an explicit portable escape character independent of server SQL mode.
                clauses.append(f"CAST({col} AS TEXT) ILIKE ? ESCAPE '!'" if self.pg else f"{col} LIKE ? ESCAPE '!'" )
                params.append('%'+str(f['value']).replace('!','!!').replace('%','!%').replace('_','!_')+'%')
            else:
                op = f.get('op', '=')
                if op not in ('=', '<','>','<=','>=','!='):
                    raise ValueError('Invalid comparison operator')
                clauses.append(f"{col} {op} ?")
                params.append(f['value'])
        if clauses:
            sql += ' WHERE ' + ' AND '.join(clauses)
        if body.get('sort'):
            sort = body['sort']
            sql += ' ORDER BY '+q(sort['column'])+(' DESC' if sort.get('dir')=='desc' else ' ASC')
        limit = max(1,min(500,int(body.get('limit',100))))
        sql += f" LIMIT {limit+1} OFFSET {max(0,int(body.get('offset',0)))}"
        started = time.monotonic()
        with self.guarded(), self.execute(sql,params) as cursor:
            names = [d[0] for d in cursor.description]
            records = cursor.fetchmany(limit+1)
        return dict(rows=[{n:cell(v) for n,v in zip(names,row)} for row in records[:limit]],
                    hasMore=len(records)>limit, durationMs=round((time.monotonic()-started)*1000),sql=sql)

    def refs(self, body):
        self.enforce_readonly()
        counts = []
        cap = max(1,min(1000,int(body.get('cap',1000))))
        for ref in body['refs']:
            try:
                with self.guarded():
                    n = self.query(f"SELECT COUNT(*) AS n FROM (SELECT 1 FROM {self.quote(ref['schema'])}.{self.quote(ref['table'])} WHERE {self.quote(ref['column'])} = ? LIMIT {cap+1}) counted", [ref['value']])[0]['n']
                counts.append(dict(count=min(n,cap),capped=n>cap))
            except Exception as error:
                counts.append(dict(error=self.config.redact(error)))
        return dict(counts=counts)

    def validate_sql(self, statements):
        for item in statements:
            sql = item['sql']
            kind = classify(sql,self.options.driver)
            if kind == 'control':
                raise ValueError('Use the commit and rollback buttons for transactions')
            # Session commands can disable read-only mode, invoke prepared writes or
            # change connection state. Only schema selection is allowed in read-only mode.
            if not self.options.allow_writes:
                masked = _mask_literals(sql,self.options.driver)
                if kind not in ('read','session'):
                    raise ValueError('This connection is read-only')
                if kind == 'session' and not re.fullmatch(r'\s*USE\s+[`"\w]+\s*;?\s*',sql,re.I):
                    raise ValueError('Session changes are disabled on a read-only connection')
                # MySQL executable comments can contain hidden writes or SET commands.
                if re.search(r'/\*\s*!',sql) or re.search(r'\bINTO\b',masked,re.I):
                    raise ValueError('This statement is not allowed on a read-only connection')

    def sql(self, body):
        statements = body['statements']
        self.validate_sql(statements)
        results, failure, implicit = [], None, False
        self.command('SET statement_timeout = 120000' if self.pg else 'SET SESSION max_execution_time = 120000')
        try:
            for index,item in enumerate(statements):
                self.enforce_readonly()
                kind = classify(item['sql'],self.options.driver)
                mysql_ddl = not self.pg and kind=='ddl'
                was_open = self.tx
                if not self.tx and not mysql_ddl and kind in ('write','ddl'):
                    self.command('BEGIN' if self.pg else 'START TRANSACTION')
                    self.tx = True
                save = self.pg and self.tx
                if save:
                    self.command('SAVEPOINT atlas_statement')
                started = time.monotonic()
                try:
                    with self.execute(item['sql'],body.get('params') if len(statements)==1 else None) as cursor:
                        result = dict(index=index,kind=kind)
                        if cursor.description:
                            rows = cursor.fetchmany(501)
                            result.update(columns=[d[0] for d in cursor.description], rows=[[cell(v) for v in row] for row in rows[:500]], hasMore=len(rows)>500)
                        else:
                            result['affected'] = cursor.rowcount
                        while cursor.nextset():
                            if cursor.description:
                                cursor.fetchall()
                    result['durationMs'] = round((time.monotonic()-started)*1000)
                    results.append(result)
                    if save:
                        self.command('RELEASE SAVEPOINT atlas_statement')
                except Exception as error:
                    if save:
                        self.command('ROLLBACK TO SAVEPOINT atlas_statement')
                        self.command('RELEASE SAVEPOINT atlas_statement')
                    failure = dict(index=index,message=self.config.redact(error))
                finally:
                    if not self.pg:
                        actual = self.in_transaction()
                        implicit = implicit or (was_open and not actual)
                        self.tx = actual
                if failure:
                    break
        finally:
            self.command('SET statement_timeout = 15000' if self.pg else 'SET SESSION max_execution_time = 15000')
        return dict(results=results,error=failure,txOpen=self.tx,implicitCommit=implicit)

    def end(self, action):
        done = self.tx
        if done:
            self.command('COMMIT' if action=='commit' else 'ROLLBACK')
            self.tx = False
        return dict(txOpen=False,done=done)

    def close(self):
        if self.connection:
            self.connection.close()
            self.connection = None
        self.tx = False


class DbWorker:
    def __init__(self, config):
        self.config = config
        self.databases = {o.id:Database(config,o) for o in config.connections}
        self.transactions_lost_at = None

    @property
    def transactions(self):
        return {name: db.tx for name,db in self.databases.items()}

    def validate_sql(self, engine, statements):
        self.config.connection(engine)
        self.databases[engine].validate_sql(statements)

    def request(self, payload, timeout=30):
        db = self.databases[self.config.connection(payload['engine']).id]
        with db.lock:
            try:
                op = payload['op']
                if op in ('commit','rollback'):
                    result = db.end(op)
                elif op in ('rows','refs','sql'):
                    result = getattr(db,op)(payload)
                else:
                    raise ValueError('Unknown database operation')
                return dict(ok=True,**result)
            except Exception as error:
                # Disconnected sessions cannot retain a meaningful transaction state.
                if db.connection and (getattr(db.connection,'closed',False) or getattr(db.connection,'open',True) is False):
                    if db.tx:
                        from datetime import datetime,timezone
                        self.transactions_lost_at = datetime.now(timezone.utc).isoformat()
                    db.close()
                return dict(ok=False,error=self.config.redact(error),txOpen=db.tx)

    def close(self):
        for db in self.databases.values():
            with db.lock:
                db.close()
