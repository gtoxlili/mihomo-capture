#!/usr/bin/env python3
"""Serve the built dashboard with synthetic, read-only API data and take the README screenshots.

The data is docs/demo/traffic.json. No mihomo connection, capture service, CA, or system proxy is used.

  python3 scripts/docs_preview.py                        browse the dashboard at http://127.0.0.1:8088
  python3 scripts/docs_preview.py --screenshots          write the README screenshots to docs/assets/
  python3 scripts/docs_preview.py --screenshots --only dashboard-modules

The preview opens on the capture of Demo iPhone; add ?scene=daily to any page for daily mode, with nothing
captured. /_frame?src=/modules&w=924&scene=daily shows a page in the README's frame.
"""
import argparse
import base64
import copy
import functools
import http.cookies
import http.server
import json
import os
from pathlib import Path
import re
import shutil
import struct
import subprocess
import tempfile
import threading
import time
import urllib.parse

ROOT = Path(__file__).resolve().parent.parent
FIXTURE = json.loads((ROOT / 'docs/demo/traffic.json').read_text())
FIXED_CLOCK = '2026-10-29T12:34:56Z'

# One fixture, two moments. "capture" is Demo iPhone's recorded session; "daily" is the same service with
# nothing being captured. A page picks one with ?scene=, and the cookie it sets carries the choice to the
# API calls the page makes.
SCENES = ('capture', 'daily')


def scene_status(scene):
    status = copy.deepcopy(FIXTURE['status'])
    if scene == 'daily':
        status['capture'] = None
        status['gateway']['rules'] = FIXTURE['daily']['gateway_rules']
    return status


def scene_devices(scene):
    devices = copy.deepcopy(FIXTURE['devices'])
    capture = scene_status(scene)['capture']
    for device in devices:
        device['capturing'] = bool(capture) and device['id'] == capture['device_id']
    return devices


def module_summary(m):
    lists = ('header_rewrites', 'url_rewrites', 'map_locals', 'body_rewrites', 'scripts')
    return {'id': m['id'], 'name': m['name'], 'enabled': m['enabled'], 'devices': m['devices'],
            'hostnames': m['hostnames'], 'rules': {k: len(m[k]) for k in lists}, 'updated_at': m['updated_at']}


def record_summary(r):
    summary = {k: v for k, v in r.items() if k not in ('request', 'response', 'frames', 'curl')}
    if r.get('frames'):
        summary['frames'] = len(r['frames'])
    return summary


class Preview(http.server.SimpleHTTPRequestHandler):
    def log_message(self, *_args):
        pass

    def send_bytes(self, payload, content_type, status=200, headers=()):
        self.send_response(status)
        self.send_header('Content-Type', content_type)
        self.send_header('Cache-Control', 'no-store')
        self.send_header('Content-Length', str(len(payload)))
        for name, value in headers:
            self.send_header(name, value)
        self.end_headers()
        self.wfile.write(payload)

    def send_json(self, value, status=200):
        self.send_bytes(json.dumps(value, ensure_ascii=False).encode(), 'application/json; charset=utf-8', status)

    def scene(self):
        jar = http.cookies.SimpleCookie(self.headers.get('Cookie', ''))
        scene = jar['scene'].value if 'scene' in jar else 'capture'
        return scene if scene in SCENES else 'capture'

    def do_GET(self):
        url = urllib.parse.urlsplit(self.path)
        query = urllib.parse.parse_qs(url.query)
        path = url.path
        if path.startswith('/v1/'):
            return self.api(path, query)
        if path == '/_frame':
            return self.send_bytes(frame_page(query).encode(), 'text/html; charset=utf-8')
        if path not in ('/', '/index.html') and os.path.isfile(self.translate_path(path)):
            return super().do_GET()
        # Every other path is a dashboard route.
        headers = []
        if query.get('scene', [''])[0] in SCENES:
            headers.append(('Set-Cookie', 'scene=%s; Path=/; SameSite=Lax' % query['scene'][0]))
        self.send_bytes(dashboard_page().encode(), 'text/html; charset=utf-8', headers=headers)

    def readonly(self):
        self.send_json({'error': {'code': 'method_not_allowed', 'message': 'The documentation preview is read-only'}}, 405)

    do_POST = do_PUT = do_PATCH = do_DELETE = readonly

    def api(self, path, query):
        scene = self.scene()
        if path == '/v1/status':
            return self.send_json(scene_status(scene))
        if path == '/v1/devices':
            return self.send_json({'items': scene_devices(scene)})
        if path == '/v1/modules':
            return self.send_json({'items': [module_summary(m) for m in FIXTURE['modules']]})
        m = re.fullmatch(r'/v1/modules/([0-9a-f]+)(/logs)?', path)
        if m:
            module = next((x for x in FIXTURE['modules'] if x['id'] == m.group(1)), None)
            if module:
                return self.send_json({'items': FIXTURE['module_logs'][module['id']]} if m.group(2) else module)
        if path == '/v1/records':
            items = FIXTURE['records']
            for key in ('host', 'path', 'method', 'status', 'device', 'q'):
                if key not in query:
                    continue
                value = query[key][0].lower()
                if key == 'q':
                    items = [r for r in items if value in json.dumps(r, ensure_ascii=False).lower()]
                elif key == 'status':
                    items = [r for r in items if value == str(r['status'])]
                elif key == 'device':
                    items = [r for r in items if value == r['device_id']]
                else:
                    items = [r for r in items if value in str(r[key]).lower()]
            return self.send_json({'items': [record_summary(r) for r in items], 'next': ''})
        m = re.fullmatch(r'/v1/records/([0-9a-f]+)', path)
        if m:
            record = next((r for r in FIXTURE['records'] if r['id'] == m.group(1)), None)
            if record:
                return self.send_json(record)
        self.send_json({'error': {'code': 'not_found', 'message': 'no such resource'}}, 404)


