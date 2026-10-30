#!/usr/bin/env python3
"""Serve the built dashboard with synthetic, read-only API data.

No mihomo connection, capture service, CA, or system proxy is used.
"""
import argparse
import functools
import http.server
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import threading
import urllib.parse

ROOT = Path(__file__).resolve().parent.parent
FIXTURE = json.loads((ROOT / 'docs/demo/traffic.json').read_text())


class Preview(http.server.SimpleHTTPRequestHandler):
    def log_message(self, *_args):
        pass

    def send_json(self, value):
        payload = json.dumps(value, ensure_ascii=False).encode()
        self.send_response(200)
        self.send_header('Content-Type', 'application/json; charset=utf-8')
        self.send_header('Cache-Control', 'no-store')
        self.send_header('Content-Length', str(len(payload)))
        self.end_headers()
        self.wfile.write(payload)

    def do_GET(self):
        url = urllib.parse.urlsplit(self.path)
        path = url.path
        if path == '/v1/status':
            return self.send_json(FIXTURE['status'])
        if path == '/v1/devices':
            return self.send_json({'items': FIXTURE['devices']})
        if path == '/v1/modules':
            return self.send_json({'items': []})
        if path == '/v1/records':
            items = FIXTURE['records']
            query = urllib.parse.parse_qs(url.query)
            for key in ('host', 'path', 'method', 'status', 'device', 'q'):
                if key not in query:
                    continue
                value = query[key][0].lower()
                field = 'device_id' if key == 'device' else key
                items = [r for r in items if value in (json.dumps(r).lower() if key == 'q' else str(r.get(field, '')).lower())]
            summaries = [{k: v for k, v in r.items() if k not in ('request', 'response', 'frames', 'curl')} for r in items]
            for summary, record in zip(summaries, items):
                if record.get('frames'):
                    summary['frames'] = len(record['frames'])
            return self.send_json({'items': summaries, 'next': ''})
        if path.startswith('/v1/records/'):
            for record in FIXTURE['records']:
                if record['id'] == path.rsplit('/', 1)[1]:
                    return self.send_json(record)
            return self.send_error(404)
        if path.startswith('/v1/'):
            return self.send_error(404)
        if path in ('/', '/devices', '/certificate', '/system', '/modules'):
            html = (ROOT / 'web/dist/index.html').read_text()
            # Only this isolated preview gets an inert token and a fixed clock.
            bootstrap = '''<script>
localStorage.setItem('mihomo-capture.token', 'docs-demo');
const NativeDate = Date;
window.Date = class extends NativeDate {
 constructor(...args) { super(...(args.length ? args : ['2026-10-29T12:34:56Z'])); }
 static now() { return new NativeDate('2026-10-29T12:34:56Z').getTime(); }
};
</script><style>*{animation:none!important;caret-color:transparent!important}</style>'''
            label = '<div style="position:fixed;bottom:14px;right:18px;z-index:9999;background:#17202b;color:white;border-radius:6px;padding:7px 12px;font:11px system-ui;letter-spacing:.08em;pointer-events:none">DEMO / SYNTHETIC TRAFFIC</div>'
            html = html.replace('<head>', '<head>' + bootstrap).replace('</body>', label + '</body>')
            payload = html.encode()
            self.send_response(200)
            self.send_header('Content-Type', 'text/html; charset=utf-8')
            self.send_header('Content-Length', str(len(payload)))
            self.end_headers()
            self.wfile.write(payload)
            return
        super().do_GET()


def screenshots(server, chrome):
    output = ROOT / 'docs/assets'
    output.mkdir(exist_ok=True)
    for filename, record in [('dashboard-http.png', '00000000000000000000000000000001'),
                             ('dashboard-websocket.png', '00000000000000000000000000000002')]:
        with tempfile.TemporaryDirectory(prefix='mihomo-capture-chrome-') as profile:
            command = [chrome, '--headless=new', '--no-first-run', '--no-default-browser-check',
                       '--disable-background-networking', '--disable-component-update', '--disable-sync',
                       '--hide-scrollbars', '--force-device-scale-factor=2', '--window-size=1440,960',
                       '--virtual-time-budget=5000', f'--user-data-dir={profile}',
                       f'--screenshot={output / filename}',
                       f'http://127.0.0.1:{server.server_port}/?r={record}']
            result = subprocess.run(command, capture_output=True, text=True, timeout=45,
                                    env={**os.environ, 'TZ': 'UTC'})
            if result.returncode or not (output / filename).is_file():
                raise RuntimeError(result.stderr)
            print(f'Wrote docs/assets/{filename}', flush=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--port', type=int, default=8088)
    parser.add_argument('--screenshots', action='store_true')
    parser.add_argument('--chrome', default=shutil.which('google-chrome') or '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome')
    args = parser.parse_args()
    if not (ROOT / 'web/dist/index.html').is_file():
        parser.error('Build the dashboard first: make install && make web')
    handler = functools.partial(Preview, directory=str(ROOT / 'web/dist'))
    with http.server.ThreadingHTTPServer(('127.0.0.1', 0 if args.screenshots else args.port), handler) as server:
        if args.screenshots:
            thread = threading.Thread(target=server.serve_forever, daemon=True)
            thread.start()
            try:
                screenshots(server, args.chrome)
            finally:
                server.shutdown()
                thread.join()
        else:
            print(f'Read-only mock dashboard: http://127.0.0.1:{server.server_port}', flush=True)
            try:
                server.serve_forever()
            except KeyboardInterrupt:
                pass


if __name__ == '__main__':
    main()
