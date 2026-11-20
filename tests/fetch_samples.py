"""Download public sample databases to ignored storage with pinned checksums."""
import hashlib
import io
import json
import tarfile
import urllib.request
from pathlib import Path

ROOT=Path(__file__).resolve().parents[1]


def main():
    lock=json.loads((ROOT/'tests/samples.lock.json').read_text())
    dest=ROOT/'.local/samples'
    dest.mkdir(parents=True,exist_ok=True)
    for item in lock:
        request=urllib.request.Request(item['url'],headers={'User-Agent':'AtlasSchemaDesk-tests'})
        data=urllib.request.urlopen(request,timeout=60).read()
        if hashlib.sha256(data).hexdigest()!=item['sha256']:
            raise RuntimeError('Public sample checksum mismatch: '+item['name'])
        if item.get('archive'):
            with tarfile.open(fileobj=io.BytesIO(data),mode='r:gz') as archive:
                for source,target in item['members'].items():
                    (dest/target).write_bytes(archive.extractfile(source).read())
        else:
            (dest/item['name']).write_bytes(data)


if __name__=='__main__':
    main()
