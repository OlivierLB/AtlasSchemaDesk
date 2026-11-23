"""Event window UI checks with synthetic schedules and no database access."""
import datetime as dt
import functools
import threading
from http.server import ThreadingHTTPServer
from pathlib import Path
from urllib.parse import urlparse

from playwright.sync_api import sync_playwright, expect
from ui_redesign import QuietHandler, ROOT

DEST = ROOT / ".local/test-results/event-window"


def main():
    DEST.mkdir(parents=True, exist_ok=True)
    server = ThreadingHTTPServer(("127.0.0.1", 0), functools.partial(QuietHandler, directory=str(ROOT / "web/dist/app")))
    threading.Thread(target=server.serve_forever, daemon=True).start()
    base = f"http://127.0.0.1:{server.server_port}"
    queries, errors = [], []
    connection = dict(id="store", label="Sample MySQL", driver="mysql", default_schema="public", color="#79b8ff", allow_writes=False)
    def event(name, impact, **extra):
        return dict(id=f"store:event:{name}", name=name, kind="event", schema="public", body="SELECT 1;", status="ENABLED", schedule="toutes les 15 MINUTE", impact=impact, **extra)
    programs = [event("refresh_products", dict(writes=["public.products"])), event("report_sales", dict(reads=["public.products"])), event("unknown_schedule", dict(dynamicSql=True)), {**event("paused", {}), "status": "DISABLED"}]
    programs[0]["impact"]["calls"] = ["store:procedure:refresh_helper"]
    helper = dict(id="store:procedure:refresh_helper", kind="procedure", schema="public", name="refresh_helper", body="UPDATE products SET quantity = 0;", impact=dict(writes=["public.products"]))

    def respond(route):
        path = urlparse(route.request.url).path
        if path == "/api/config": data = dict(connections=[connection], modules=dict(dumps=False, routines=False))
        elif path == "/api/sessions": data = dict(id="event-window-session", expiresInSeconds=1800)
        elif path == "/api/sessions/current": data = dict(transactions={}, expiresInSeconds=1800, active=0)
        elif path == "/api/status": data = dict(state="success", transactions={}, s3=dict(state="idle"), apply=dict(state="idle"))
        elif path == "/data/schema.json": data = dict(generatedAt="2026-11-23T09:00:00Z", sources=dict(store=[dict(schema="public", name="products", columns=[dict(name="id", type="integer", primary=True)], foreignKeys=[])]))
        elif path == "/data/programs.json": data = dict(generatedAt="2026-11-23T09:00:00Z", engines=dict(store=dict(programs=[], tableStats={}, tableImpacts={})))
        elif path == "/api/events/reference": data = dict(generatedAt="2026-11-23T09:00:00Z", engines=dict(store=dict(programs=[*programs, helper], tableStats={}, tableImpacts={})), warnings=[], filesRead=4)
        elif path == "/api/events/window":
            value = route.request.post_data_json
            queries.append(value)
            start = dt.datetime.fromisoformat(value["from"].replace("Z", "+00:00"))
            end = dt.datetime.fromisoformat(value["to"].replace("Z", "+00:00"))
            minutes = int((end - start).total_seconds() // 60)
            iso = lambda value: value.isoformat().replace("+00:00", "Z")
            events = [dict(engine="store", programId=programs[0]["id"], timeZone="UTC", scheduler="ON", starts=max(1, minutes // 15), firstAt=iso(start), lastAt=iso(end-dt.timedelta(minutes=min(15, minutes))), previousAt=iso(start-dt.timedelta(minutes=15)) if value["lookbackMinutes"] >= 15 else None), dict(engine="store", programId=programs[1]["id"], timeZone="UTC", scheduler="ON", starts=max(1, minutes // 60), firstAt=iso(start), lastAt=iso(start), previousAt=None)]
            step = dt.timedelta(minutes=max(1, (minutes+23)//24))
            at, buckets = start, []
            while at < end:
                until = min(end, at+step)
                buckets.append(dict(**{"from":iso(at), "to":iso(until)}, counts={programs[0]["id"]:1, programs[1]["id"]:1}))
                at = until
            data = dict(generatedAt="2026-11-23T09:00:00Z", **{"from":value["from"],"to":value["to"]}, lookbackMinutes=value["lookbackMinutes"], events=events, unknown=[dict(engine="store", programId=programs[2]["id"], reason="Horaires structurés absents : actualise le catalogue")], excluded=[dict(engine="store", programId=programs[3]["id"], reason="Désactivé au dernier export")], buckets=buckets)
        else:
            route.fulfill(status=404, json=dict(error="Unused fixture endpoint"))
            return
        route.fulfill(json=data)

    try:
        with sync_playwright() as p:
            browser = p.firefox.launch(headless=True)
            context = browser.new_context(viewport=dict(width=1440, height=1100), timezone_id="Europe/Paris", locale="fr-FR")
            context.route("**/api/**", respond)
            context.route("**/data/**", respond)
            page = context.new_page()
            page.on("pageerror", lambda error: errors.append(str(error)))
            page.goto(base + "/#/events")
            page.get_by_label("Jour à analyser", exact=True).fill("2026-11-23")
            page.get_by_label("Heure de début", exact=True).fill("10:00")
            page.get_by_label("Heure de fin", exact=True).fill("11:00")
            page.get_by_role("button", name="Analyser le créneau", exact=True).click()
            expect(page.locator(".window-events tbody tr")).to_have_count(2)
            assert queries[-1]["source"] == "reference"
            assert queries[-1]["from"].startswith("2026-11-23T09:00:00")
            assert queries[-1]["to"].startswith("2026-11-23T10:00:00")
            expect(page.get_by_text("Durée d’exécution inconnue", exact=True)).to_be_visible()
            expect(page.locator(".window-shared-row")).to_have_count(1)
            expect(page.get_by_text("Horaires structurés absents : actualise le catalogue", exact=True)).to_be_visible()
            page.get_by_role("button", name="Catalogue des événements", exact=True).click()
            expect(page.locator(".events-table tbody tr")).to_have_count(4)
            page.get_by_role("searchbox", name="Rechercher un événement").fill("report_sales")
            expect(page.locator(".events-table tbody tr")).to_have_count(1)
            page.get_by_role("button", name="Analyse par créneau", exact=True).click()
            expect(page.get_by_label("Heure de début", exact=True)).to_have_value("10:00")
            expect(page.locator(".window-events tbody tr")).to_have_count(2)
            page.screenshot(path=str(DEST / "window-desktop.png"))
            page.get_by_role("button", name="Examiner refresh_products", exact=True).click()
            expect(page.get_by_role("heading", name="refresh_products", exact=True)).to_be_visible()
            page.get_by_role("button", name="Dépendances", exact=True).click()
            page.get_by_role("button", name="public.refresh_helper", exact=True).click()
            expect(page.get_by_role("heading", name="refresh_helper", exact=True)).to_be_visible()
            page.get_by_role("button", name="Fermer la fiche", exact=True).click()
            page.get_by_role("combobox", name="Inclure les lancements précédents", exact=True).select_option("0")
            page.get_by_role("button", name="Analyser le créneau", exact=True).click()
            expect(page.get_by_text("Durée d’exécution inconnue", exact=True)).not_to_be_visible()
            assert queries[-1]["lookbackMinutes"] == 0
            page.get_by_role("checkbox", name="Toute la journée").check()
            page.get_by_role("button", name="Analyser le créneau", exact=True).click()
            expect(page.locator(".window-bars button")).to_have_count(24)
            assert queries[-1]["from"].startswith("2026-11-22T23:00:00")
            page.locator(".window-bars button").nth(10).click()
            expect(page.get_by_role("checkbox", name="Toute la journée")).not_to_be_checked()
            page.wait_for_timeout(100)
            assert len(queries) >= 4
            page.set_viewport_size(dict(width=390, height=844))
            page.locator("#main").evaluate("node => node.scrollTop=0")
            page.screenshot(path=str(DEST / "window-mobile.png"))
            assert page.evaluate("document.documentElement.scrollWidth <= innerWidth"), "Mobile event window overflow"
            page.locator(".window-results").scroll_into_view_if_needed()
            page.screenshot(path=str(DEST / "window-results-mobile.png"))
            assert not errors, errors
            browser.close()
        print("Event window UI passed: local time conversion, date/hour/day selection, prior starts, shared tables, unknown schedules, inspector, timeline drill-down and mobile layout.")
    finally:
        server.shutdown()
        server.server_close()


if __name__ == "__main__": main()