def dashboard_page():
    page = (ROOT / 'web/dist/index.html').read_text()
    # Only this isolated preview gets an inert token, a fixed clock and still frames.
    bootstrap = '''<script>
localStorage.setItem('mihomo-capture.token', 'docs-demo');
const NativeDate = Date;
window.Date = class extends NativeDate {
 constructor(...args) { super(...(args.length ? args : ['%s'])); }
 static now() { return new NativeDate('%s').getTime(); }
};
</script><style>*{animation:none!important;transition:none!important;caret-color:transparent!important}</style>''' % (FIXED_CLOCK, FIXED_CLOCK)
    label = ('<div style="position:fixed;bottom:14px;right:18px;z-index:9999;background:#17202b;color:white;border-radius:6px;'
             'padding:7px 12px;font:11px system-ui;letter-spacing:.08em;pointer-events:none">DEMO / SYNTHETIC TRAFFIC</div>')
    return page.replace('<head>', '<head>' + bootstrap).replace('</body>', label + '</body>')


# The README frame: a dotted panel with the dashboard in a rounded window. The panel is the window plus 40 px
# on every side, and the page around it is transparent, so a screenshot of the frame has transparent corners.
FRAME = '''<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>Dashboard frame</title>
<style>
html, body { margin: 0; background: transparent; }
.panel {
  box-sizing: border-box; width: calc(var(--w) + 80px); padding: 40px; border-radius: 20px;
  background-color: #eceff4;
  background-image: radial-gradient(circle, rgba(16,24,40,.13) 1px, transparent 1.4px);
  background-size: 20px 20px;
}
.window {
  width: var(--w); height: var(--h); border-radius: 12px; overflow: hidden; background: #fff;
  box-shadow: 0 0 0 1px rgba(16,24,40,.10), 0 16px 40px -8px rgba(16,24,40,.18), 0 2px 6px rgba(16,24,40,.06);
}
iframe { display: block; width: 100%; height: 100%; border: 0; }
</style></head>
<body style="--w: __W__px; --h: __H__px">
<div class="panel"><div class="window"><iframe src="__SRC__" title="Dashboard"></iframe></div></div>
</body></html>
'''


def frame_page(query):
    def number(name, default, low, high):
        try:
            return min(max(int(query.get(name, [default])[0]), low), high)
        except ValueError:
            return default
    src = query.get('src', ['/'])[0]
    if not src.startswith('/') or src.startswith('//'):
        src = '/'
    if 'scene' in query:
        src += ('&' if '?' in src else '?') + urllib.parse.urlencode({'scene': query['scene'][0]})
    return (FRAME.replace('__W__', str(number('w', 1280, 320, 4000))).replace('__H__', str(number('h', 800, 200, 6000)))
            .replace('__SRC__', src.replace('&', '&amp;').replace('"', '&quot;')))


# ---------------------------------------------------------------------------
# Screenshots: headless Chrome driven over the DevTools pipe, standard library only.

