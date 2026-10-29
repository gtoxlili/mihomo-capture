# Support

Use [Issues](https://github.com/gtoxlili/mihomo-capture/issues) for bugs, feature requests, and setup questions. Search existing issues first. Include your OS, service revision or release, mihomo version, and the exact error. English and Chinese are welcome. Security-sensitive reports belong in the [security reporting process](SECURITY.md).

## No requests appear

Confirm the application already uses mihomo through a system proxy or TUN, the correct device is selected, and capture has started. In local mode, compare the source address reported by mihomo with the dashboard's device addresses. Add an extra TUN source to `local_source_ips` if needed. Refresh the application connection after starting capture; an existing connection may keep its earlier path.

## HTTPS fails

Install the service's current CA and fully trust it on the device. A dashboard trust confirmation alone does not install a certificate. Some applications pin certificates or use their own trust store and may refuse interception. Remove optional registered modules if their hostnames should stop being decrypted after recording ends.

## Configuration check fails

Run `bin/mihomo-capture -config config.yaml -check`. Use the core's actual final YAML, keep mihomo in `rule` mode, and place `gateway_state_dir` inside its home directory. See [compatibility restrictions](docs/mihomo.md). Check that the controller is reachable and the token file matches its `secret`.

## A gateway device is missing

Gateway discovery supports IPv4 neighbors. Check `mode: gateway`, the `lan` CIDR, and that the target actually uses this computer as its gateway. Linux requires `iproute2`. A randomized MAC creates a separate device whose CA trust must be confirmed again.

## Routing is stuck after an abnormal exit

Stop the service, then run `-clear-gateway` or `-remove-gateway` as described in the [README](README.md#stop-and-uninstall). Reloading mihomo's own configuration removes the capture entries too. Do not switch mihomo into global mode with capture entries installed.
