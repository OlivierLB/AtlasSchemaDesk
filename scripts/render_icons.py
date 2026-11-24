"""Regenerates the application icons from packaging/icon.svg.

Writes packaging/icon-source.png (1024 px), packaging/icon.png (512 px, Linux
menu and window) and packaging/windows/icon.ico (Windows executable and
installer). The SVG is rendered by Firefox (Playwright, as in the UI tests) on
black and on white; the difference gives the transparency of the rounded
corners. Needs ImageMagick (`convert`). Rerunnable:

    .venv/bin/python scripts/render_icons.py
"""

import base64
import pathlib
import subprocess
import tempfile

from playwright.sync_api import sync_playwright

ROOT = pathlib.Path(__file__).resolve().parent.parent
SVG = ROOT / "packaging/icon.svg"
SIZE = 1024


def render(page, background: str, out: pathlib.Path) -> None:
    data = base64.b64encode(SVG.read_bytes()).decode()
    page.set_content(
        f'<html><body style="margin:0;background:{background}">'
        f'<img src="data:image/svg+xml;base64,{data}" width="{SIZE}" height="{SIZE}" style="display:block">'
        "</body></html>"
    )
    page.wait_for_timeout(200)
    page.screenshot(path=str(out), clip={"x": 0, "y": 0, "width": SIZE, "height": SIZE})


def main() -> None:
    with tempfile.TemporaryDirectory(prefix="sulcata-icons-") as temporary:
        work = pathlib.Path(temporary)
        with sync_playwright() as playwright:
            browser = playwright.firefox.launch()
            page = browser.new_page(viewport={"width": SIZE, "height": SIZE})
            render(page, "white", work / "white.png")
            render(page, "black", work / "black.png")
            browser.close()
        convert = ["convert"]
        subprocess.run(convert + [work / "white.png", work / "black.png", "-compose", "difference", "-composite",
                                  "-colorspace", "gray", "-negate", work / "alpha.png"], check=True)
        source = ROOT / "packaging/icon-source.png"
        subprocess.run(convert + [work / "black.png", work / "alpha.png", "-alpha", "off", "-compose", "copy_opacity",
                                  "-composite", "-define", "png:compression-level=9", source], check=True)
    subprocess.run(["convert", source, "-resize", "512x512", "-define", "png:compression-level=9",
                    ROOT / "packaging/icon.png"], check=True)
    subprocess.run(["convert", source, "-define", "icon:auto-resize=256,128,64,48,32,16",
                    ROOT / "packaging/windows/icon.ico"], check=True)
    print("Icônes régénérées depuis", SVG.relative_to(ROOT))


if __name__ == "__main__":
    main()
