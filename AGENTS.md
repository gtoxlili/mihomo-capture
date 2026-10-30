# Purpose

Mihomo Capture is a Go service that records the HTTP, HTTPS and WebSocket traffic of one device through a stock mihomo while keeping the policy mihomo's own rules choose. It serves an HTTP API and a dashboard on the same address. CLAUDE.md links to this file.

# Where to start

README.md covers usage and the two modes, docs/mihomo.md how the capture lanes enter mihomo and which configurations are refused, docs/api.md the API.

# Settled decisions

Both modes share one capture path. `local`, the default, captures the machine running the service by its loopback, interface and configured source addresses. `gateway` captures one LAN device, identified by MAC from the neighbor table; the machine running mihomo is never a gateway-mode device.

Surge modules contribute only their MITM hostname patch. Scripts, rewrites and module rules are not executed; `Processor` in internal/app/types.go is the extension point.

The dashboard in web/ is built into the binary and served on the API address. Page files need no token and data flows only through the token-protected /v1 API, so the dashboard is one more API caller and adds no capability.

`-init` generates the CA and tokens on the machine that runs the service, and the CA key never leaves it. The repository holds no identities, private network details or captured traffic.

A deployment that needs different behavior gets it as a general configuration option here, not as a fork.

# Canonical sources

Implementation > this file > docs.

# Invariants

**I1. Only a trusted, currently selected device produces capture records.** Switching the capture, revoking trust and clearing records invalidate the write tickets of earlier requests. Enforced by the selection published to mihomo, the ticket issued when a capture-lane connection arrives, and the store lock, all in internal/app.

**I2. Capture state lives only in memory, and mihomo hands no traffic to the service while its process is gone.** A restart returns to the daily state. Both lanes are emptied when the process ends, and they disappear once mihomo reloads or restarts its own configuration, so an offline service never blocks the network. Enforced by the startup clear, the exit clear in scripts/run.sh, and internal/gateway never writing mihomo's configuration file.

**I3. Captured traffic takes the policy mihomo's original rules choose for the original connection.** Source address, source port and inbound rules can change the choice, so each lane evaluates a copy of the original rules with the device's own connection metadata, and the service leaves only through the chosen policy without evaluating rules again. Enforced by the catalog and the egress listener in internal/gateway.

# Docs

Docs hold the decisions, boundaries and cross-process semantics the code cannot express. When a change makes existing text false, fix or delete it; not every feature needs a doc. User-facing docs exist in English and Chinese (`*.zh-CN.md`), and a change updates both. docs/api.md is the API description and changes with API behavior.

# Tests and verification

Tests derive from the README, docs/api.md, docs/mihomo.md, the invariants above and upstream protocols, and name their source with `// invariant: I<n>`, `// contract: <file>`, `// repro:` or `// upstream:`. internal/app uses a fake gateway that records published selections. Stock-core behavior is verified only in internal/gateway, whose core tests need `MIHOMO_TEST_BINARY` set to an unmodified mihomo and skip without it; CI sets it on Linux and macOS.

The default check is `make check`. Without web/ changes, `go test -race ./...` and `go vet ./...` are enough.

# Delivery

Leave changes uncommitted unless asked. Report what was verified, what was not and why. Note issues outside the task in one line instead of fixing them.