class Chrome:
    """Chrome with --remote-debugging-pipe: JSON messages end with a NUL byte, on fd 3 (in) and fd 4 (out)."""

    def __init__(self, binary, profile):
        import fcntl
        to_r, to_w = os.pipe()
        from_r, from_w = os.pipe()
        # The shell moves the child's ends of the pipes to fd 3 and 4, where Chrome expects them.
        child = [fcntl.fcntl(fd, fcntl.F_DUPFD, 10) for fd in (to_r, from_w)]
        os.close(to_r)
        os.close(from_w)
        self.log = open(os.path.join(profile, 'chrome.log'), 'wb')
        command = [binary, '--headless=new', '--remote-debugging-pipe', '--no-first-run', '--no-default-browser-check',
                   '--disable-background-networking', '--disable-component-update', '--disable-sync',
                   '--hide-scrollbars', '--force-color-profile=srgb', '--font-render-hinting=none', '--lang=zh-CN',
                   '--user-data-dir=' + os.path.join(profile, 'data'), 'about:blank']
        env = {**os.environ, 'TZ': 'UTC', 'FD_IN': str(child[0]), 'FD_OUT': str(child[1])}
        try:
            self.proc = subprocess.Popen(['/bin/sh', '-c', 'exec "$@" 3<&$FD_IN 4>&$FD_OUT', 'sh'] + command,
                                         pass_fds=child, stdin=subprocess.DEVNULL, stdout=self.log, stderr=self.log, env=env)
        finally:
            for fd in child:
                os.close(fd)
        self.out, self.inp = from_r, to_w
        self.pending = b''
        self.last = 0

    def send(self, method, params, session):
        self.last += 1
        message = {'id': self.last, 'method': method, 'params': params or {}}
        if session:
            message['sessionId'] = session
        os.write(self.inp, json.dumps(message).encode() + b'\0')
        return self.last

    def receive(self, deadline):
        import select
        while b'\0' not in self.pending:
            left = deadline - time.monotonic()
            if left <= 0:
                raise TimeoutError('Chrome did not answer in time')
            if select.select([self.out], [], [], left)[0]:
                chunk = os.read(self.out, 1 << 20)
                if not chunk:
                    raise RuntimeError('Chrome exited')
                self.pending += chunk
        raw, self.pending = self.pending.split(b'\0', 1)
        return json.loads(raw)

    def call(self, method, params=None, session=None, timeout=30):
        number = self.send(method, params, session)
        deadline = time.monotonic() + timeout
        while True:
            message = self.receive(deadline)
            if message.get('id') == number:
                if 'error' in message:
                    raise RuntimeError('%s: %s' % (method, message['error'].get('message')))
                return message.get('result', {})

    def errors(self):
        """What Chrome complained about, without the updater's chatter."""
        self.log.flush()
        with open(self.log.name, 'rb') as f:
            lines = f.read().decode(errors='replace').splitlines()
        errors = [line for line in lines if 'ERROR' in line and 'crashpad' not in line.lower()]
        return '\n'.join(errors[-10:])

    def close(self):
        try:
            self.call('Browser.close', timeout=5)
        except Exception:
            pass
        try:
            self.proc.wait(5)
        except subprocess.TimeoutExpired:
            self.proc.kill()
            self.proc.wait()
        for fd in (self.inp, self.out):
            os.close(fd)
        self.log.close()


PANEL = 80  # the panel is 40 px wider than the window on each side
BADGE = 28  # room under a page's content for the demo badge


class Page:
    """One tab in its own browser context, so no cookie or storage outlives its screenshot."""

    def __init__(self, chrome, width, height):
        self.chrome = chrome
        self.context = chrome.call('Target.createBrowserContext')['browserContextId']
        self.target = chrome.call('Target.createTarget', {'url': 'about:blank', 'browserContextId': self.context})['targetId']
        self.session = chrome.call('Target.attachToTarget', {'targetId': self.target, 'flatten': True})['sessionId']
        self.call('Page.enable')
        self.call('Emulation.setTimezoneOverride', {'timezoneId': 'UTC'})
        self.call('Emulation.setDefaultBackgroundColorOverride', {'color': {'r': 0, 'g': 0, 'b': 0, 'a': 0}})
        self.resize(width, height)

    def call(self, method, params=None, timeout=30):
        return self.chrome.call(method, params, self.session, timeout)

    def resize(self, width, height):
        self.call('Emulation.setDeviceMetricsOverride',
                  {'width': width + PANEL, 'height': height + PANEL, 'deviceScaleFactor': 2, 'mobile': False})

    def evaluate(self, expression, wait=False):
        result = self.call('Runtime.evaluate', {'expression': expression, 'returnByValue': True, 'awaitPromise': wait})
        if 'exceptionDetails' in result:
            detail = result['exceptionDetails']
            raise RuntimeError('page script failed: %s' % (detail.get('exception', {}).get('description') or detail.get('text')))
        return result['result'].get('value')

    def close(self):
        for method, params in (('Target.closeTarget', {'targetId': self.target}),
                               ('Target.disposeBrowserContext', {'browserContextId': self.context})):
            try:
                self.chrome.call(method, params, timeout=5)
            except Exception:
                pass


