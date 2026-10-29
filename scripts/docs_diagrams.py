#!/usr/bin/env python3
"""Generate README diagrams as editable SVG and portable PNG."""
from html import escape
from pathlib import Path
import subprocess

ROOT = Path(__file__).resolve().parent.parent
OUT = ROOT / 'docs/assets'
INK, MUTED, TEAL, ORANGE = '#17202b', '#586676', '#0d776e', '#d85e3a'


def text(x, y, value, size=24, color=INK, weight=400, mono=False):
    font = 'monospace' if mono else 'Arial, Noto Sans CJK SC, PingFang SC, sans-serif'
    return f'<text x="{x}" y="{y}" font-family="{font}" font-size="{size}" font-weight="{weight}" fill="{color}">{escape(value)}</text>'


def rect(x, y, w, h, fill='#fff', stroke='#dce2e7', radius=16):
    return f'<rect x="{x}" y="{y}" width="{w}" height="{h}" rx="{radius}" fill="{fill}" stroke="{stroke}"/>'


def arrow(x, y, ex, ey, color=TEAL, dashed=False):
    dash = 'stroke-dasharray="7 7"' if dashed else ''
    return f'<path d="M{x} {y} L{ex} {ey}" fill="none" stroke="{color}" stroke-width="3" {dash} marker-end="url(#arrow)"/>'


def save(name, title, content, h):
    svg = f'''<svg xmlns="http://www.w3.org/2000/svg" width="900" height="{h}" viewBox="0 0 900 {h}" role="img" aria-label="{escape(title)}">
<title>{escape(title)}</title><defs><marker id="arrow" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse"><path d="M0 0L10 5L0 10" fill="{TEAL}"/></marker></defs>
<rect width="900" height="{h}" rx="22" fill="#f5f7f9"/>{content}</svg>'''
    OUT.mkdir(exist_ok=True)
    source = OUT / f'{name}.svg'
    source.write_text(svg)
    subprocess.run(['rsvg-convert', '-o', str(OUT / f'{name}.png'), str(source)], check=True)
    print(f'Wrote docs/assets/{name}.svg and .png')


for zh in (False, True):
    suffix = '.zh-CN' if zh else ''
    c = text(38, 48, '从已有 mihomo 到开始抓包' if zh else 'From your existing mihomo to the first capture', 29, weight=700)
    c += text(38, 80, '服务和 mihomo 运行在同一台电脑' if zh else 'Mihomo Capture and mihomo run on the same computer.', 21, MUTED)
    c += rect(30, 112, 840, 322)
    c += text(55, 151, '01  连接 mihomo' if zh else '01  Connect to mihomo', 26, TEAL, 700)
    c += text(55, 189, '从你的 mihomo 获取' if zh else 'Find these in your mihomo setup', 21, MUTED)
    rows = [
        ('Controller 地址 / socket' if zh else 'Controller URL / socket', 'controller_url / controller_socket'),
        ('最终加载的 YAML 文件' if zh else 'The final, loaded YAML file', 'gateway_config'),
        ('mihomo home 下的新目录' if zh else 'A new directory in mihomo home', 'gateway_state_dir'),
    ]
    for y, (left, right) in zip((233, 292, 351), rows):
        c += rect(52, y-25, 355, 46, '#f5f7f9', '#edf0f3', 8)
        c += text(66, y+5, left, 20)
        c += arrow(419, y-2, 459, y-2)
        c += text(475, y+4, right, 17, TEAL, mono=True)
    c += text(55, 403, '有 secret？写入权限为 0600 的 controller_token_file。' if zh else 'If mihomo has a secret, set controller_token_file to a 0600 file.', 19, MUTED)
    c += arrow(450, 440, 450, 474)
    c += rect(30, 486, 840, 145)
    c += text(55, 526, '02  生成并信任 CA' if zh else '02  Generate and trust the CA', 26, TEAL, 700)
    c += text(55, 565, '-init  →  -check  →  scripts/run.sh', 24, mono=True)
    c += text(55, 601, '浏览器打开 Dashboard，安装并完全信任 CA。' if zh else 'Open the dashboard. Install and fully trust its CA on the device.', 21, MUTED)
    c += arrow(450, 638, 450, 670)
    c += rect(30, 683, 840, 140, '#e9f5f1', '#bfddd3')
    c += text(55, 723, '03  选“本机”，开始抓包' if zh else '03  Select your local device and start capture', 26, TEAL, 700)
    c += text(55, 762, '应用 → 系统代理 / TUN → mihomo' if zh else 'Your app → system proxy / TUN → mihomo', 24)
    c += text(55, 796, '只有已经经过 mihomo 的流量才会被抓到。' if zh else 'Only traffic already routed through mihomo can be captured.', 21, MUTED)
    save('setup'+suffix, '接入流程' if zh else 'Setup at a glance', c, 854)

    c = text(38, 48, '抓包时，请求到底怎么走？' if zh else 'Where does a captured request go?', 30, weight=700)
    c += text(38, 82, '先按原始连接选策略，再解密，最后沿已选策略出网。' if zh else 'Choose the original policy first. Inspect HTTP. Leave via that policy.', 21, MUTED)
    steps = [
        ('应用 / 局域网设备' if zh else 'Your app / selected LAN device', '系统代理或 TUN 把 TCP 交给 mihomo' if zh else 'System proxy or TUN delivers TCP to mihomo', '#fff'),
        ('mihomo · 原始规则选策略' if zh else 'mihomo · choose the outbound policy', '使用原设备的来源 IP、端口和原始规则' if zh else 'Uses the original source IP, port, and routing rules', '#fff'),
        ('Mihomo Capture · 解密与记录' if zh else 'Mihomo Capture · inspect and record', 'HTTP / HTTPS / WebSocket · Dashboard · curl' if zh else 'HTTP / HTTPS / WebSocket · dashboard · curl', '#fff3ed'),
        ('mihomo · 专用出口监听器' if zh else 'mihomo · dedicated outbound listener', '直接使用已选策略，不再匹配主规则' if zh else 'Uses the selected policy; skips the main rules', '#e9f5f1'),
        ('DIRECT / 原代理节点或策略组 → 目标服务' if zh else 'DIRECT / original proxy or group → destination', '分流意图保留；目标服务正常接收请求' if zh else 'The original routing decision is preserved', '#fff'),
    ]
    for i,(heading,caption,fill) in enumerate(steps):
        y=119+i*143
        c+=rect(48,y,804,107,fill)
        c+=text(69,y+43,f'{i+1:02d}',24,TEAL,700,True)
        c+=text(123,y+41,heading,25,weight=700)
        c+=text(123,y+79,caption,20,MUTED)
        if i<4:c+=arrow(450,y+114,450,y+135)
    c+=rect(48,852,804,91,'#edf1f5','#dce2e7')
    c+=text(69,887,'其他设备不录制；模块 hostname 仍可触发日常解密。' if zh else 'Other sources are not recorded; module hosts may still be decrypted.',20)
    c+=text(69,919,'原 YAML 不变；附加节点与规则仅装进 mihomo 运行配置。' if zh else 'The source YAML stays intact; capture entries live in runtime config.',19,MUTED)
    save('routing'+suffix, '抓包请求路径' if zh else 'Captured request path',c,974)
