"""HTTP and real-browser acceptance tests against disposable public databases."""
import json
import os
import subprocess
import time
import urllib.error
import urllib.request
from pathlib import Path

from playwright.sync_api import sync_playwright

ROOT = Path(__file__).resolve().parents[1]
BASE = os.environ.get('ATLAS_TEST_BASE', 'http://127.0.0.1:18765')
SESSION = ''


def request(path, body=None, headers=None):
    defaults = {'X-Atlas-Session': SESSION} if SESSION else {}
    if body is not None:
        defaults |= {'Content-Type': 'application/json', 'X-DB-Atlas': '1'}
    req = urllib.request.Request(BASE + path, data=json.dumps(body).encode() if body is not None else None,
                                 headers=headers if headers is not None else defaults)
    try:
        response = urllib.request.urlopen(req, timeout=30)
    except urllib.error.HTTPError as error:
        response = error
    raw = response.read()
    try:
        data = json.loads(raw)
    except ValueError:
        data = raw
    return response.status, data


def browser_sql(page, sql):
    return page.evaluate('''async sql => {
      const response = await fetch('/api/sql', {method:'POST', headers:{'Content-Type':'application/json',
        'X-DB-Atlas':'1','X-Atlas-Session':sessionStorage.getItem('atlas-session')},
        body:JSON.stringify({engine:'library-write',sql})});
      return {status:response.status, body:await response.json()};
    }''', sql)


def browser_end(page, action):
    return page.evaluate('''async action => {
      const response = await fetch('/api/sql/'+action, {method:'POST', headers:{'Content-Type':'application/json',
        'X-DB-Atlas':'1','X-Atlas-Session':sessionStorage.getItem('atlas-session')},
        body:JSON.stringify({engine:'library-write'})});
      return {status:response.status,body:await response.json()};
    }''', action)


def wait_ready(timeout=60):
    deadline = time.time() + timeout
    while time.time() < deadline:
        try:
            status, data = request('/api/status', headers={})
            if status == 200 and data['state'] == 'success':
                return
        except OSError:
            pass
        time.sleep(.2)
    raise RuntimeError('Server not ready after restart')


def connections_page(context, process):
    """Add, edit and delete a connection and a backup source, then apply them."""
    pid = process.pid
    page = context.new_page()
    page.on('dialog', lambda dialog: dialog.accept())
    page.goto(BASE + '/#/connexions')
    page.wait_for_selector('#connections-table > article')
    assert page.locator('#connections-table > article').count() == 6
    assert 'public-test-only' not in page.content()
    page.locator('#connection-add').click()
    form = page.get_by_role('form', name='Connexion')
    form.get_by_label('Identifiant').fill('extra')
    form.get_by_label('Libellé').fill('Extra sample')
    form.get_by_label('Port', exact=True).fill('55432')
    form.get_by_label('Base', exact=True).fill('pagila')
    form.get_by_label('Utilisateur').fill('atlas_test')
    assert form.get_by_label('Variable du mot de passe').input_value() == 'ATLAS_EXTRA_PASSWORD'
    form.get_by_label('Mot de passe', exact=True).fill('public-test-only')
    form.get_by_role('button', name='Enregistrer').click()
    page.wait_for_selector('#connection-extra')
    page.locator('#settings-restart').wait_for()
    page.locator('#connection-extra').get_by_role('button', name='Associer une sauvegarde').click()
    source = page.get_by_role('form', name='Source de sauvegarde')
    assert source.get_by_role('combobox', name='Connexion', exact=True).input_value() == 'extra'
    assert source.get_by_role('combobox', name='Connexion', exact=True).is_disabled()
    source.get_by_label('Bucket').fill('public-example')
    source.get_by_label('Clé de l’objet').fill('extra.sql.gz')
    source.get_by_role('button', name='Enregistrer').click()
    page.locator('#connection-extra code:text-is("s3://public-example/extra.sql.gz")').wait_for()
    page.locator('#connection-extra').get_by_text('Détails de restauration', exact=True).click()
    page.locator('#connection-extra').get_by_role('button', name='Dissocier la sauvegarde').click()
    page.locator('#connection-extra').get_by_text('Aucune source associée à cette base').wait_for()
    page.locator('#settings-restart').click()
    # The banner disappears only after the reload that follows the restart.
    page.locator('#settings-restart').wait_for(state='detached', timeout=60000)
    assert process.pid == pid and process.poll() is None, 'restart must keep the same process'
    wait_ready()
    page.goto(BASE + '/#/extra/public/actor/colonnes')
    page.wait_for_selector('#tree a[href="#/extra/public/actor/colonnes"]', timeout=30000)
    page.goto(BASE + '/#/connexions')
    page.locator('#connection-extra').get_by_role('button', name='Supprimer la base Extra sample', exact=True).click()
    page.locator('#settings-restart').click()
    page.locator('#settings-restart').wait_for(state='detached', timeout=60000)
    page.wait_for_selector('#connections-table > article')
    assert page.locator('#connections-table > article').count() == 6
    wait_ready()
    page.close()


