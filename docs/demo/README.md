# Screenshot fixtures and preview

The README screenshots show the actual built dashboard backed by `traffic.json`. All hosts use `.test`, payloads are synthetic, and the preview token is an inert demo value. The preview does not connect to mihomo, generate a CA, install certificates, or change proxy settings. Its API is read-only and it binds to loopback.

```sh
make install
make web
python3 scripts/docs_preview.py
```

Open <http://127.0.0.1:8088> to browse the fixture records. Stop with Ctrl-C. The demo badge and fixed clock exist only in this preview's served HTML, not in the product build.

## Refresh the screenshots

```sh
python3 scripts/docs_preview.py --screenshots
# On Linux, or with a different Chrome installation:
python3 scripts/docs_preview.py --screenshots --chrome /path/to/google-chrome
```

This uses Chrome headless with temporary profiles and an ephemeral loopback server. The profiles and server are removed afterward. Images go to `docs/assets/`. Capture the full HTTP detail and WebSocket views without altering the product UI.

## Refresh the diagrams

```sh
# Requires rsvg-convert from librsvg.
python3 scripts/docs_diagrams.py
```

The generator writes editable SVG and PNG versions in English and Chinese. README embeds repository-local PNGs so it works without third-party image services or a Mermaid renderer.
