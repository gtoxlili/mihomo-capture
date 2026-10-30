# Contributing

Bug reports, fixes, and documentation corrections are welcome. Use English or Chinese in issues and pull requests. For a larger feature or a routing change, open an issue first so its scope can be discussed.

## Local setup

Install the Go version listed in `go.mod`, Node.js 24+, and pnpm 12. Clone the repository, then run:

```sh
make install
make build
make check
```

Copy `config.example.yaml` to `config.yaml` only if you need to run the service. Use a separate mihomo instance and temporary configuration for routing changes. Do not test against another person's gateway or commit generated identities, tokens, or captures.

## Checks

`make check` checks Go formatting, runs the Go race detector and vet, lints the frontend, and builds both the dashboard and the binary. It does not install dependencies; run `make install` first.

Stock-core integration tests need an unmodified mihomo binary:

```sh
MIHOMO_TEST_BINARY=/absolute/path/to/mihomo make test
```

Without that variable, the core integration tests are skipped. They start their own temporary core and do not change a running instance. CA installation, TUN routing, and real applications' trust behavior need separate manual checks.

Add a regression test when a change affects observable behavior or fixes a bug that could recur. For documentation and other low-impact changes, validate the changed material without adding tests that merely repeat it.

## Pull requests

Explain the problem, the resulting behavior, and how you checked it. Include a small reproduction when it helps. Keep unrelated changes separate. Format Go with `gofmt`; the frontend uses its checked-in Prettier configuration. Changes to configuration, API behavior, or usage should update the relevant English and Chinese documentation in the same pull request.

Use a short commit subject such as `fix: preserve the selected outbound policy`. There is no CLA or sign-off requirement. Contributions are provided under the repository's MIT license. See the [code of conduct](CODE_OF_CONDUCT.md) for participation expectations.

## Reports and captured data

Reduce a failure to synthetic traffic where possible. Remove tokens, cookies, authorization headers, identifying URLs, and private network details before attaching a reproduction. Do not attach CA private keys or full capture directories. For a suspected vulnerability, follow [SECURITY.md](SECURITY.md) instead of posting details in a public issue.
