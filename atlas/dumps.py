"""Optional directory and S3-versioned backups; never execute request-supplied commands."""
import argparse
import hashlib
import json
import os
import shutil
import subprocess
import tempfile
from datetime import datetime, timezone
from pathlib import Path
from .config import load_config
from .snapshot import write_atomic


def now():
    return datetime.now(timezone.utc).isoformat(timespec='seconds')


def aws(*args):
    result = subprocess.run(['aws',*args,'--output','json'],capture_output=True,text=True,check=True)
    return json.loads(result.stdout)


def versions(source):
    if source['type']=='directory':
        root = Path(source['path'])
        result=[]
        for path in root.glob(source.get('pattern','*.sql*')):
            if not path.is_file() or not path.resolve().is_relative_to(root.resolve()):
                continue
            stat=path.stat()
            version=hashlib.sha256((path.name+str(stat.st_size)+str(stat.st_mtime_ns)).encode()).hexdigest()
            result.append(dict(versionId=version,lastModified=datetime.fromtimestamp(stat.st_mtime,timezone.utc).isoformat(),size=stat.st_size,_path=str(path)))
        return sorted(result,key=lambda x:x['lastModified'],reverse=True)
    data = aws('s3api','list-object-versions','--bucket',source['bucket'],'--prefix',source['key'])
    return [dict(versionId=x['VersionId'],lastModified=x['LastModified'],size=x['Size'],isLatest=x.get('IsLatest',False))
            for x in data.get('Versions',[]) if x['Key']==source['key']]


def listing(config):
    targets=[]
    for source in config.dump_sources:
        conn=config.connection(source['connection'])
        try:
            items=[{k:v for k,v in x.items() if k!='_path'} for x in versions(source)]
            targets.append(dict(engine=conn.id,label=conn.label,key=source.get('key','directory'),versions=items,error=None,checkedAt=now()))
        except Exception as error:
            targets.append(dict(engine=conn.id,label=conn.label,versions=[],error=dict(message=config.redact(error))))
    write_atomic(config.data_dir/'s3-dumps.json',json.dumps(dict(generatedAt=now(),targets=targets)))


def apply(config, connection, version):
    source=next((x for x in config.dump_sources if x['connection']==connection),None)
    if source is None:
        raise ValueError('No dump source configured for this connection')
    config.connection(connection)
    command=source.get('restore_command')
    if not isinstance(command,str) or not command.strip():
        raise ValueError('No restore_command configured')
    item=next((x for x in versions(source) if x['versionId']==version),None)
    if item is None:
        raise ValueError('Unknown dump version')
    target=Path(source.get('local_path',config.path.parent/'.local/dumps'/f'{connection}.sql.gz'))
    target.parent.mkdir(parents=True,exist_ok=True)
    if shutil.disk_usage(target.parent).free < item['size']+64*1024*1024:
        raise RuntimeError('Not enough free space for the downloaded dump')
    fd,temporary=tempfile.mkstemp(prefix='.download-',dir=target.parent)
    os.close(fd)
    temporary=Path(temporary)
    try:
        if source['type']=='directory':
            shutil.copyfile(item['_path'],temporary)
        else:
            aws('s3api','get-object','--bucket',source['bucket'],'--key',source['key'],'--version-id',version,str(temporary))
        if temporary.stat().st_size!=item['size']:
            raise RuntimeError('Downloaded dump size does not match the selected version')
        # The previous local dump remains untouched if restoration fails.
        env={**os.environ,'ATLAS_DUMP_PATH':str(temporary),'ATLAS_CONNECTION':connection}
        result=subprocess.run(command,shell=True,cwd=config.path.parent,env=env,stdout=subprocess.PIPE,stderr=subprocess.STDOUT,text=True)
        if result.returncode:
            raise RuntimeError('Restore command failed: '+config.redact(result.stdout[-4000:]))
        os.replace(temporary,target)
        applied_file=config.data_dir/'applied-dumps.json'
        applied=json.loads(applied_file.read_text()) if applied_file.exists() else {}
        applied[connection]=dict(versionId=version,lastModified=item['lastModified'],appliedAt=now())
        write_atomic(applied_file,json.dumps(applied))
        from .catalog import export
        export(config)
    finally:
        temporary.unlink(missing_ok=True)


def main():
    parser=argparse.ArgumentParser()
    parser.add_argument('--config',default='atlas.toml')
    parser.add_argument('action',choices=('list','apply'))
    parser.add_argument('connection',nargs='?')
    parser.add_argument('version',nargs='?')
    args=parser.parse_args()
    config=load_config(args.config)
    try:
        listing(config) if args.action=='list' else apply(config,args.connection,args.version)
    except Exception as error:
        print(config.redact(error))
        return 1
    return 0


if __name__=='__main__':
    raise SystemExit(main())
