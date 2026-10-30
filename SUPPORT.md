# Support

Use [Issues](https://github.com/gtoxlili/mihomo-capture/issues) for bugs, feature requests, and setup questions. Search existing issues first. Include your OS, service revision or release, mihomo version, and the exact error. English and Chinese are welcome. Security-sensitive reports belong in the [security reporting process](SECURITY.md).

## No requests appear

Confirm the application already uses mihomo through a system proxy or TUN, the correct device is selected, and capture has started. In local mode, compare the source address reported by mihomo with the dashboard's device addresses. Add an extra TUN source to `local_source_ips` if needed. Refresh the application connection after starting capture; an existing connection may keep its earlier path.

## HTTPS fails

Install the service's current CA and fully trust it on the device. A dashboard trust confirmation alone does not install a certificate. Some applications pin certificates or use their own trust store and may refuse interception. Outside a capture, only the hostnames of modules enabled for the device are decrypted; disable a module, or take the device off it, to stop that.

## SSH or SMTP hangs during capture

A connection to a protocol where the server speaks first, such as SSH or SMTP, can lose its opening banner and hang while its device is captured. This is a mihomo bug in v1.19.33 and earlier, and only some connections hit it. Open a new connection, or stop the capture while you use that protocol. The fix is merged into mihomo's Alpha branch and will come with the next stable release. See [the known issue](docs/mihomo.md#known-issue-server-first-protocols).

## A module does not act

The device must be trusted, online, and listed on the enabled module, and the request's host must match one of the module's hostnames. Device changes apply to connections opened afterwards, so reconnect the application. The module's log on the dashboard, or `GET /v1/modules/{id}/logs`, names rules that were skipped and scripts that failed, timed out or never called `$done`.

## Configuration check fails

Run `bin/mihomo-capture -config config.yaml -check`. Use the core's actual final YAML, keep mihomo in `rule` mode, and place `gateway_state_dir` inside its home directory. See [compatibility restrictions](docs/mihomo.md). Check that the controller is reachable and the token file matches its `secret`.

## A gateway device is missing

Gateway discovery supports IPv4 neighbors. Check `mode: gateway`, the `lan` CIDR, and that the target actually uses this computer as its gateway. Linux requires `iproute2`. A randomized MAC creates a separate device whose CA trust must be confirmed again.

## Routing is stuck after an abnormal exit

Stop the service, then run `-clear-gateway` or `-remove-gateway` as described in the [README](README.md#limits-and-cleanup). Reloading mihomo's own configuration removes the capture entries too. Do not switch mihomo into global mode with capture entries installed.