# JavaScript that runs in the frame page. `d` is the dashboard's document, `text` its visible text.
DOC = "(document.querySelector('iframe') || {}).contentDocument"

# '' once the page is complete, else what it still waits for. The dashboard shows placeholders (an empty status
# band, "正在读取…", a code block waiting for its lazy chunk) until its data has arrived.
PROBE = '''(() => {
  const d = %s;
  if (!d || d.readyState !== 'complete' || !d.getElementById('root')) return 'document';
  if (!d.querySelector('header + section')) return 'status band';
  const text = d.body.innerText;
  if (text.includes('正在读取')) return 'loading text';
  if (d.querySelector('pre.h-24') || d.querySelector('textarea[readonly][aria-label="脚本代码"]')) return 'code view';
  if (d.fonts.status !== 'loaded') return 'fonts';
  return (%%s) ? '' : 'page content';
})()''' % DOC

LOAD_FONTS = '''Promise.all(['400', '500', '600'].map((w) => %s.fonts.load(w + ' 12px "IBM Plex Mono"'))).then(() => %s.fonts.ready).then(() => true)''' % (DOC, DOC)

# A fingerprint of the layout, to see that nothing is still moving.
LAYOUT = '''(() => { const d = %s; return [d.body.innerHTML.length, d.documentElement.scrollHeight, d.body.innerText.length].join(); })()''' % DOC

# The height a page needs: everything above <main>, plus the content in it.
CONTENT_HEIGHT = '''(() => {
  const main = %s.querySelector('main'); main.scrollTop = 0;
  return Math.ceil(main.getBoundingClientRect().top + main.firstElementChild.getBoundingClientRect().height);
})()''' % DOC

# Pixels of the next row that show below the record list's last whole row.
LIST_SLACK = '''(() => {
  const list = %s.querySelector('[aria-label^="请求记录"]'); list.scrollTop = 0;
  const top = list.getBoundingClientRect().top; let edge = 0;
  for (const row of list.querySelectorAll('button')) {
    const bottom = Math.ceil(row.getBoundingClientRect().bottom - top);
    if (bottom <= list.clientHeight) edge = Math.max(edge, bottom);
  }
  return list.clientHeight - edge;
})()''' % DOC

SET_HEIGHT = '''(() => { document.body.style.setProperty('--h', %d + 'px'); return new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r))); })()'''

# Nothing is focused or selected when the picture is taken.
SETTLE = '''(() => { const d = %s; d.activeElement && d.activeElement.blur(); d.getSelection().removeAllRanges(); return new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r))); })()''' % DOC


class Shot:
    """One README image. height=None takes the page's own height. A number is the height of a page that fills
    the window; with snap, the record list is shortened to end on a whole row. pad adds room under the content
    for the demo badge. after clicks something once the page is ready, after_ready says when that is done, and
    verify checks the result: it fails the shot when content was cut off."""

    def __init__(self, name, scene, path, width, ready, height=None, snap=False, pad=0, after=None, after_ready=None,
                 verify=None, problem='something does not fit'):
        self.name, self.scene, self.path, self.width, self.ready = name, scene, path, width, ready
        self.height, self.snap, self.pad, self.after, self.after_ready = height, snap, pad, after, after_ready
        self.verify, self.problem = verify, problem  # verify is a JS expression, true when the picture is whole


def js(value):
    return json.dumps(value, ensure_ascii=False)


def record_id(**match):
    return next(r['id'] for r in FIXTURE['records'] if all(r.get(k) == v for k, v in match.items()))


def module_id(name):
    return next(m['id'] for m in FIXTURE['modules'] if m['name'] == name)


