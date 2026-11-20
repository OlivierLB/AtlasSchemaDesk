"""Catalog-only exports and optional source repository matching."""
import argparse
import ast
import json
import re
from datetime import datetime, timezone
from pathlib import Path
from .config import load_config
from .db import Database
from .queries import PG_PROGRAMS
from .analysis import pg_programs, mysql_programs, analyse, table_stats
from .snapshot import write_atomic, source_hash


EXCLUDED = "('mysql','sys','performance_schema','information_schema')"
PG_COLUMNS = """SELECT n.nspname AS schema_name,c.relname AS table_name,a.attname AS column_name,
 format_type(a.atttypid,a.atttypmod) AS data_type,a.attnotnull AS not_null,
 pg_get_expr(d.adbin,d.adrelid) AS default_value,
 EXISTS(SELECT 1 FROM pg_index i WHERE i.indrelid=c.oid AND i.indisprimary AND a.attnum=ANY(i.indkey)) AS is_primary
 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
 JOIN pg_attribute a ON a.attrelid=c.oid LEFT JOIN pg_attrdef d ON d.adrelid=c.oid AND d.adnum=a.attnum
 WHERE c.relkind IN ('r','p') AND a.attnum>0 AND NOT a.attisdropped
 AND n.nspname NOT IN ('pg_catalog','information_schema') AND n.nspname NOT LIKE 'pg_toast%'
 ORDER BY n.nspname,c.relname,a.attnum"""
PG_FKS = """SELECT ns.nspname AS schema_name,src.relname AS table_name,sa.attname AS column_name,
 nt.nspname AS ref_schema,dst.relname AS ref_table,da.attname AS ref_column
 FROM pg_constraint con JOIN pg_class src ON src.oid=con.conrelid JOIN pg_namespace ns ON ns.oid=src.relnamespace
 JOIN pg_class dst ON dst.oid=con.confrelid JOIN pg_namespace nt ON nt.oid=dst.relnamespace
 JOIN LATERAL unnest(con.conkey,con.confkey) WITH ORDINALITY k(src_att,dst_att,ord) ON true
 JOIN pg_attribute sa ON sa.attrelid=src.oid AND sa.attnum=k.src_att
 JOIN pg_attribute da ON da.attrelid=dst.oid AND da.attnum=k.dst_att WHERE con.contype='f'
 ORDER BY ns.nspname,src.relname,con.conname,k.ord"""
PG_UNIQUE = """SELECT ns.nspname AS schema_name,tbl.relname AS table_name,con.conname AS constraint_name,
 att.attname AS column_name FROM pg_constraint con JOIN pg_class tbl ON tbl.oid=con.conrelid
 JOIN pg_namespace ns ON ns.oid=tbl.relnamespace
 JOIN LATERAL unnest(con.conkey) WITH ORDINALITY k(attnum,ord) ON true
 JOIN pg_attribute att ON att.attrelid=tbl.oid AND att.attnum=k.attnum WHERE con.contype IN ('p','u')
 ORDER BY ns.nspname,tbl.relname,con.conname,k.ord"""
MYSQL_COLUMNS = f"""SELECT TABLE_SCHEMA AS schema_name,TABLE_NAME AS table_name,COLUMN_NAME AS column_name,
 COLUMN_TYPE AS data_type,IS_NULLABLE='NO' AS not_null,COLUMN_DEFAULT AS default_value,COLUMN_KEY='PRI' AS is_primary
 FROM information_schema.COLUMNS WHERE TABLE_SCHEMA NOT IN {EXCLUDED} ORDER BY TABLE_SCHEMA,TABLE_NAME,ORDINAL_POSITION"""
MYSQL_FKS = """SELECT TABLE_SCHEMA AS schema_name,TABLE_NAME AS table_name,COLUMN_NAME AS column_name,
 REFERENCED_TABLE_SCHEMA AS ref_schema,REFERENCED_TABLE_NAME AS ref_table,REFERENCED_COLUMN_NAME AS ref_column
 FROM information_schema.KEY_COLUMN_USAGE WHERE REFERENCED_TABLE_NAME IS NOT NULL
 ORDER BY TABLE_SCHEMA,TABLE_NAME,CONSTRAINT_NAME,ORDINAL_POSITION"""
