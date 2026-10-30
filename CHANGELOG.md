# Changelog

User-visible changes are recorded here. Tagged releases use `vMAJOR.MINOR.PATCH`; interfaces may change before 1.0. Release notes include the relevant pull requests.

## Unreleased

- Capture local HTTP, HTTPS, and WebSocket traffic through stock mihomo while preserving its outbound policy.
- Select a LAN device in gateway mode, with macOS ARP and Linux neighbor discovery.
- Inspect requests in the dashboard and export complete requests as curl commands.
- Process chosen devices' requests with modules: header, URL and body rewrites, map local, and http-request and http-response scripts, written through the API or dashboard and enabled per trusted device.
- Configure a Unix socket or authenticated HTTP controller, with a locally generated CA and service tokens.
- Provide English and Chinese usage documentation, contribution templates, and draft release packaging.
