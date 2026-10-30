# mihomo integration

[简体中文](mihomo.zh-CN.md)

![Captured request path through the original policy, inspection, and dedicated outbound](assets/routing.png)

For initial setup, follow the [illustrated connection guide](../README.md#get-connected). This page explains why the return connection keeps the original routing decision.

Mihomo Capture runs on the same computer as mihomo. It reads the actual loaded YAML and uses the controller's `PUT /configs` endpoint to load a copy with capture nodes and an outbound listener. The source file is never written. File rule providers live in `gateway_state_dir`, which must be readable by mihomo and should be inside its home directory.

The service adds proxies, rule providers, and sub-rules prefixed with `mihomo-capture-`, plus an HTTP outbound listener bound to loopback. The capture lane receives TCP from the selected device. The daily lane handles registered module hostnames for trusted devices. Each lane copies the original rules and sub-rules. Mihomo selects a policy while the connection's source metadata is still available, then sends its policy ID in the proxy CONNECT headers. After interception, the service re-enters mihomo through the outbound listener, whose own sub-rules select that policy without evaluating the main rules again. This also lets loopback traffic be captured without repeatedly entering the service.

Selection changes update only the provider files. The first nonempty selection loads the extra configuration. The service checks every five seconds and reinstalls it if mihomo has reloaded its own configuration. After editing mihomo's source file, reload it normally so the running configuration and file agree.

## Compatibility restrictions

Mihomo must remain in `rule` mode. Do not switch to `global` or `direct` with capture entries installed, and do not select internal `mihomo-capture-` nodes in a desktop client. A mode switch can bypass outbound sub-rules and cause routing loops. Stop the service and run `-remove-gateway` before changing modes.

The service rejects configurations it cannot preserve, including `PASS`, `PASS-RULE`, source-dependent `load-balance` groups, group types other than select/fallback/url-test, and `include-all` or `include-all-proxies`. Explicit group members avoid accidentally including capture nodes as ordinary outbounds. Existing names with the reserved prefix, missing rules, unknown policies, and conflicting outbound ports are also rejected. Use `-check` first.

The required inputs are the core's controller and final configuration. The service does not read a desktop client's database or infer its subscription conversion and merge behavior. Clients that automatically replace the running core configuration may conflict with capture reloads. Disable that behavior or use a standalone core.

## Lifecycle

Startup clears both selections. Stopping capture or revoking trust invalidates in-flight record tickets. Existing TCP connections may continue naturally; stopping recording does not actively disconnect them. Stopping the service disconnects connections it owns. Clearing records leaves capture active, so new connections can still produce records.

`-clear-gateway` empties the selections. `-remove-gateway` asks the core to reload its original configuration and deletes selection files. Stop the service before removal, or the next synchronization can reinstall the entries. If the service hangs while its process is still running, mihomo keeps handing the selected traffic to it; restart the service.

Gateway discovery uses IPv4 neighbors. DHCP reassignment can leave a stale IP binding until the next five-second refresh. Randomizing a MAC address creates a new device without inherited trust.

## Test coverage

Stock-core tests cover source-address and source-port routing, REJECT, loopback traffic passing through capture once, unselected sources, hostname exclusions, server-first protocols, half-close, UDP port blocking, reload, clear, and removal. They need neither TUN nor administrator privileges. Real application trust, desktop-client configuration merges, and system routing require separate manual verification.
