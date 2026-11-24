"""Isolated UX checks against the production frontend; no real DB/S3 access.

Run after npm run build --prefix frontend, using the Playwright test venv.
"""
import copy
import functools
import json
import threading
from http.server import SimpleHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from urllib.parse import urlparse

from playwright.sync_api import sync_playwright, expect

ROOT = Path(__file__).resolve().parents[1]
DEST = ROOT / ".local/test-results/redesign"


class QuietHandler(SimpleHTTPRequestHandler):
    def log_message(self, *_args):
        pass


def main():
    DEST.mkdir(parents=True, exist_ok=True)
    server = ThreadingHTTPServer(("127.0.0.1", 0), functools.partial(QuietHandler, directory=str(ROOT / "web/dist/app")))
    threading.Thread(target=server.serve_forever, daemon=True).start()
    base = f"http://127.0.0.1:{server.server_port}"
    connections = [dict(id=key, label=label, driver=driver, host="127.0.0.1", port=port, database=key, user="reader", defaultSchema="public", color=color, allowWrites=False, passwordEnv=f"{key.upper()}_PASSWORD", backup=None) for key, label, driver, port, color in [("library", "Bibliothèque", "postgresql", 5432, "#79b8ff"), ("store", "Boutique", "mysql", 3306, "#e5a85e")]]
    backup = dict(connection="library", type="s3-versioned", bucket="sample-backups", key="library.sql.gz", path="", pattern="", localPath="dumps/library.sql.gz", restoreCommand="restore-library")
    connections[0]["backup"] = backup
    connections[1]["allowWrites"] = True
    tables = [dict(schema="public", name=name, columns=[dict(name="id", type="integer", primary=True, nullable=False), dict(name="name", type="text", nullable=True)], foreignKeys=[]) for name in ["authors", "books"]]
    tables[1]["foreignKeys"] = [dict(column="id", targetSchema="public", targetTable="authors", targetColumn="id")]
    state = dict(configPath="sulcata.example.toml", allowedDbHosts=["127.0.0.1"], connections=connections, dumpSources=[backup], pendingRestart=False)
    writes = []
    failures = []
    runtime = dict(transactions={}, restore="idle", cancelled=False)
    restores = []
    row_queries = []
    scheduled = dict(id="store:event:daily_refresh", kind="event", schema="public", name="daily_refresh", schedule="EVERY 1 DAY", status="ENABLED", body="CALL refresh_products();", impact=dict(writes=["public.products"], calls=["store:procedure:refresh_products"]), source=dict(status="matched", path="events/daily_refresh.sql", line=1))
    procedure = dict(id="store:procedure:refresh_products", kind="procedure", schema="public", name="refresh_products", body="SELECT * FROM products;", impact=dict(reads=["public.products"]))

    def respond(route):
        path = urlparse(route.request.url).path
        if path == "/api/config":
            data = dict(connections=[dict(id=c["id"], label=c["label"], driver=c["driver"], default_schema=c["defaultSchema"], color=c["color"], allow_writes=c["allowWrites"]) for c in connections], modules=dict(dumps=True, routines=False))
        elif path == "/api/connections":
            data = state
        elif path == "/api/dump-sources/save":
            value = route.request.post_data_json["source"]
            writes.append(copy.deepcopy(value))
            owner = next(c for c in connections if c["id"] == value["connection"])
            owner["backup"] = {**(owner["backup"] or dict(localPath="", restoreCommand="")), **value}
            state["pendingRestart"] = True
            data = dict(warnings=[], pendingRestart=True)
        elif path == "/api/dump-sources/delete":
            owner = next(c for c in connections if c["id"] == route.request.post_data_json["connection"])
            owner["backup"] = None
            data = dict(warnings=[], pendingRestart=True)
        elif path == "/api/sessions":
            data = dict(id="ui-redesign-session", expiresInSeconds=1800)
        elif path.startswith("/api/sessions/"):
            data = dict(transactions=runtime["transactions"], expiresInSeconds=1800, active=0)
        elif path == "/api/status":
            data = dict(state="success", transactions=runtime["transactions"], s3=dict(state="idle"), apply=dict(state=runtime["restore"], id="restore-job" if runtime["restore"] != "idle" else None, log="Restoring sample backup", context=dict(engine="library")))
        elif path == "/data/schema.json":
            data = dict(generatedAt="2026-11-23", sources=dict(library=tables, store=[{**tables[0], "name": "products"}]))
        elif path == "/data/programs.json":
            data = dict(engines={c["id"]: dict(programs=[], tableStats={}, tableImpacts={}) for c in connections})
            data["engines"]["store"]["programs"] = [scheduled, procedure, {**scheduled, "id": "store:event:paused_refresh", "name": "paused_refresh", "status": "DISABLED"}]
        elif path == "/api/sql":
            value = route.request.post_data_json
            writing = value["sql"].upper().startswith("UPDATE")
            if writing and " WHERE " not in value["sql"].upper() and not value.get("confirmed"):
                data = dict(needsConfirmation=True, confirmations=[dict(sql=value["sql"], reasons=["Vérifier la portée de la modification"])], results=[], txOpen=False)
            else:
                runtime["transactions"][value["engine"]] = writing
                data = dict(results=[dict(index=0, kind="write" if writing else "read", durationMs=2, **(dict(affected=1) if writing else dict(columns=["n"], rows=[[1]])))], txOpen=writing)
        elif path in ["/api/sql/commit", "/api/sql/rollback"]:
            runtime["transactions"][route.request.post_data_json["engine"]] = False
            data = dict(txOpen=False)
        elif path == "/api/apply":
            restores.append(route.request.post_data_json)
            runtime["restore"] = "running"
            data = dict(state="running", id="restore-job")
        elif path == "/api/s3/refresh":
            route.fulfill(status=500, json=dict(error="Source de sauvegarde indisponible (test)"))
            return
        elif path == "/api/jobs/restore-job/cancel":
            runtime["restore"] = "cancelled"
            runtime["cancelled"] = True
            data = dict(state="cancelled")
        elif path == "/api/data/rows":
            row_queries.append(route.request.post_data_json)
            data = dict(columns=["id", "name"], rows=[dict(id=1, name="Example")], hasMore=False, durationMs=2)
        elif path == "/api/data/refs":
            data = dict(refs=[])
        elif path == "/data/s3-dumps.json":
            data = dict(targets=[dict(engine="library", label="Bibliothèque", versions=[dict(versionId="sample-v1", lastModified="2026-11-20T10:00:00Z", size=1048576)])])
        elif path == "/data/snapshots/index.json":
            data = dict(versions=[dict(generatedAt="2026-11-20T10:00:00Z", origin="sample", sources=dict(library=dict(hash="sample-old", tables=2)), changed=["library"])])
        elif path == "/data/snapshots/library/sample-old.json":
            data = tables
        elif path == "/data/applied-dumps.json":
            data = {}
        else:
            route.fulfill(status=404, json=dict(error=f"Unmocked request: {path}"))
            return
        route.fulfill(json=data)

    try:
        with sync_playwright() as p:
            browser = p.firefox.launch(headless=True)
            context = browser.new_context(viewport=dict(width=1440, height=1000))
            context.route("**/api/**", respond)
            context.route("**/data/**", respond)
            page = context.new_page()
            page.on("pageerror", lambda error: failures.append(str(error)))
            page.goto(base)
            expect(page.get_by_role("heading", name="Vos bases de données")).to_be_visible()
            expect(page.locator(".connection-card")).to_have_count(2)
            page.screenshot(path=str(DEST / "home-desktop.png"))
            database_buttons = page.locator(".tree-engine-toggle")
            expect(database_buttons).to_have_count(2)
            expect(database_buttons.nth(0)).to_have_attribute("aria-expanded", "false")
            database_buttons.nth(0).click()
            expect(database_buttons.nth(0)).to_have_attribute("aria-expanded", "true")
            database_buttons.nth(1).click()
            expect(database_buttons.nth(0)).to_have_attribute("aria-expanded", "false")
            expect(database_buttons.nth(1)).to_have_attribute("aria-expanded", "true")
            page.locator(".schema-links a").first.click()
            expect(page.get_by_role("heading", name="public", exact=True)).to_be_visible()
            expect(page.locator(".sortable tbody tr")).to_have_count(2)
            page.locator("#schema-filter").fill("books")
            expect(page.locator(".sortable tbody tr")).to_have_count(1)
            page.locator("#schema-filter").fill("")
            page.locator(".sortable th").first.get_by_role("button").click()
            expect(page.locator(".sortable th").first).to_have_attribute("aria-sort", "descending")
            page.screenshot(path=str(DEST / "schema-desktop.png"))
            page.locator("#home-open").click()
            page.locator("#tree-filter").fill("Bibliothèque")
            expect(page.locator("#tree .tree-table")).to_have_count(2)
            page.locator("#tree-filter").fill("missing")
            expect(page.get_by_text("Aucun résultat pour")).to_be_visible()
            page.get_by_role("button", name="Effacer le filtre").click()
            page.get_by_role("button", name="Rechercher dans le catalogue").click()
            page.locator("#palette-input").fill("books")
            page.locator("#palette-input").press("Enter")
            expect(page.get_by_role("heading", name="books", exact=True)).to_be_visible()
            page.screenshot(path=str(DEST / "table-desktop.png"))
            page.get_by_role("link", name="Données", exact=True).click()
            expect(page.locator(".data-table tbody tr")).to_have_count(1)
            page.get_by_label("Filtrer name", exact=True).fill("Example")
            page.get_by_label("Filtrer name", exact=True).press("Enter")
            page.wait_for_timeout(150)
            assert row_queries[-1]["filters"]["name"] == "Example"
            page.get_by_role("button", name="Effacer les filtres").click()
            expect(page.get_by_label("Filtrer name", exact=True)).to_have_value("")
            page.locator(".data-table tbody tr").first.click()
            expect(page.get_by_role("region", name="Détail de la ligne")).to_be_visible()
            page.screenshot(path=str(DEST / "data-desktop.png"))
            page.get_by_role("button", name="Fermer le détail de la ligne").click()
            page.get_by_role("link", name="Relations", exact=True).click()
            expect(page.get_by_text("Cette table utilise")).to_be_visible()
            expect(page.locator(".relationship-card")).to_have_count(1)
            page.screenshot(path=str(DEST / "relations-desktop.png"))
            page.get_by_role("link", name="Carte des relations", exact=True).click()
            expect(page.locator(".neighbor-card")).to_have_count(1)
            page.screenshot(path=str(DEST / "map-desktop.png"))
            page.get_by_role("link", name="Programmes SQL", exact=True).click()
            expect(page.get_by_text("Aucun programme ne référence cette table dans le catalogue.")).to_be_visible()
            page.screenshot(path=str(DEST / "table-programs-desktop.png"))
            page.locator("#sql-open").click()
            expect(page.locator(".CodeMirror")).to_be_visible()
            page.locator("#sql-run").click()
            expect(page.locator("#sql-results tbody tr")).to_have_count(1)
            expect(page.locator(".history-entry")).to_have_count(1)
            page.screenshot(path=str(DEST / "sql-desktop.png"))
            page.locator(".connection-tabs a").filter(has_text="Boutique").click()
            page.locator(".CodeMirror").click()
            page.keyboard.press("Control+a")
            page.keyboard.insert_text("UPDATE products SET name = 'sample';")
            page.locator("#sql-run").click()
            expect(page.get_by_role("button", name="Confirmer l’exécution")).to_be_visible()
            assert not runtime["transactions"].get("store")
            page.get_by_role("button", name="Confirmer l’exécution").click()
            expect(page.locator("#sql-commit")).to_be_enabled()
            expect(page.get_by_text("Modifications en attente de validation")).to_be_visible()
            page.screenshot(path=str(DEST / "sql-transaction-desktop.png"))
            page.locator("#sql-rollback").click()
            expect(page.locator("#sql-commit")).to_be_disabled()
            page.locator("#events-toggle").click()
            expect(page.get_by_role("heading", name="Événements planifiés")).to_be_visible()
            page.get_by_role("combobox", name="Environnement analysé", exact=True).select_option("local")
            page.get_by_role("button", name="Catalogue des événements", exact=True).click()
            expect(page.locator(".events-table tbody tr")).to_have_count(2)
            page.get_by_role("combobox", name="État", exact=True).select_option("ENABLED")
            expect(page.locator(".events-table tbody tr")).to_have_count(1)
            page.screenshot(path=str(DEST / "events-desktop.png"))
            page.get_by_role("button", name="Inspecter daily_refresh", exact=True).click()
            expect(page.get_by_role("heading", name="daily_refresh", exact=True)).to_be_visible()
            page.screenshot(path=str(DEST / "program-code-desktop.png"))
            page.get_by_role("button", name="Source", exact=True).click()
            expect(page.get_by_text("events/daily_refresh.sql", exact=True)).to_be_visible()
            page.get_by_role("button", name="Dépendances", exact=True).click()
            expect(page.locator("#detail .dependency-link")).to_have_count(2)
            page.screenshot(path=str(DEST / "program-dependencies-desktop.png"))
            page.locator("#detail .dependency-link").filter(has_text="refresh_products").click()
            expect(page.get_by_role("heading", name="refresh_products", exact=True)).to_be_visible()
            page.get_by_role("button", name="Fermer la fiche", exact=True).click()
            page.get_by_role("searchbox", name="Rechercher un événement").fill("missing-event")
            expect(page.get_by_text("Aucun événement ne correspond")).to_be_visible()
            page.get_by_role("button", name="Réinitialiser les filtres").click()
            page.locator("#versions-toggle").click()
            maintenance = page.get_by_role("dialog", name="Versions des bases")
            expect(maintenance.get_by_role("button", name="Fermer", exact=True)).to_be_focused()
            page.keyboard.press("Shift+Tab")
            assert maintenance.evaluate("node => node.contains(document.activeElement)")
            page.keyboard.press("Tab")
            expect(maintenance.get_by_role("button", name="Fermer", exact=True)).to_be_focused()
            page.keyboard.press("Control+k")
            expect(page.locator("#palette")).not_to_be_visible()
            maintenance.locator("#maintenance-engine").select_option("library")
            maintenance.get_by_role("combobox", name="Version de library", exact=True).select_option("sample-old")
            expect(maintenance.get_by_role("button", name="Revenir au dernier export")).to_be_visible()
            maintenance.get_by_role("button", name="Revenir au dernier export").click()
            page.screenshot(path=str(DEST / "versions-desktop.png"))
            maintenance.get_by_role("button", name="Sauvegardes", exact=True).click()
            expect(maintenance.get_by_text("s3://sample-backups/library.sql.gz", exact=True)).to_be_visible()
            page.screenshot(path=str(DEST / "backups-desktop.png"))
            maintenance.get_by_role("button", name="Actualiser les sauvegardes", exact=True).click()
            expect(maintenance.get_by_role("alert")).to_contain_text("Source de sauvegarde indisponible (test)")
            maintenance.get_by_role("button", name="Fermer l’erreur d’opération", exact=True).click()
            maintenance.get_by_role("button", name="Restaurer", exact=True).click()
            confirm_input = maintenance.get_by_role("textbox", name="Saisis library pour confirmer")
            expect(confirm_input).to_be_focused()
            confirm_input.fill("store")
            expect(maintenance.get_by_role("button", name="Confirmer la restauration")).to_be_disabled()
            assert not restores
            page.keyboard.press("Escape")
            expect(maintenance).to_be_visible()
            maintenance.get_by_role("button", name="Restaurer", exact=True).click()
            confirm_input.fill("library")
            page.screenshot(path=str(DEST / "restore-confirm-desktop.png"))
            maintenance.get_by_role("button", name="Confirmer la restauration").click()
            expect(maintenance.get_by_role("heading", name="Opérations du serveur")).to_be_visible()
            assert restores == [dict(engine="library", versionId="sample-v1")]
            maintenance.get_by_role("button", name="Annuler l’opération").click()
            expect(maintenance.get_by_text("La base peut être partiellement restaurée.", exact=False)).to_be_visible()
            assert runtime["cancelled"]
            page.screenshot(path=str(DEST / "operations-desktop.png"))
            maintenance.get_by_role("button", name="Fermer", exact=True).click()
            expect(page.locator("#versions-toggle")).to_be_focused()
            page.locator("#connections-open").click()
            library = page.locator("#connection-library")
            store = page.locator("#connection-store")
            expect(library.get_by_text("s3://sample-backups/library.sql.gz", exact=True)).to_be_visible()
            expect(store.get_by_text("Aucune source associée à cette base")).to_be_visible()
            page.screenshot(path=str(DEST / "connections-desktop.png"))
            store.get_by_role("button", name="Associer une sauvegarde").click()
            form = page.get_by_role("form", name="Source de sauvegarde")
            expect(form.get_by_role("combobox", name="Connexion", exact=True)).to_be_disabled()
            expect(form.get_by_role("combobox", name="Connexion", exact=True)).to_have_value("store")
            form.get_by_label("Bucket").fill("store-backups")
            form.get_by_label("Clé de l’objet").fill("store.sql.gz")
            form.get_by_role("button", name="Enregistrer").click()
            expect(store.get_by_text("s3://store-backups/store.sql.gz", exact=True)).to_be_visible()
            assert writes[-1]["connection"] == "store"
            expect(library.get_by_text("s3://sample-backups/library.sql.gz", exact=True)).to_be_visible()
            library.get_by_role("button", name="Modifier la source").click()
            form.get_by_role("combobox", name="Type", exact=True).select_option("directory")
            form.get_by_label("Dossier", exact=True).fill("backups/library")
            form.get_by_label("Motif des fichiers").fill("*.gz")
            form.get_by_role("button", name="Enregistrer").click()
            expect(library.get_by_text("backups/library/*.gz", exact=True)).to_be_visible()
            assert writes[-1]["connection"] == "library"
            library.get_by_text("Détails de restauration", exact=True).click()
            expect(library.get_by_text("restore-library", exact=True)).to_be_visible()
            library.get_by_role("button", name="Modifier la base").click()
            expect(page.get_by_role("form", name="Connexion", exact=True)).to_be_focused()
            page.screenshot(path=str(DEST / "connection-edit-desktop.png"))
            page.get_by_role("form", name="Connexion", exact=True).get_by_role("button", name="Annuler").click()
            page.set_viewport_size(dict(width=390, height=844))
            page.screenshot(path=str(DEST / "connections-mobile.png"))
            assert page.evaluate("document.documentElement.scrollWidth <= innerWidth"), "Mobile page overflow"
            page.get_by_role("button", name="Ouvrir la navigation").click()
            expect(page.locator("#database-navigation")).to_be_visible()
            page.get_by_role("link", name="Vue d’ensemble", exact=True).first.click()
            expect(page.get_by_role("heading", name="Vos bases de données")).to_be_visible()
            expect(page.locator("#database-navigation")).not_to_be_visible()
            page.screenshot(path=str(DEST / "home-mobile.png"))
            assert page.evaluate("document.documentElement.scrollWidth <= innerWidth"), "Mobile home overflow"
            page.get_by_role("button", name="Rechercher dans le catalogue").click()
            page.locator("#palette-input").fill("no-such-table")
            expect(page.get_by_text("Aucun résultat. Essaie")).to_be_visible()
            page.keyboard.press("Escape")
            page.get_by_role("button", name="Ouvrir la navigation").click()
            page.keyboard.press("Escape")
            expect(page.locator("#database-navigation")).not_to_be_visible()
            page.goto(base + "/#/sql/library")
            expect(page.locator(".CodeMirror")).to_be_visible()
            assert page.locator("#main").evaluate("node => node.scrollTop") == 0
            page.screenshot(path=str(DEST / "sql-mobile.png"))
            assert page.evaluate("document.documentElement.scrollWidth <= innerWidth"), "Mobile SQL overflow"
            page.goto(base + "/#/events")
            page.get_by_role("combobox", name="Environnement analysé", exact=True).select_option("local")
            page.get_by_role("button", name="Catalogue des événements", exact=True).click()
            expect(page.get_by_role("heading", name="Événements planifiés")).to_be_visible()
            page.screenshot(path=str(DEST / "events-mobile.png"))
            page.get_by_role("button", name="Inspecter daily_refresh", exact=True).click()
            expect(page.get_by_role("heading", name="daily_refresh", exact=True)).to_be_visible()
            page.screenshot(path=str(DEST / "program-mobile.png"))
            page.get_by_role("button", name="Fermer la fiche", exact=True).click()
            page.get_by_role("button", name="Ouvrir la navigation").click()
            page.locator("#versions-toggle").click()
            expect(maintenance).to_be_visible()
            maintenance.get_by_role("button", name="Sauvegardes", exact=True).click()
            page.screenshot(path=str(DEST / "backups-mobile.png"))
            assert page.evaluate("document.documentElement.scrollWidth <= innerWidth"), "Mobile drawer overflow"
            maintenance.get_by_role("button", name="Fermer", exact=True).click()
            page.goto(base + "/#/store/public/products/donnees")
            expect(page.locator(".data-table tbody tr")).to_have_count(1)
            page.locator('td[title="Double-clic : modifier"]').filter(has_text="Example").dblclick()
            expect(page.locator("#cell-input")).to_be_visible()
            page.locator("#edit-cancel").click()
            page.locator(".data-table tbody tr").first.focus()
            page.keyboard.press("Enter")
            expect(page.get_by_role("region", name="Détail de la ligne")).to_be_visible()
            page.get_by_role("button", name="Modifier name", exact=True).click()
            expect(page.locator("#cell-input")).to_be_focused()
            page.locator("#cell-input").fill("Edited on mobile")
            page.get_by_role("button", name="Prévisualiser", exact=True).click()
            page.screenshot(path=str(DEST / "data-edit-mobile.png"))
            page.locator("#edit-run").click()
            expect(page.locator("#edit-run")).not_to_be_visible()
            expect(page.get_by_text("Transaction ouverte", exact=True)).to_be_visible()
            page.goto(base + "/#/library/public/books/voisinage")
            expect(page.locator(".selected-table-card")).to_be_visible()
            page.screenshot(path=str(DEST / "map-mobile.png"))
            assert page.evaluate("document.documentElement.scrollWidth <= innerWidth"), "Mobile relationship map overflow"
            assert not failures, failures
            page.close()
            dense = context.new_page()
            many_tables = [{**tables[0], "name": f"table_{index:04d}"} for index in range(500)]
            dense.route("**/data/schema.json?*", lambda route: route.fulfill(json=dict(generatedAt="2026-11-23", sources=dict(library=many_tables, store=[{**tables[0], "name": "products"}]))))
            dense.goto(base)
            dense.locator(".tree-engine-toggle").first.click()
            tree_body = dense.locator("#tree-database-library")
            expect(tree_body.locator(".tree-table")).to_have_count(500)
            assert tree_body.evaluate("node => node.scrollHeight > node.clientHeight")
            next_database = dense.locator(".tree-engine-toggle").nth(1)
            expect(next_database).to_be_visible()
            assert next_database.bounding_box()["y"] < 800, "Large database pushed the next database out of view"
            dense.screenshot(path=str(DEST / "navigation-many-tables.png"))
            next_database.click()
            expect(dense.locator(".tree-engine-toggle").first).to_have_attribute("aria-expanded", "false")
            dense.get_by_role("button", name="Rechercher dans le catalogue").click()
            dense.locator("#palette-input").fill("table_0499")
            dense.locator("#palette-input").press("Enter")
            expect(dense.get_by_role("heading", name="table_0499", exact=True)).to_be_visible()
            expect(dense.locator(".tree-engine-toggle").first).to_have_attribute("aria-expanded", "true")
            active_bounds = dense.locator(".tree-table.active").bounding_box()
            body_bounds = tree_body.bounding_box()
            assert body_bounds["y"] <= active_bounds["y"] < body_bounds["y"] + body_bounds["height"], "Active table is hidden in the large tree"
            browser.close()
        print("UX checks passed: desktop/mobile navigation, search, schema/relations, data editing, SQL confirmation/rollback/history, event filters, inspector dependencies, versions and restore confirmation/cancellation, backup ownership and keyboard focus.")
    finally:
        server.shutdown()
        server.server_close()


if __name__ == "__main__":
    main()
