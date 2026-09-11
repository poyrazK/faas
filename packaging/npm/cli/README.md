# gregale

The CLI for [Gregale](https://github.com/poyrazK/faas) — scale-to-zero
Functions-as-a-Service on Firecracker microVMs.

```sh
npm install -g gregale
gregale login
gregale deploy
```

Or without installing:

```sh
npx gregale deploy
```

## What this package contains

No binary. This package is a small launcher; the compiled binary arrives
through one of four `optionalDependencies`, gated on `os` and `cpu` so npm
downloads only the one matching your machine:

| Platform | Package |
|---|---|
| macOS Apple Silicon | `@gregale/cli-darwin-arm64` |
| macOS Intel | `@gregale/cli-darwin-amd64` |
| Linux x86_64 | `@gregale/cli-linux-amd64` |
| Linux arm64 | `@gregale/cli-linux-arm64` |

If the launcher reports a missing platform package, optional dependencies
were skipped during install:

```sh
npm install --include=optional gregale
```

**Windows is not supported yet.** The `os` field makes npm refuse the
install rather than leave you with a launcher that cannot find a binary.

## Release channels

`latest` tracks stable releases. Pre-1.0 release candidates publish under
the `rc` tag:

```sh
npm install -g gregale@rc
```

## Other install methods

```sh
curl -fsSL https://get.gregale.dev | sh
```

Both channels install the same binary from the same signed GitHub Release.
Shell completion and man pages come from the binary itself —
`gregale completion --help` and `gregale man`.

## License

Proprietary. See `LICENSE`.
