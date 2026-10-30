#!/usr/bin/env python3
"""Render the README diagrams: docs/diagrams/*.html to docs/assets/*.png.

A page holds its English and Chinese text and declares its size in
<meta name="artboard" content="WIDTHxHEIGHT">. Headless Chrome renders it twice
at 2x, once per language. Run `make install` first so the dashboard's mono font
exists on disk.
"""
import argparse
import functools
import http.server
from pathlib import Path
import re
import shutil
import subprocess
import tempfile
import threading

ROOT = Path(__file__).resolve().parent.parent
SOURCES = ROOT / 'docs/diagrams'
OUTPUT = ROOT / 'docs/assets'
LANGUAGES = {'en': '', 'zh-CN': '.zh-CN'}


class Quiet(http.server.SimpleHTTPRequestHandler):
    def log_message(self, *_args):
        pass


def render(chrome, port, page, language):
    width, height = re.search(r'name="artboard" content="(\d+)x(\d+)"', page.read_text()).groups()
    target = OUTPUT / f'{page.stem}{LANGUAGES[language]}.png'
    with tempfile.TemporaryDirectory(prefix='mihomo-capture-chrome-') as profile:
        result = subprocess.run(
            [chrome, '--headless=new', '--no-first-run', '--no-default-browser-check',
             '--disable-background-networking', '--disable-component-update', '--disable-sync',
             '--hide-scrollbars', '--force-device-scale-factor=2', '--default-background-color=00000000',
             f'--window-size={width},{height}', '--virtual-time-budget=4000',
             f'--user-data-dir={profile}', f'--screenshot={target}',
             f'http://127.0.0.1:{port}/docs/diagrams/{page.name}?lang={language}'],
            capture_output=True, text=True, timeout=60)
    if result.returncode or not target.is_file():
        raise RuntimeError(result.stderr)
    print(f'Wrote {target.relative_to(ROOT)}', flush=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--chrome', default=shutil.which('google-chrome') or '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome')
    parser.add_argument('--only', help='render a single diagram by file name, such as traffic-lanes')
    args = parser.parse_args()
    if not (ROOT / 'web/node_modules/@fontsource/ibm-plex-mono').is_dir():
        parser.error('Install the dashboard dependencies first: make install')
    pages = sorted(p for p in SOURCES.glob('*.html') if args.only in (None, p.stem))
    if not pages:
        parser.error('no diagram matches')
    OUTPUT.mkdir(exist_ok=True)
    handler = functools.partial(Quiet, directory=str(ROOT))
    with http.server.ThreadingHTTPServer(('127.0.0.1', 0), handler) as server:
        threading.Thread(target=server.serve_forever, daemon=True).start()
        try:
            for page in pages:
                for language in LANGUAGES:
                    render(args.chrome, server.server_port, page, language)
        finally:
            server.shutdown()


if __name__ == '__main__':
    main()
