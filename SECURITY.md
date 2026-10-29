# Security policy

## Supported versions

Until the first tagged release, fixes target `main`. After releases begin, security fixes target the latest release. Older versions may require an upgrade.

## Report a vulnerability

Use [GitHub's private vulnerability reporting page](https://github.com/gtoxlili/mihomo-capture/security/advisories/new) when it is enabled for the public repository. If it is unavailable, open an issue titled "Security contact request" with no technical details and ask the maintainer for a private channel. The repository may still be private during review.

A private report should describe the affected version, impact, and a minimal reproduction. Include synthetic requests or a redacted configuration when possible. Do not send live access tokens, CA private keys, or unrelated captured traffic. Please keep exploit details private while a fix is prepared. There is no guaranteed response time or bounty program.

## Operating the service

The API token grants access to all captured data and device controls. The CA key can sign certificates trusted by devices where the CA is installed. Keep the data directory owner-only, bind the API and mihomo controller to loopback unless you have deliberately configured authenticated remote access, and remove the CA from trust stores when it is no longer needed.

The service cannot inspect a remote operating system's trust store. Trust confirmation in the dashboard records the operator's statement and is bound to the current CA fingerprint. Capture traffic only where you have permission to inspect it.