def shots():
    capture = FIXTURE['status']['capture']
    session = sum(1 for r in FIXTURE['records'] if r['session'] == capture['generation'])
    device = next(d['label'] for d in FIXTURE['devices'] if d['id'] == capture['device_id'])
    frames = len(next(r for r in FIXTURE['records'] if r['status'] == 101)['frames'])
    staging = module_id('Staging switch')
    staging_log = len(FIXTURE['module_logs'][staging])
    # What must be on screen before the picture is true: names resolved, data in, the placeholders gone.
    capturing = ' && '.join([
        "(d.querySelector('section[aria-label=\"正在抓包\"]') || {textContent: ''}).textContent.includes(%s)" % js(device),
        "d.querySelectorAll('[aria-label^=\"本次抓包最近\"] [role=\"listitem\"]').length === %d" % session,
    ])
    daily = "text.includes('日常模式') && text.includes('台受信设备在线') && !/\\b02:[0-9a-f]{2}:/.test(text)"
    records = capturing + " && d.querySelectorAll('[aria-label^=\"请求记录\"] button').length > 15"
    cards = daily + " && d.querySelectorAll('ol > li').length === %d" % len(FIXTURE['modules'])
    code = "d.querySelectorAll('.cm-editor .cm-line').length >= %d"
    open_logs = ("(() => { const card = [...%s.querySelectorAll('ol > li')].find((li) => li.textContent.includes(%s));"
                 " card.querySelector('button[aria-label=\"日志\"]').click(); })()" % (DOC, js('Staging switch')))
    # Content in the detail pane or the dialog is whole when nothing in it scrolls and the dialog clears the edges.
    detail_whole = "(() => { const pane = d.querySelector('[role=\"tablist\"]').nextElementSibling; return pane.scrollHeight <= pane.clientHeight; })()"
    dialog_whole = ("(() => { const box = d.querySelector('[role=\"dialog\"]'); const log = box.querySelector('ol'); const r = box.getBoundingClientRect();"
                    " return log.scrollHeight <= log.clientHeight && r.top >= 24 && r.bottom <= d.documentElement.clientHeight - 24; })()")
    return [
        # The record list and the detail pane share the window. The height is a start; the list is then shortened to
        # end on a whole row.
        Shot('dashboard-records', 'capture', '/?r=' + record_id(path='/v1/flags'), 1360, height=850, snap=True,
             ready=' && '.join([records, "text.includes('经模块处理：Mock feature flags')", code % 15]),
             verify=detail_whole, problem='the detail pane scrolls; raise the height'),
        Shot('dashboard-websocket', 'capture', '/?r=' + record_id(status=101), 1360, height=570, snap=True,
             ready=records + " && d.querySelectorAll('[role=\"tabpanel\"] ol > li').length === %d" % frames,
             verify=detail_whole, problem='the detail pane scrolls; raise the height'),
        # Below about 1160 px the status band's request chart runs under its buttons.
        Shot('dashboard-devices', 'capture', '/devices', 1160, pad=BADGE,
             ready=capturing + " && d.querySelectorAll('tbody tr').length === %d" % len(FIXTURE['devices'])),
        Shot('dashboard-modules', 'daily', '/modules', 924, pad=BADGE, ready=cards),
        Shot('dashboard-module-editor', 'daily', '/modules/' + staging, 924, pad=BADGE,
             ready=' && '.join([daily, "d.getElementById('name') && d.getElementById('name').value === %s" % js('Staging switch'), code % 14])),
        # The dialog is centred in the window; the height leaves it room, and its list does not scroll.
        Shot('dashboard-module-logs', 'daily', '/modules', 924, height=740, ready=cards, after=open_logs,
             after_ready=daily + " && d.querySelectorAll('[role=\"dialog\"] ol > li').length === %d" % staging_log,
             verify=dialog_whole, problem='the log dialog does not fit; raise the height'),
    ]


def wait_for(page, ready, what, timeout=40):
    """Poll until the dashboard has everything on screen and has stopped moving."""
    deadline = time.monotonic() + timeout
    probe = PROBE % ready
    reason, still = 'document', None
    while time.monotonic() < deadline:
        reason = page.evaluate(probe)
        if not reason:
            layout = page.evaluate(LAYOUT)
            if layout == still:
                return
            still = layout
        else:
            still = None
        time.sleep(0.25)
    raise RuntimeError('%s: the page was not ready after %ds (waiting for %s)' % (what, timeout, reason))


