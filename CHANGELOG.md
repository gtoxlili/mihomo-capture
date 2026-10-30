# Changelog

User-visible changes are recorded here. Tagged releases use `vMAJOR.MINOR.PATCH`; interfaces may change before 1.0. Release notes include the relevant pull requests.

## Unreleased

## v0.1.0 - 2026-10-30

- Capture local HTTP, HTTPS, and WebSocket traffic through stock mihomo while preserving its outbound policy.
- Select a LAN device in gateway mode, with macOS ARP and Linux neighbor discovery.
- Inspect requests in the dashboard and export complete requests as curl commands.
- Process chosen devices' requests with modules: header, URL and body rewrites, map local, and http-request and http-response scripts, written through the API or dashboard and enabled per trusted device.
- Configure a Unix socket or authenticated HTTP controller, with a locally generated CA and service tokens.
- Provide English and Chinese usage documentation, contribution templates, and release archives for Linux and macOS on amd64 and arm64.
- Known issue: with mihomo v1.19.33 and earlier, SSH, SMTP, and other server-first protocols can lose their opening banner during capture. The fix, MetaCubeX/mihomo#3382, is merged upstream and will arrive with the next stable mihomo release.
