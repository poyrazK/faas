# Installing the gregale CLI

Two channels ship the same release archives: the curl installer and npm.
On npm, use the `rc` tag until the first stable release (see [npm](#npm)).
Decision and rejected alternatives:
[ADR-172](adr/172-cli-distribution-channels.md).

| | curl installer | npm |
|---|---|---|
| Command | `curl -fsSL https://get.gregale.dev \| sh` | `npm install -g gregale@rc` |
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
directory is not already on `PATH`, and warns when another `gregale` comes
first on `PATH` (an npm install, or an older copy in `/usr/local/bin`), since
plain `gregale` would keep running that copy.

Flags go after `sh -s --`:

```bash
curl -fsSL https://get.gregale.dev | sh -s -- --version v0.1.18 --dir /usr/local/bin
```

| Flag | Environment variable | Default |
|---|---|---|
| `--version <tag>` | `GREGALE_VERSION` | newest stable with matching CLI assets; otherwise newest compatible release |
| `--dir <path>` | `GREGALE_INSTALL_DIR` | `$HOME/.local/bin`, or `/usr/local/bin` as root |

### How it picks a version

Without `--version`, the script reads two one-line pointers that the
release workflow keeps on the `cli-channel` release: `CLI-STABLE`, the
newest stable tag, then `CLI-LATEST`, the newest tag, which may be a
prerelease (the script warns when it installs one). Both are read through
`releases/download`, which GitHub does not count against its anonymous API
limit of 60 requests an hour per address, so installs from a shared office,
CI or conference address keep working. If neither pointer names a release
with your platform, the script falls back to the GitHub API
([ADR-20261025092617](adr/20261025092617-cli-install-channel-pointer.md)).
If that fallback is rate limited too, pass `--version`.

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

The installer prefers the newest *stable* release when it has the archive
for your platform and a checksum asset. Older releases such as `v0.1.17`
predate that format. If the newest stable release cannot be installed, the
installer checks recent published releases for matching CLI assets and warns
before selecting a prerelease. Releases still uploading their assets are
skipped. Pass `--version` to select an exact tag; an explicit install fails
if that tag or its assets are unavailable.

## npm

Every release publishes the npm channel: a prerelease under the `rc`
dist-tag, a stable release under `latest`. There is no stable release yet,
so install with `@rc` or an exact version:

```bash
npm install -g gregale@rc
npx gregale@rc deploy                   # without installing
npm install -g gregale@0.1.18-rc.313    # one exact release
```

Do not use a bare `npm install -g gregale` yet. npm gave `latest` to the
first version ever published, `0.1.18-rc.224`, which predates the current
command tree, and npm refuses to remove `latest`, so it stays there until
the first stable release publishes. From then on `latest` moves to each
stable release, prereleases move only `rc`, and if npm hands `latest` to a
newly published prerelease the release job puts the newest stable version
back. Check where the tags point:

```bash
npm view gregale dist-tags --json
```

A failed npm publish fails the release's workflow run: a missing
`NPM_TOKEN`, a platform package that is not public within ten minutes,
missing provenance, mismatched platform or dependency pins, or a package
that does not install from the public registry. The job waits for all four
platform packages before publishing the launcher, including on retries, and
its install test uses empty npm configs, no credentials and disabled
lifecycle scripts, then checks the installed binary against the release
archive. The GitHub release does not wait for npm, and for a prerelease
neither does the curl installer's channel, so an rc can be installable with
curl while its npm version is missing; use the curl installer above when
`npm view` does not list the version you want. A stable release promotes
the curl installer's stable channel only after npm publishes it, so the two
channels' stable versions match.

The `gregale` package holds no binary. It declares four
`optionalDependencies` gated on `os`/`cpu`, so npm downloads only the
`@gregale/cli-<os>-<arch>` package matching your machine, and a small
launcher (`bin/gregale.js`) execs it.

If the launcher reports a missing platform package, optional dependencies
may have been skipped, or the matching platform version may not yet be public:

```bash
npm install -g --include=optional gregale@rc
```

## In CI

For scripts that need to save a CLI session, pass a token on stdin so it does
not appear in the process argument list:

```bash
printf '%s' "$GREGALE_API_TOKEN" | gregale login --token-stdin
```

Bare `gregale login` requires an interactive terminal. Existing `--token`
calls continue to work.

GitHub Actions already has a first-class path that needs no install — the
action vendors the binary. A direct action reference follows the latest
public-beta release:

```yaml
- uses: poyrazK/faas/.github/actions/deploy@v0
  with:
    app: my-app
```

CLI-generated workflows use an immutable Action SHA by default and add a
`# v0` comment so Dependabot can update the pin. Elsewhere, pin an exact CLI
version so a new release cannot change your build:

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
2. **npm** needs an org named `gregale` and a publishing credential in the
   `NPM_TOKEN` repository secret. Without the secret the `publish-npm` job
   still stages and validates all five packages, then fails its credential
   precondition. A successful upload does not establish public availability:
   npm can retain a holding placeholder while processing the real version.
   The job waits up to ten minutes per publication phase, then fails with
   the missing package names. Inspect the npm owner dashboard before retrying
   an accepted upload; a placeholder does not necessarily mean there is a
   maintainer approval action.
3. Nothing else is per-release. A `v*.*.*` tag builds the four archives,
   attaches them plus `install.sh` and `CLI-SHA256SUMS` to the GitHub Release,
   and publishes npm under `latest` (stable) or `rc` (prerelease).

For an already-uploaded release, run `npm-channel-repair.yml` with its
`release_tag`. It verifies the existing GitHub archives and checks every npm
platform again without uploading any version. It removes prerelease `latest`
tags, withdraws this release's root `rc` while binaries are unavailable, and
restores `rc` after all five versions and their tarballs are public. It refuses
to replace a different release's channel tag and runs the same anonymous
installation/checksum test. It does not rerun daemon builds or deployment.
