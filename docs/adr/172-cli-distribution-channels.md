# ADR-172 · Public CLI distribution channels: curl installer and npm

- **Status:** accepted
- **Date:** 2026-09-11

## Decision

Ship the `gregale` CLI through exactly two public channels, both driven
automatically off a `v*.*.*` tag:

1. **A POSIX `sh` installer** (`scripts/install.sh`), served at
   `https://get.gregale.dev`, that resolves the newest release from the
   GitHub API at *run* time, verifies the archive against the release's
   `CLI-SHA256SUMS`, and installs one binary.
2. **npm**, as a root package `gregale` plus four `os`/`cpu`-gated
   platform packages `@gregale/cli-{darwin,linux}-{amd64,arm64}` pulled in
   through `optionalDependencies`.

Both consume the same release artifacts: per-target
`gregale_<version>_<os>_<arch>.tar.gz` archives attached to the GitHub
Release alongside the existing vendored-binary assets.

Release tags that carry a prerelease suffix (`v0.1.18-rc.119`) publish to
npm under the `rc` dist-tag. Only stable tags move `latest`. The installer
mirrors this: it resolves stable by default, and only serves a prerelease
when no stable release exists yet (loudly, on stderr).

## Why

The CLI had no install path at all. `release.yml` attached bare
`linux-amd64` and `darwin-arm64` binaries and vendored the linux one into
the GitHub Action, which covers CI but leaves a developer on a laptop with
"download a file from a releases page and chmod it".

The two channels were chosen for reach per unit of maintenance:

- The installer is the lowest-friction path for the `curl | sh` muscle
  memory every hosted-platform CLI has trained, needs no registry
  account, no review queue, and no per-release publish step — a new tag is
  live the moment the release assets exist.
- Gregale's primary workload is Node applications, so its users already
  have npm. `npx gregale deploy` in a repo that already has a
  `package.json` is a shorter path than any binary download.

Neither channel requires a redistributable license, which matters: the
repository is proprietary (`LICENSE`), which rules out homebrew-core,
nixpkgs, and the distro archives.

## Consequences

- `release.yml` builds **four** targets instead of two. `linux-arm64` and
  `darwin-amd64` are new; both were verified to cross-compile.
- The release publishes the four CLI archives and compatibility binaries in
  `CLI-SHA256SUMS`. The canonical daemon bundle keeps the distinct
  `SHA256SUMS` name, so the two independently generated assets cannot replace
  each other. The installer falls back to the old name only for releases that
  have no `CLI-SHA256SUMS` asset.
- **Windows is not a target.** `cmd/gregale` transitively imports
  `pkg/fcvm`, which needs `syscall.Stat_t`, `syscall.Mkfifo`, and
  `syscall.SYS_IOCTL`. Both channels therefore fail closed on Windows: the
  installer exits 1 with a named reason, and npm refuses to install via the
  root package's `os` field. Cutting that import is a prerequisite for a
  Windows channel (and for Scoop/WinGet later).
- The npm job needs an `NPM_TOKEN` repository secret and a `gregale` npm
  org. Absent the secret the publish steps **skip** rather than fail, so a
  release is never blocked on registry credentials.
- `get.gregale.dev` is a proxied Cloudflare hostname with a redirect rule to
  the maintained `scripts/install.sh` on the default branch. The release
  pipeline also attaches that script to each immutable release.
- Two new hermetic shell tests run on every PR
  (`scripts/install_test.sh`, `scripts/build-npm-packages_test.sh`)
  alongside the existing `materialize-release-manifest_test.sh`. The
  installer's checksum-mismatch path is covered, because a silent
  verification failure in a `curl | sh` installer is the worst defect this
  surface can have.

## Rejected alternatives

- **Homebrew tap.** Viable (a personal tap sidesteps homebrew-core's
  notability bar), and was the original plan, but it reaches a strict
  subset of the installer's audience at the cost of a second repository
  and a cross-repo push token. Revisit when macOS users ask for it by
  name; the release archives this ADR adds are exactly what a formula
  needs, so it stays cheap to add.
- **GoReleaser.** Would own the whole release. `release.yml` already
  performs cosign blob signing, SPDX SBOM generation, builder/runtime-base
  digest resolution, kernel pinning, binary vendoring, and moving-tag
  updates. Re-homing that onto GoReleaser's model is a larger and riskier
  change than the ~50 lines of archive packaging this needs.
- **`go install`.** Broken today regardless: the module declares
  `github.com/onebox-faas/faas` while the repository lives at
  `github.com/poyrazK/faas`, so neither path resolves. It also only ever
  reaches users who already have a Go toolchain.
- **A self-updating binary (`gregale upgrade`).** Attractive, and the
  installer is designed to be re-runnable so it can back one later, but
  in-place self-update needs a signing story for the downloaded
  replacement that this ADR does not attempt.
- **`.deb`/`.rpm` plus a hosted apt/yum repo.** The packaging is easy; the
  signed, hosted repository is the actual work. Deferred until there are
  operators who need unattended server installs.