MYSQL_UNIQUE = """SELECT TABLE_SCHEMA AS schema_name,TABLE_NAME AS table_name,INDEX_NAME AS constraint_name,
 COLUMN_NAME AS column_name FROM information_schema.STATISTICS WHERE NON_UNIQUE=0 AND COLUMN_NAME IS NOT NULL
 ORDER BY TABLE_SCHEMA,TABLE_NAME,INDEX_NAME,SEQ_IN_INDEX"""
MYSQL_PROGRAMS = {
 'routines':f'SELECT ROUTINE_SCHEMA AS schema_name,ROUTINE_NAME AS name,ROUTINE_TYPE AS type,DTD_IDENTIFIER AS returns,ROUTINE_DEFINITION AS body,LAST_ALTERED AS last_altered,ROUTINE_COMMENT AS comment FROM information_schema.ROUTINES WHERE ROUTINE_SCHEMA NOT IN {EXCLUDED}',
 'parameters':f'SELECT SPECIFIC_SCHEMA AS schema_name,SPECIFIC_NAME AS name,ROUTINE_TYPE AS type,ORDINAL_POSITION AS position,PARAMETER_MODE AS mode,PARAMETER_NAME AS parameter,DTD_IDENTIFIER AS data_type FROM information_schema.PARAMETERS WHERE SPECIFIC_SCHEMA NOT IN {EXCLUDED} AND ORDINAL_POSITION>0 ORDER BY 1,2,4',
 'triggers':f'SELECT TRIGGER_SCHEMA AS schema_name,TRIGGER_NAME AS name,EVENT_MANIPULATION AS event,EVENT_OBJECT_SCHEMA AS table_schema,EVENT_OBJECT_TABLE AS table_name,ACTION_TIMING AS timing,ACTION_STATEMENT AS body FROM information_schema.TRIGGERS WHERE TRIGGER_SCHEMA NOT IN {EXCLUDED}',
 'views':f'SELECT TABLE_SCHEMA AS schema_name,TABLE_NAME AS name,VIEW_DEFINITION AS body FROM information_schema.VIEWS WHERE TABLE_SCHEMA NOT IN {EXCLUDED}',
 'events':f'SELECT EVENT_SCHEMA AS schema_name,EVENT_NAME AS name,STATUS AS status,EVENT_TYPE AS event_type,EXECUTE_AT AS execute_at,INTERVAL_VALUE AS interval_value,INTERVAL_FIELD AS interval_field,STARTS AS starts,LAST_EXECUTED AS last_executed,EVENT_DEFINITION AS body FROM information_schema.EVENTS WHERE EVENT_SCHEMA NOT IN {EXCLUDED}',
 'table_stats':f"SELECT TABLE_SCHEMA AS schema_name,TABLE_NAME AS name,TABLE_ROWS AS estimated_rows,DATA_LENGTH+INDEX_LENGTH AS total_bytes FROM information_schema.TABLES WHERE TABLE_TYPE='BASE TABLE' AND TABLE_SCHEMA NOT IN {EXCLUDED}",
}


def schema(db):
    tables, unique = {}, {}
    for r in db.query(PG_COLUMNS if db.pg else MYSQL_COLUMNS):
        key = (r['schema_name'],r['table_name'])
        table = tables.setdefault(key,dict(schema=key[0],name=key[1],columns=[],foreignKeys=[],uniqueConstraints=[]))
        table['columns'].append(dict(name=r['column_name'],type=r['data_type'],nullable=not bool(r['not_null']),default=r['default_value'],primary=bool(r['is_primary'])))
    for r in db.query(PG_UNIQUE if db.pg else MYSQL_UNIQUE):
        unique.setdefault((r['schema_name'],r['table_name']),{}).setdefault(r['constraint_name'],[]).append(r['column_name'])
    for key, groups in unique.items():
        if key in tables:
            tables[key]['uniqueConstraints'] = list(groups.values())
    for r in db.query(PG_FKS if db.pg else MYSQL_FKS):
        key = (r['schema_name'],r['table_name'])
        if key in tables:
            tables[key]['foreignKeys'].append(dict(column=r['column_name'],targetSchema=r['ref_schema'],targetTable=r['ref_table'],targetColumn=r['ref_column']))
    return list(tables.values())


