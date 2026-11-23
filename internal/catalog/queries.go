// Catalog queries (ported verbatim from the original implementation).

package catalog

const (
	pgColumns = `SELECT n.nspname AS schema_name,c.relname AS table_name,a.attname AS column_name,
 format_type(a.atttypid,a.atttypmod) AS data_type,a.attnotnull AS not_null,
 pg_get_expr(d.adbin,d.adrelid) AS default_value,
 EXISTS(SELECT 1 FROM pg_index i WHERE i.indrelid=c.oid AND i.indisprimary AND a.attnum=ANY(i.indkey)) AS is_primary
 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
 JOIN pg_attribute a ON a.attrelid=c.oid LEFT JOIN pg_attrdef d ON d.adrelid=c.oid AND d.adnum=a.attnum
 WHERE c.relkind IN ('r','p') AND a.attnum>0 AND NOT a.attisdropped
 AND n.nspname NOT IN ('pg_catalog','information_schema') AND n.nspname NOT LIKE 'pg_toast%'
 ORDER BY n.nspname,c.relname,a.attnum`
	pgFKs = `SELECT ns.nspname AS schema_name,src.relname AS table_name,sa.attname AS column_name,
 nt.nspname AS ref_schema,dst.relname AS ref_table,da.attname AS ref_column
 FROM pg_constraint con JOIN pg_class src ON src.oid=con.conrelid JOIN pg_namespace ns ON ns.oid=src.relnamespace
 JOIN pg_class dst ON dst.oid=con.confrelid JOIN pg_namespace nt ON nt.oid=dst.relnamespace
 JOIN LATERAL unnest(con.conkey,con.confkey) WITH ORDINALITY k(src_att,dst_att,ord) ON true
 JOIN pg_attribute sa ON sa.attrelid=src.oid AND sa.attnum=k.src_att
 JOIN pg_attribute da ON da.attrelid=dst.oid AND da.attnum=k.dst_att WHERE con.contype='f'
 ORDER BY ns.nspname,src.relname,con.conname,k.ord`
	pgUnique = `SELECT ns.nspname AS schema_name,tbl.relname AS table_name,con.conname AS constraint_name,
 att.attname AS column_name FROM pg_constraint con JOIN pg_class tbl ON tbl.oid=con.conrelid
 JOIN pg_namespace ns ON ns.oid=tbl.relnamespace
 JOIN LATERAL unnest(con.conkey) WITH ORDINALITY k(attnum,ord) ON true
 JOIN pg_attribute att ON att.attrelid=tbl.oid AND att.attnum=k.attnum WHERE con.contype IN ('p','u')
 ORDER BY ns.nspname,tbl.relname,con.conname,k.ord`
	mysqlColumns = `SELECT TABLE_SCHEMA AS schema_name,TABLE_NAME AS table_name,COLUMN_NAME AS column_name,
 COLUMN_TYPE AS data_type,IS_NULLABLE='NO' AS not_null,COLUMN_DEFAULT AS default_value,COLUMN_KEY='PRI' AS is_primary
 FROM information_schema.COLUMNS WHERE TABLE_SCHEMA NOT IN ('mysql','sys','performance_schema','information_schema') ORDER BY TABLE_SCHEMA,TABLE_NAME,ORDINAL_POSITION`
	mysqlFKs = `SELECT TABLE_SCHEMA AS schema_name,TABLE_NAME AS table_name,COLUMN_NAME AS column_name,
 REFERENCED_TABLE_SCHEMA AS ref_schema,REFERENCED_TABLE_NAME AS ref_table,REFERENCED_COLUMN_NAME AS ref_column
 FROM information_schema.KEY_COLUMN_USAGE WHERE REFERENCED_TABLE_NAME IS NOT NULL
 ORDER BY TABLE_SCHEMA,TABLE_NAME,CONSTRAINT_NAME,ORDINAL_POSITION`
	mysqlUnique = `SELECT TABLE_SCHEMA AS schema_name,TABLE_NAME AS table_name,INDEX_NAME AS constraint_name,
 COLUMN_NAME AS column_name FROM information_schema.STATISTICS WHERE NON_UNIQUE=0 AND COLUMN_NAME IS NOT NULL
 ORDER BY TABLE_SCHEMA,TABLE_NAME,INDEX_NAME,SEQ_IN_INDEX`
	pgPrograms = `
SELECT json_build_object(
  'functions', COALESCE((SELECT json_agg(row_to_json(f)) FROM (
    SELECT n.nspname AS schema_name, p.proname AS name,
           pg_get_function_identity_arguments(p.oid) AS arguments,
           pg_get_function_result(p.oid) AS returns,
           l.lanname AS language,
           p.prorettype = 'trigger'::regtype AS is_trigger_function,
           p.prosrc AS body
    FROM pg_proc p
    JOIN pg_namespace n ON n.oid = p.pronamespace
    JOIN pg_language l ON l.oid = p.prolang
    LEFT JOIN pg_depend d ON d.objid = p.oid AND d.deptype = 'e'
    WHERE n.nspname NOT IN ('pg_catalog', 'information_schema') AND d.objid IS NULL AND NOT p.proisagg
    ORDER BY 1, 2) f), '[]'::json),
  'triggers', COALESCE((SELECT json_agg(row_to_json(t)) FROM (
    SELECT n.nspname AS schema_name, c.relname AS table_name, tg.tgname AS name,
           pg_get_triggerdef(tg.oid) AS definition,
           fn.nspname AS function_schema, p.proname AS function_name
    FROM pg_trigger tg
    JOIN pg_class c ON c.oid = tg.tgrelid
    JOIN pg_namespace n ON n.oid = c.relnamespace
    JOIN pg_proc p ON p.oid = tg.tgfoid
    JOIN pg_namespace fn ON fn.oid = p.pronamespace
    WHERE NOT tg.tgisinternal AND n.nspname NOT IN ('pg_catalog', 'information_schema')
    ORDER BY 1, 2, 3) t), '[]'::json),
  'views', COALESCE((SELECT json_agg(row_to_json(v)) FROM (
    SELECT n.nspname AS schema_name, c.relname AS name, c.relkind = 'm' AS materialized,
           pg_get_viewdef(c.oid) AS body
    FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
    WHERE c.relkind IN ('v', 'm') AND n.nspname NOT IN ('pg_catalog', 'information_schema')
    ORDER BY 1, 2) v), '[]'::json),
  'table_stats', COALESCE((SELECT json_agg(row_to_json(s)) FROM (
    SELECT n.nspname AS schema_name, c.relname AS name,
           c.reltuples::bigint AS estimated_rows, pg_total_relation_size(c.oid) AS total_bytes,
           pg_relation_size(c.oid) AS heap_bytes
    FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
    WHERE c.relkind IN ('r', 'p') AND n.nspname NOT IN ('pg_catalog', 'information_schema')) s), '[]'::json)
)::text;
`
)

