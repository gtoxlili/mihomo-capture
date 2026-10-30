# Screenshot fixtures and preview

The README screenshots show the actual built dashboard backed by `traffic.json`. It describes a gateway-mode setup with five synthetic LAN devices, five modules, and 23 records. All hosts use `.test`, MAC addresses are locally administered (`02:`), payloads are synthetic, and the preview token is an inert demo value. The preview does not connect to mihomo, generate a CA, install certificates, or change proxy settings. Its API is read-only and it binds to loopback.

```sh
make install
make web
python3 scripts/docs_preview.py
```

Open <http://127.0.0.1:8088> to browse the fixture. It opens on the capture of Demo iPhone. Add `?scene=daily` to any page for daily mode, where nothing is captured. Stop with Ctrl-C. The demo badge and fixed clock exist only in this preview's served HTML, not in the product build.

When you edit `traffic.json`, keep it consistent with the service. Store module definitions as `module.Compile` returns them. List a module on a record only if it is enabled on that record's device and its hostnames match. Use log lines the engine really writes.

## Refresh the screenshots

```sh
python3 scripts/docs_preview.py --screenshots
python3 scripts/docs_preview.py --screenshots --only dashboard-modules,dashboard-devices
# On Linux, or with a different Chrome installation:
python3 scripts/docs_preview.py --screenshots --chrome /path/to/google-chrome
```

The script drives headless Chrome over its DevTools pipe and uses only the standard library. It waits until the placeholders are gone, sizes each window to its content, and fails a shot whose content would be cut off. Each image is a dotted frame around the dashboard window, written to `docs/assets/` at 2x with transparent corners. The records, WebSocket, and devices shots show the capture of Demo iPhone. The module shots show daily mode.

## Refresh the diagrams

```sh
python3 scripts/docs_diagrams.py
python3 scripts/docs_diagrams.py --only traffic-lanes
```

Each diagram is an HTML page in `docs/diagrams/` that holds its English and Chinese text. Headless Chrome renders it at 2x into `docs/assets/`, once per language, so the README embeds PNGs that need no image service or Mermaid renderer. The pages use the dashboard's colors and its mono font from `web/node_modules`, so run `make install` first. After editing a page, render it and look at both PNGs.
