# Releases

Release tags use `vMAJOR.MINOR.PATCH`. Keep changes under `Unreleased` in [CHANGELOG.md](../CHANGELOG.md) until preparing a release. Before 1.0, an incompatible configuration or API change should increment the minor version and explain migration in the notes.

## Prepare a release

1. Update the changelog and both READMEs for any usage changes.
2. Run `make install`, then `MIHOMO_TEST_BINARY=/absolute/path/to/mihomo make check` locally; CI does not run the stock-core tests.
3. Check CA trust and a real application's route separately if the change affects TLS or capture selection. Integration tests do not install system certificates or create TUN routes.
4. Push a tag on a reviewed `main` commit, for example `git tag v0.1.0` followed by `git push origin v0.1.0`.
5. The Release workflow runs the CI check, builds Linux and macOS archives for amd64 and arm64, then creates a **draft** GitHub release with checksums and generated notes. Inspect its contents and notes, then publish the draft manually.

The workflow never changes repository visibility. During private review, keep release drafts private too. Before making the repository public, review the files and history for private data, and enable GitHub private vulnerability reporting so the link in [SECURITY.md](../SECURITY.md) is available. Public visibility is a separate maintainer decision.

## Build packages locally

```sh
make install
make package VERSION=v0.1.0
```

Packages are written to `dist/`, with `SHA256SUMS` alongside them. Each archive includes `bin/mihomo-capture`, the run wrapper, example configuration, both READMEs, documentation, and dependency license notices. It contains no generated CA, tokens, captures, or mihomo binary. Dashboard npm packages keep their own licenses; Go module licenses and a module inventory are included as well. macOS binaries are not signed or notarized.

To verify a downloaded archive, use `sha256sum -c SHA256SUMS` on Linux or `shasum -a 256 -c SHA256SUMS` on macOS. If only one archive was downloaded, verify its corresponding checksum line.