def screenshot(chrome, base_url, shot, output):
    start = shot.height or 1600
    page = Page(chrome, shot.width, start)
    try:
        src = shot.path + ('&' if '?' in shot.path else '?') + 'scene=' + shot.scene
        query = urllib.parse.urlencode({'src': src, 'w': shot.width, 'h': start})
        page.call('Page.navigate', {'url': '%s/_frame?%s' % (base_url, query)})
        wait_for(page, shot.ready, shot.name)
        page.evaluate(LOAD_FONTS, wait=True)
        height = start
        if shot.height is None:
            # The page's height can depend on the window's (vh units), so measure until the two agree.
            for _ in range(6):
                needed = page.evaluate(CONTENT_HEIGHT) + shot.pad
                if needed == height:
                    break
                height = needed
                page.evaluate(SET_HEIGHT % height, wait=True)
        elif shot.snap:
            height -= page.evaluate(LIST_SLACK)
        page.resize(shot.width, height)
        page.evaluate(SET_HEIGHT % height, wait=True)
        if shot.after:
            page.evaluate(shot.after)
        wait_for(page, shot.after_ready or shot.ready, shot.name)
        page.evaluate(SETTLE, wait=True)
        if shot.verify and not page.evaluate('(() => { const d = %s; return %s; })()' % (DOC, shot.verify)):
            raise RuntimeError('%s: %s' % (shot.name, shot.problem))
        image = base64.b64decode(page.call('Page.captureScreenshot', {
            'format': 'png', 'fromSurface': True, 'captureBeyondViewport': False,
            'clip': {'x': 0, 'y': 0, 'width': shot.width + PANEL, 'height': height + PANEL, 'scale': 1}})['data'])
    finally:
        page.close()
    temporary = output.with_name(output.name + '.tmp')
    temporary.write_bytes(image)
    temporary.replace(output)
    return struct.unpack('>II', image[16:24]), len(image)


def take_screenshots(server, binary, names):
    chosen = [s for s in shots() if not names or s.name in names]
    output = ROOT / 'docs/assets'
    output.mkdir(exist_ok=True)
    base_url = 'http://127.0.0.1:%d' % server.server_port
    with tempfile.TemporaryDirectory(prefix='mihomo-capture-chrome-') as profile:
        chrome = Chrome(binary, profile)
        try:
            for shot in chosen:
                try:
                    (width, height), size = screenshot(chrome, base_url, shot, output / (shot.name + '.png'))
                except Exception as error:
                    raise RuntimeError('%s\n%s' % (error, chrome.errors())) from error
                print('Wrote docs/assets/%s.png (%dx%d, %d bytes)' % (shot.name, width, height, size), flush=True)
        finally:
            chrome.close()


def find_chrome():
    for name in ('google-chrome', 'google-chrome-stable', 'chromium', 'chromium-browser'):
        found = shutil.which(name)
        if found:
            return found
    return '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome'


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument('--port', type=int, default=8088, help='port of the interactive preview (default 8088)')
    parser.add_argument('--screenshots', action='store_true', help='write the README screenshots to docs/assets/ and exit')
    parser.add_argument('--only', action='append', default=[], metavar='NAME',
                        help='with --screenshots, take only this one (repeat, or separate with commas); names: '
                             + ', '.join(s.name for s in shots()))
    parser.add_argument('--chrome', default=find_chrome(), help='Chrome or Chromium executable')
    args = parser.parse_args()
    if not (ROOT / 'web/dist/index.html').is_file():
        parser.error('Build the dashboard first: make install && make web')
    names = [n[:-4] if n.endswith('.png') else n for arg in args.only for n in arg.split(',') if n]
    unknown = sorted(set(names) - {s.name for s in shots()})
    if unknown:
        parser.error('unknown screenshot %s' % ', '.join(unknown))
    if names and not args.screenshots:
        parser.error('--only needs --screenshots')
    if args.screenshots and not os.access(args.chrome, os.X_OK):
        parser.error('Chrome not found at %s; pass --chrome' % args.chrome)
    handler = functools.partial(Preview, directory=str(ROOT / 'web/dist'))
    with http.server.ThreadingHTTPServer(('127.0.0.1', 0 if args.screenshots else args.port), handler) as server:
        if args.screenshots:
            thread = threading.Thread(target=server.serve_forever, daemon=True)
            thread.start()
            try:
                take_screenshots(server, args.chrome, names)
            finally:
                server.shutdown()
                thread.join()
        else:
            print('Read-only mock dashboard: http://127.0.0.1:%d' % server.server_port, flush=True)
            print('Daily mode instead of the capture: /?scene=daily. In the README frame: /_frame?src=/modules&w=924&scene=daily', flush=True)
            try:
                server.serve_forever()
            except KeyboardInterrupt:
                pass


if __name__ == '__main__':
    main()