// mysqlPrograms lists the MySQL program catalog queries in export order.
var mysqlPrograms = []struct{ name, sql string }{
	{"routines", `SELECT ROUTINE_SCHEMA AS schema_name,ROUTINE_NAME AS name,ROUTINE_TYPE AS type,DTD_IDENTIFIER AS returns,ROUTINE_DEFINITION AS body,LAST_ALTERED AS last_altered,ROUTINE_COMMENT AS comment FROM information_schema.ROUTINES WHERE ROUTINE_SCHEMA NOT IN ('mysql','sys','performance_schema','information_schema')`},
	{"parameters", `SELECT SPECIFIC_SCHEMA AS schema_name,SPECIFIC_NAME AS name,ROUTINE_TYPE AS type,ORDINAL_POSITION AS position,PARAMETER_MODE AS mode,PARAMETER_NAME AS parameter,DTD_IDENTIFIER AS data_type FROM information_schema.PARAMETERS WHERE SPECIFIC_SCHEMA NOT IN ('mysql','sys','performance_schema','information_schema') AND ORDINAL_POSITION>0 ORDER BY 1,2,4`},
	{"triggers", `SELECT TRIGGER_SCHEMA AS schema_name,TRIGGER_NAME AS name,EVENT_MANIPULATION AS event,EVENT_OBJECT_SCHEMA AS table_schema,EVENT_OBJECT_TABLE AS table_name,ACTION_TIMING AS timing,ACTION_STATEMENT AS body FROM information_schema.TRIGGERS WHERE TRIGGER_SCHEMA NOT IN ('mysql','sys','performance_schema','information_schema')`},
	{"views", `SELECT TABLE_SCHEMA AS schema_name,TABLE_NAME AS name,VIEW_DEFINITION AS body FROM information_schema.VIEWS WHERE TABLE_SCHEMA NOT IN ('mysql','sys','performance_schema','information_schema')`},
	{"events", `SELECT EVENT_SCHEMA AS schema_name,EVENT_NAME AS name,STATUS AS status,EVENT_TYPE AS event_type,EXECUTE_AT AS execute_at,INTERVAL_VALUE AS interval_value,INTERVAL_FIELD AS interval_field,STARTS AS starts,ENDS AS ends,TIME_ZONE AS time_zone,@@system_time_zone AS system_time_zone,@@GLOBAL.event_scheduler AS event_scheduler,CONVERT_TZ(CREATED,@@SESSION.time_zone,TIME_ZONE) AS created_event_zone,LAST_EXECUTED AS last_executed,EVENT_DEFINITION AS body FROM information_schema.EVENTS WHERE EVENT_SCHEMA NOT IN ('mysql','sys','performance_schema','information_schema')`},
	{"table_stats", `SELECT TABLE_SCHEMA AS schema_name,TABLE_NAME AS name,TABLE_ROWS AS estimated_rows,DATA_LENGTH+INDEX_LENGTH AS total_bytes FROM information_schema.TABLES WHERE TABLE_TYPE='BASE TABLE' AND TABLE_SCHEMA NOT IN ('mysql','sys','performance_schema','information_schema')`},
}