def programs(db):
    if not db.pg:
        return {name:db.query(sql) for name,sql in MYSQL_PROGRAMS.items()}
    version = int(db.query('SHOW server_version_num')[0]['server_version_num'])
    query = PG_PROGRAMS
    if version >= 110000:
        query = query.replace('NOT p.proisagg', "p.prokind IN ('f','p')")
        query = query.replace('p.prosrc AS body', "p.prosrc AS body, p.prokind = 'p' AS is_procedure")
        # pg_get_function_result() is not defined for procedures.
        query = query.replace('pg_get_function_result(p.oid)', "CASE WHEN p.prokind = 'p' THEN NULL ELSE pg_get_function_result(p.oid) END")
    with db.execute(query) as cursor:
        value = cursor.fetchone()[0]
    return json.loads(value) if isinstance(value,str) else value


def link_sources(program_list, options):
    root = Path(options['path'])
    pattern = options.get('pattern','{schema}/{kind}s/{name}.sql')
    counts = dict(same=0,absent=0,different=0)
    normalize = lambda s: re.sub(r"[\s'\"`\\]+",'',re.sub(r'--[^\n]*|/\*.*?\*/|#[^\n]*','',s,flags=re.S)).lower()
    for program in program_list:
        relative = pattern.format(**{k:program[k] for k in ('schema','kind','name')})
        target = (root/relative).resolve()
        if not target.is_relative_to(root.resolve()):
            continue
        state = 'absent'
        if target.is_file():
            raw = target.read_bytes()
            try:
                text = raw.decode('utf-8')
            except UnicodeDecodeError:
                text = raw.decode('latin-1')
            state = 'same' if normalize(program['body']) in normalize(text) else 'different'
        counts[state]+=1
        program['source'] = dict(status=state,path=relative,absolutePath=str(target),line=1)
    return dict(available=root.is_dir(),counts=counts)


def export(config):
    now = datetime.now(timezone.utc).isoformat(timespec='seconds')
    sources, engines = {}, {}
    for options in config.connections:
        db = Database(config,options)
        try:
            tables = schema(db)
            catalog = programs(db)
            stored = pg_programs(options.driver,catalog) if db.pg else mysql_programs(catalog)
            if db.pg:
                for p,row in zip(stored,catalog['functions']):
                    if row.get('is_procedure'):
                        p['kind'] = 'procedure'
            # Analysis needs the driver dialect; stable IDs use the configured source.
            index = analyse(options.driver,stored,tables)
            old_prefix, new_prefix = options.driver+':', options.id+':'
            for p in stored:
                p['id'] = new_prefix+p['id'][len(old_prefix):]
                for key in ('calls','calledBy'):
                    p['impact'][key] = [new_prefix+x[len(old_prefix):] for x in p['impact'][key]]
            for entry in index.values():
                for key,values in entry.items():
                    entry[key] = [new_prefix+x[len(old_prefix):] for x in values]
            sources[options.id] = tables
            engines[options.id] = dict(generatedAt=now,programs=stored,tableImpacts=index,tableStats=table_stats(catalog),error=None)
            if config.routines_repository:
                engines[options.id]['sourceRepository'] = link_sources(stored,config.routines_repository)
        except Exception as error:
            # Preserve the previous complete export instead of replacing it with partial data.
            raise RuntimeError(f"Catalog export failed for {options.id}: {config.redact(error)}") from None
        finally:
            db.close()
    config.data_dir.mkdir(parents=True,exist_ok=True)
    encode = lambda x: json.dumps(x,ensure_ascii=False,default=str,separators=(',',':'))
    write_atomic(config.data_dir/'programs.json',encode(dict(generatedAt=now,engines=engines)))
    write_atomic(config.data_dir/'schema.json',encode(dict(generatedAt=now,sources=sources)))
    index_file = config.data_dir/'snapshots/index.json'
    index = json.loads(index_file.read_text()) if index_file.exists() else dict(versions=[])
    previous = index['versions'][-1]['sources'] if index['versions'] else {}
    snapshots,changed = {},[]
    for name,tables in sources.items():
        digest = source_hash(tables)
        write_atomic(config.data_dir/f'snapshots/{name}/{digest}.json',encode(tables))
        snapshots[name] = dict(hash=digest,tables=len(tables))
        if previous.get(name,{}).get('hash') != digest:
            changed.append(name)
    if changed or not index['versions']:
        index['versions'].append(dict(generatedAt=now,origin='export',sources=snapshots,changed=changed))
        write_atomic(index_file,encode(index))


def main():
    parser=argparse.ArgumentParser()
    parser.add_argument('--config',default='atlas.toml')
    args=parser.parse_args()
    config=load_config(args.config)
    try:
        export(config)
    except Exception as error:
        print(config.redact(error))
        return 1
    return 0


if __name__=='__main__':
    raise SystemExit(main())
