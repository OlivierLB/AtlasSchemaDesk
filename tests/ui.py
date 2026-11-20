"""Browser + HTTP checks against public samples, including N arbitrary source IDs."""
import json
import os
import subprocess
import sys
import time
import urllib.error
import urllib.request
from pathlib import Path
from playwright.sync_api import sync_playwright

ROOT=Path(__file__).resolve().parents[1]
BASE='http://127.0.0.1:18765'


def request(path,body=None,headers=None):
    req=urllib.request.Request(BASE+path,data=json.dumps(body).encode() if body is not None else None,
        headers=headers or ({'Content-Type':'application/json','X-DB-Atlas':'1'} if body is not None else {}))
    try:
        response=urllib.request.urlopen(req,timeout=30)
    except urllib.error.HTTPError as error:
        response=error
    data=response.read()
    try:
        data=json.loads(data)
    except ValueError:
        pass
    return response.status,data


def main():
    dest=ROOT/'.local/test-results'
    dest.mkdir(parents=True,exist_ok=True)
    with (dest/'server.log').open('w') as log:
        process=subprocess.Popen([sys.executable,'-m','atlas','--config',str(ROOT/'.local/test.toml')],cwd=ROOT,stdout=log,stderr=subprocess.STDOUT)
        try:
            for _ in range(200):
                if process.poll() is not None:
                    raise RuntimeError('Server exited; see .local/test-results/server.log')
                try:
                    status,data=request('/api/status')
                    if status==200 and data['state'] in ('success','error'):
                        assert data['state']=='success',data
                        break
                except OSError:
                    pass
                time.sleep(.1)
            else:
                raise RuntimeError('Server did not become ready')
            assert request('/api/config')[0]==200
            assert 'password' not in json.dumps(request('/api/config')[1]).lower()
            assert request('/api/config',headers={'Host':'evil.invalid'})[0]==403
            assert request('/api/sql',{'engine':'library','sql':'SELECT 1'},headers={'Content-Type':'application/json'})[0]==403
            for sql in ['UPDATE actor SET first_name=\'bad\'','SET default_transaction_read_only=off','SELECT 1 /*!; SET SESSION TRANSACTION READ WRITE */']:
                assert request('/api/sql',dict(engine='library',sql=sql))[0]==400
            assert request('/api/sql',dict(engine='unknown',sql='SELECT 1'))[0]==400
            assert request('/api/s3/refresh',{})[0]==404
            assert request('/api/data/rows',dict(engine='library',schema='public',table='actor',filters={'unknown':'1'}))[0]==400
            assert request('/api/sql',dict(engine='library',sql='SELECT 1'))[0]==200
            assert request('/api/sql',dict(engine='library-write',sql='DELETE FROM atlas_probe'))[1]['needsConfirmation']
            with sync_playwright() as p:
                browser=p.firefox.launch(headless=True)
                page=browser.new_page(viewport=dict(width=1440,height=1000))
                failures=[]
                page.on('pageerror',lambda error:failures.append(str(error)))
                page.goto(BASE)
                page.wait_for_selector('#main .page-title')
                page.wait_for_function('all.length > 0 && programsById.size > 0')
                assert page.evaluate('ENGINES.length')==6
                assert page.evaluate('connectionOf("store").driver')=='mysql'
                assert page.evaluate('SQL_DEFAULT_SCHEMA.library')=='public'
                page.screenshot(path=str(dest/'home.png'),full_page=True)
                page.goto(BASE+'/#/library/public/actor/donnees')
                page.wait_for_function('dataView && !dataView.loading && dataView.rows.length > 0')
                assert not page.locator('td[title="Double-clic : modifier"]').count()
                page.screenshot(path=str(dest/'actor.png'),full_page=True)
                page.goto(BASE+'/#/sql/store')
                page.wait_for_selector('.CodeMirror')
                assert page.evaluate('sqlEditor.getOption("mode")')=='text/x-mysql'
                page.evaluate('sqlEditor.setValue("SELECT actor_id, first_name FROM actor LIMIT 3")')
                page.locator('#sql-run').click()
                page.wait_for_function('sqlState.store.response || sqlState.store.error')
                assert not page.evaluate('sqlState.store.error'),page.evaluate('sqlState.store')
                assert len(page.evaluate('sqlState.store.response.results[0].rows'))==3
                page.screenshot(path=str(dest/'sql.png'),full_page=True)
                page.goto(BASE+'/#/library-write/public/atlas_probe/donnees')
                page.wait_for_function('dataView && !dataView.loading && dataView.rows.length > 0')
                assert page.locator('td[title="Double-clic : modifier"]').count()>0
                page.goto(BASE+'/#/library/public/actor/programmes')
                page.wait_for_selector('#main .page-title')
                # Empty catalogs must remain usable instead of displaying loading forever.
                page.route('**/data/schema.json?*',lambda route:route.fulfill(json=dict(generatedAt='2026-01-01',sources={})))
                page.goto(BASE)
                page.wait_for_selector('#main .page-title')
                page.goto(BASE+'/#/sql/library')
                page.wait_for_selector('.CodeMirror')
                assert not failures,failures
                browser.close()
            print('Browser and HTTP checks passed (6 configured sources); screenshots in .local/test-results')
        finally:
            process.terminate()
            try:
                process.wait(timeout=10)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait()


if __name__=='__main__':
    main()
