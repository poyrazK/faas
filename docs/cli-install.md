# Installing the gregale CLI

Two channels, both published automatically from a `v*.*.*` tag. Decision
and rejected alternatives: [ADR-172](adr/172-cli-distribution-channels.md).

| | curl installer | npm |
|---|---|---|
| Command | `curl -fsSL https://get.gregale.dev \| sh` | `npm install -g gregale` |
| Needs | curl/wget, tar, sha256sum | Node ≥ 18 |
| Verifies checksum | yes, against the release `CLI-SHA256SUMS` | yes, npm integrity + provenance |
| Pin a version | `--version v0.1.18` | `gregale@0.1.18` |
| Best for | laptops, servers, Dockerfiles | repos that already have a `package.json` |

Supported platforms: **darwin/arm64, darwin/amd64, linux/amd64,
linux/arm64**.

## curl

```bash
curl -fsSL https://get.gregale.dev | sh
```

Installs to `$HOME/.local/bin` as a normal user, or `/usr/local/bin` when
run as root. The script prints a PATH hint for your shell if the target
directory is not already on `PATH`.

Flags go after `sh -s --`:

```bash
curl -fsSL https://get.gregale.dev | sh -s -- --version v0.1.18 --dir /usr/local/bin
```

| Flag | Environment variable | Default |
|---|---|---|
| `--version <tag>` | `GREGALE_VERSION` | newest stable release |
| `--dir <path>` | `GREGALE_INSTALL_DIR` | `$HOME/.local/bin`, or `/usr/local/bin` as root |

Upgrading is re-running the script; it replaces the binary in place. A
running `gregale logs -f` keeps its open inode and is unaffected.

Uninstall is removing the one file:

```bash
rm ~/.local/bin/gregale
```

### What it verifies

The script downloads the release's `CLI-SHA256SUMS` and compares it against
the archive before anything is written to `PATH`. A mismatch, or an archive
with no entry in `CLI-SHA256SUMS`, aborts with a non-zero exit and installs
nothing. This is covered by `scripts/install_test.sh`, which runs on every
PR. Releases from before the CLI/daemon checksum split remain installable
through a fallback to their legacy `SHA256SUMS` asset.

To verify by hand instead:

```bash
TAG=v0.1.18
curl -fsSLO "https://github.com/poyrazK/faas/releases/download/$TAG/gregale_${TAG#v}_darwin_arm64.tar.gz"
curl -fsSLO "https://github.com/poyrazK/faas/releases/download/$TAG/CLI-SHA256SUMS"
sha256sum --ignore-missing -c CLI-SHA256SUMS
```

### Release candidates

Every tag so far is a prerelease (`v0.1.18-rc.119`). The installer resolves
the newest *stable* release; while none exists it falls back to the newest
prerelease and says so on stderr. Once a stable tag ships, `curl | sh`
silently starts preferring it — pass `--version` to stay on a specific rc.

## npm

```bash
npm install -g gregale
npx gregale deploy          # without installing
npm install -g gregale@rc   # newest release candidate
```

The `gregale` package holds no binary. It declares four
`optionalDependencies` gated on `os`/`cpu`, so npm downloads only the
`@gregale/cli-<os>-<arch>` package matching your machine, and a small
launcher (`bin/gregale.js`) execs it.

If the launcher reports a missing platform package, optional dependencies
were skipped:

```bash
npm install --include=optional gregale
```

`latest` tracks stable releases; prerelease tags publish under `rc`.

## In CI

GitHub Actions already has a first-class path that needs no install — the
action vendors the binary:

```yaml
- uses: poyrazK/faas/.github/actions/deploy@v0
  with:
    app: my-app
```

Elsewhere, pin an exact version so a new release cannot change your build:

```bash
curl -fsSL https://get.gregale.dev | sh -s -- --version v0.1.18 --dir /usr/local/bin
```

In a Dockerfile:

```dockerfile
RUN curl -fsSL https://get.gregale.dev | sh -s -- --version v0.1.18 --dir /usr/local/bin
```

## Shell completion and man pages

The binary is the source of truth for both; nothing is installed by either
channel. See [`cli-setup.md`](cli-setup.md).

```bash
gregale completion zsh > ~/.zsh/completions/_gregale
gregale man | man -l -
```

Persistent non-secret settings are managed with [`gregale config`](cli-config.md):
`gregale config list`, `gregale config get api-base`, and
`gregale config set json true`. `FAAS_API` and `FAAS_JSON` remain higher
precedence for CI and one-off local overrides.

## Windows

Not supported. `cmd/gregale` transitively imports `pkg/fcvm`, which needs
`syscall.Stat_t`, `syscall.Mkfifo`, and `syscall.SYS_IOCTL` and does not
compile for `GOOS=windows`. Both channels fail closed rather than install
something broken: the installer exits 1, and npm's `os` field refuses the
package. WSL2 works today (it is linux/amd64).

## Operator setup

One-time steps behind these channels, for whoever owns the release:

1. **`get.gregale.dev`** must serve `scripts/install.sh`. The Worker that
   does it, plus its deploy and pinning steps, lives in
   [`deploy/cloudflare/get-gregale-dev/`](../deploy/cloudflare/get-gregale-dev/README.md).
   Note the ordering: it can only be pinned to a **tag that contains the
   installer**, and the newest tag predates ADR-172 — so cut a release
   first. Until the Worker is deployed the hostname resolves to
   `gatewayd-public` through the `gregale.dev` wildcard and answers with
   `no app is routed to "get.gregale.dev"`; the working URL meanwhile is
   `https://raw.githubusercontent.com/poyrazK/faas/main/scripts/install.sh`.
2. **npm** needs an org named `gregale` and an automation token in the
   `NPM_TOKEN` repository secret. Without the secret the `publish-npm` job
   still stages and validates all five packages, then skips publishing with
   a workflow warning — releases are never blocked on credentials.
3. Nothing else is per-release. A `v*.*.*` tag builds the four archives,
   attaches them plus `install.sh` and `CLI-SHA256SUMS` to the GitHub Release,
   and publishes npm under `latest` (stable) or `rc` (prerelease).