def main():
    global SESSION
    dest = ROOT / '.local/test-results'
    dest.mkdir(parents=True, exist_ok=True)
    with (dest / 'server.log').open('w') as log:
        binary = os.environ.get('ATLAS_BINARY', str(ROOT / 'bin/atlas-schema-desk'))
        process = subprocess.Popen([binary, '--config', str(ROOT / '.local/test.toml')],
                                   cwd=ROOT, stdout=log, stderr=subprocess.STDOUT)
        try:
            for _ in range(300):
                if process.poll() is not None:
                    raise RuntimeError('Server exited; see .local/test-results/server.log')
                try:
                    status, data = request('/api/status')
                    if status == 200 and data['state'] in ('success', 'error'):
                        assert data['state'] == 'success', data
                        break
                except OSError:
                    pass
                time.sleep(.1)
            else:
                raise RuntimeError('Server did not become ready')
            SESSION = request('/api/sessions', {})[1]['id']
            assert request('/api/config')[0] == 200
            assert 'password' not in json.dumps(request('/api/config')[1]).lower()
            assert request('/api/config', headers={'Host': 'evil.invalid'})[0] == 403
            assert request('/api/sql', {'engine': 'library', 'sql': 'SELECT 1'}, headers={'Content-Type': 'application/json'})[0] == 403
            for sql in ['UPDATE actor SET first_name=\'bad\'', 'SET default_transaction_read_only=off', 'SELECT 1 /*!; SET SESSION TRANSACTION READ WRITE */']:
                assert request('/api/sql', dict(engine='library', sql=sql))[0] == 400
            assert request('/api/sql', dict(engine='unknown', sql='SELECT 1'))[0] == 400
            assert request('/api/s3/refresh', {})[0] == 404
            assert request('/api/data/rows', dict(engine='library', schema='public', table='actor', filters={'unknown': '1'}))[0] == 400
            assert request('/api/sql', dict(engine='library', sql='SELECT 1'))[0] == 200
            assert request('/api/sql', dict(engine='library-write', sql='DELETE FROM atlas_probe'))[1]['needsConfirmation']
            with sync_playwright() as p:
                browser = p.firefox.launch(headless=True)
                context = browser.new_context(viewport=dict(width=1440, height=1000))
                page = context.new_page()
                failures = []
                page.on('pageerror', lambda error: failures.append(str(error)))
                page.goto(BASE)
                page.wait_for_selector('#main .page-title')
                page.locator('.tree-engine-toggle').first.click()
                page.wait_for_selector('#tree a[href="#/library/public/actor/colonnes"]')
                page.screenshot(path=str(dest / 'home.png'), full_page=True)
                page.goto(BASE + '/#/library/public/actor/donnees')
                page.wait_for_selector('.data-table tbody tr')
                assert page.locator('td[title="Double-clic : modifier"]').count() == 0
                page.screenshot(path=str(dest / 'actor.png'), full_page=True)
                page.goto(BASE + '/#/sql/store')
                page.wait_for_selector('.CodeMirror')
                editor = page.locator('.CodeMirror')
                editor.click()
                page.keyboard.press('Control+a')
                page.keyboard.insert_text('SELECT actor_id, first_name FROM actor LIMIT 3')
                page.locator('#sql-run').click()
                page.wait_for_selector('#sql-results tbody tr')
                assert page.locator('#sql-results tbody tr').count() == 3
                assert page.locator('.data-error').count() == 0
                page.screenshot(path=str(dest / 'sql.png'), full_page=True)
                # A reload retains the session, but a cloned tab must not share it.
                original = page.evaluate('sessionStorage.getItem("atlas-session")')
                page.reload()
                page.wait_for_selector('.CodeMirror')
                assert page.evaluate('sessionStorage.getItem("atlas-session")') == original
                second = context.new_page()
                second.add_init_script(f'sessionStorage.setItem("atlas-session", {json.dumps(original)})')
                second.goto(BASE + '/#/sql/library-write')
                second.wait_for_selector('.CodeMirror')
                assert second.evaluate('sessionStorage.getItem("atlas-session")') != original
                # Two real database sessions see only their own uncommitted state.
                result = browser_sql(page, "UPDATE atlas_probe SET value='tab-one' WHERE id=1")
                assert result['status'] == 200 and result['body']['txOpen'], result
                assert browser_sql(page, 'SELECT value FROM atlas_probe WHERE id=1')['body']['results'][0]['rows'][0][0] == 'tab-one'
                assert browser_sql(second, 'SELECT value FROM atlas_probe WHERE id=1')['body']['results'][0]['rows'][0][0] == 'original'
                assert browser_end(second, 'commit')['body']['done'] is False
                assert browser_sql(page, 'SELECT value FROM atlas_probe WHERE id=1')['body']['results'][0]['rows'][0][0] == 'tab-one'
                assert browser_end(page, 'rollback')['body']['done'] is True
                second.close()
                page.goto(BASE + '/#/library-write/public/atlas_probe/donnees')
                page.wait_for_selector('td[title="Double-clic : modifier"]')
                page.locator('td[title="Double-clic : modifier"]').filter(has_text='original').dblclick()
                page.locator('#cell-input').fill('edited-in-grid')
                page.get_by_role('button', name='Prévisualiser').click()
                page.locator('#edit-run').click()
                page.wait_for_selector('td:text-is("edited-in-grid")')
                page.get_by_role('button', name='Annuler', exact=True).click()
                page.wait_for_selector('td:text-is("original")')
                page.goto(BASE + '/#/library/public/actor/programmes')
                page.wait_for_selector('#main .page-title')
                page.get_by_role('button', name='Rechercher dans le catalogue', exact=True).click()
                page.locator('#palette-input').fill('actor')
                page.wait_for_selector('#palette-results button')
                page.keyboard.press('Escape')
                page.locator('#versions-toggle').click()
                page.wait_for_selector('select[aria-label="Version de library"]')
                page.get_by_role('button', name='Fermer', exact=True).click()
                # Expiry warning is driven by server state; polling never renews it.
                warning = context.new_page()
                renewal = {'done': False}
                def current_session(route):
                    route.fulfill(json=dict(transactions={}, transactionsLostAt=None,
                        expiresInSeconds=1800 if renewal['done'] else 290, active=0))
                def renew_session(route):
                    renewal['done'] = True
                    route.fulfill(json=dict(transactions={}, transactionsLostAt=None, expiresInSeconds=1800, active=0))
                warning.route('**/api/sessions/current', current_session)
                warning.route('**/api/sessions/renew', renew_session)
                warning.goto(BASE)
                warning.get_by_role('button', name='Prolonger la session').wait_for()
                warning.get_by_role('button', name='Prolonger la session').click()
                warning.get_by_role('button', name='Prolonger la session').wait_for(state='detached')
                assert renewal['done']
                warning.route('**/api/status', lambda route: route.fulfill(status=409, json=dict(error='Session expired', code='session_expired')))
                warning.get_by_role('button', name='Nouvelle session').wait_for()
                previous_session = warning.evaluate('sessionStorage.getItem("atlas-session")')
                warning.unroute('**/api/status')
                warning.get_by_role('button', name='Nouvelle session').click()
                warning.get_by_role('button', name='Nouvelle session').wait_for(state='detached')
                assert warning.evaluate('sessionStorage.getItem("atlas-session")') != previous_session
                warning.locator('#versions-toggle').click()
                warning.get_by_role('button', name='Opérations', exact=True).click()
                warning.get_by_role('button', name='Annuler les transactions et fermer la session').click()
                warning.get_by_role('button', name='Nouvelle session').wait_for()
                warning.close()
                # Empty catalogs remain usable, including the SQL console.
                page.route('**/data/schema.json?*', lambda route: route.fulfill(json=dict(generatedAt='2026-01-01', sources={})))
                page.goto(BASE)
                page.reload()
                page.wait_for_selector('#main .page-title')
                page.get_by_role('heading', name='Aucun schéma chargé', exact=True).wait_for()
                page.goto(BASE + '/#/sql/library')
                page.wait_for_selector('.CodeMirror')
                page.unroute('**/data/schema.json?*')
                connections_page(context, process)
                assert not failures, failures
                browser.close()
            print('React browser and HTTP checks passed; two-tab transactions, reload, editing, connection settings and routes verified')
        finally:
            process.terminate()
            try:
                process.wait(timeout=15)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait()


if __name__ == '__main__':
    main()
