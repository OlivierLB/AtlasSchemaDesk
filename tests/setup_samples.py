"""Initialize only the disposable Compose project used by this test suite."""
import os
import subprocess
from pathlib import Path

ROOT=Path(__file__).resolve().parents[1]
BASE=['docker','compose','-p','atlas-schema-desk-test','-f',str(ROOT/'docker-compose.test.yml')]


def execute(service,sql):
    command=['psql','-X','-v','ON_ERROR_STOP=1','-U','atlas_test','-d','library' if service=='postgres10' else 'pagila'] if service.startswith('postgres') else ['mysql','-uroot','--default-character-set=utf8mb4']
    env=['-e','MYSQL_PWD=public-test-only'] if service=='mysql' else []
    subprocess.run(BASE+['exec','-T',*env,service,*command],input=sql,check=True,stdout=subprocess.DEVNULL)


def main():
    execute('postgres',b"DO $$ BEGIN IF NOT EXISTS(SELECT 1 FROM pg_roles WHERE rolname='postgres') THEN CREATE ROLE postgres; END IF; END $$; DROP SCHEMA public CASCADE; CREATE SCHEMA public;")
    for name in ('pagila-schema.sql','pagila-data.sql'):
        execute('postgres',(ROOT/'.local/samples'/name).read_bytes())
    for name in ('sakila-schema.sql','sakila-data.sql'):
        execute('mysql',(ROOT/'.local/samples'/name).read_bytes())
    execute('postgres10',b'CREATE TABLE public.actor(actor_id integer primary key, first_name text, last_name text); INSERT INTO public.actor VALUES(1,\'Ada\',\'Example\');')
    pg_sql=b"""CREATE TABLE public.atlas_probe(id integer primary key, value text);
    INSERT INTO public.atlas_probe VALUES(1,'original');
    CREATE TABLE public.atlas_audit(id serial primary key, value text);
    CREATE FUNCTION public.atlas_audit_write() RETURNS trigger LANGUAGE plpgsql AS $$
    BEGIN INSERT INTO public.atlas_audit(value) VALUES(NEW.value); RETURN NEW; END $$;
    CREATE TRIGGER atlas_probe_changed AFTER UPDATE ON public.atlas_probe FOR EACH ROW EXECUTE PROCEDURE public.atlas_audit_write();
    """
    for service in ('postgres','postgres10'):
        execute(service,pg_sql)
    execute('mysql',b"""USE sakila;
    CREATE TABLE atlas_probe(id integer primary key, value varchar(100)) ENGINE=InnoDB;
    INSERT INTO atlas_probe VALUES(1,'original');
    CREATE TABLE atlas_audit(id integer primary key auto_increment, value varchar(100));
    CREATE TRIGGER atlas_probe_changed AFTER UPDATE ON atlas_probe FOR EACH ROW INSERT INTO atlas_audit(value) VALUES(NEW.value);
    CREATE PROCEDURE atlas_probe_read() SELECT * FROM atlas_probe;
    CREATE EVENT atlas_probe_tick ON SCHEDULE EVERY 1 DAY DISABLE DO INSERT INTO atlas_audit(value) VALUES('scheduled');
CREATE EVENT atlas_probe_week ON SCHEDULE EVERY 1 WEEK STARTS '2026-01-05 12:00:00' DISABLE DO SELECT 1;
CREATE EVENT atlas_probe_quarter ON SCHEDULE EVERY 1 QUARTER STARTS '2026-01-01 12:00:00' DISABLE DO SELECT 1;
    """)
    config=ROOT/'.local/test.toml'
    config.parent.mkdir(exist_ok=True)
    lines=['[server]','port = 18765','data_dir = "data"','allowed_db_hosts = ["127.0.0.1"]']
    for name,driver,port,database,user in [('library','postgresql',55432,'pagila','atlas_test'),('store','mysql',53306,'sakila','root'),('older','postgresql',55433,'library','atlas_test')]:
        for writable in (False,True):
            ident=name+('-write' if writable else '')
            lines.extend(['','[[connections]]',f'id = "{ident}"',f'label = "{ident}"',f'driver = "{driver}"','host = "127.0.0.1"',f'port = {port}',f'database = "{database}"',f'user = "{user}"','password_env = "ATLAS_TEST_PASSWORD"',f'default_schema = "{database if driver=="mysql" else "public"}"',f'allow_writes = {str(writable).lower()}'])
    config.write_text('\n'.join(lines)+'\n')
    (ROOT/'.local/.env').write_text('ATLAS_TEST_PASSWORD=public-test-only\n')


if __name__=='__main__':
    main()
