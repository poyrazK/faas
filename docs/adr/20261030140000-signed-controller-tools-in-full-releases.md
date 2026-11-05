# ADR-20261030140000: Full releases ship signed controller tools

- **Status:** proposed
- **Date:** 2026-10-30
- **Related:** ADR-113 (canonical signed release), ADR-1650 (per-unit
  releases), ADR-580 (control-plane convergence in CD)

## Context

`cd-controlplane`'s deploy job assembles the control plane's release bundle
from the verified `release.tar.gz`, then compiled two controller tools from
the checked-out tag: `deployctl` (bundle creation, upload, activation) and
`migrate` (the migrations it runs before activation). The canonical tarball
does not contain them: `release-manifest.json` of rc.307 lists the catalog
daemons, `gregale`, `gregalectl`, `init`, the vmmd helpers, `vmlinux` and
the runners, and no controller tool.

On production-us the fleet runner is the control plane itself, a 2-vCPU
host at a load average near 4. The two `go build` calls took most of the
"Build immutable release bundle" step: 508–734 s on the first attempt of
every release from rc.294 to rc.308 (rc.306: 8 min 42 s from the inventory
render to `bundle-create`). Retries took 143–181 s, on a warm build cache.
That is about a quarter of the 34-minute job, spent compiling on the host
that serves the control plane.

They are also the only binaries the deploy runs that no signature covers:
they come from the runner's Go toolchain and the runner's build cache. The
unit lane already ships its `tools/deployctl` and `tools/migrate` inside
its signed unit release, so its deploy jobs neither build them nor trust a
runner-built copy (ADR-1650, `scripts/build-unit-release.sh`).

## Decision

1. `release.yml`'s daemon-bundle job builds `deployctl` and `migrate` with
   the flags `cd-controlplane` used (`-trimpath`, `-s -w`,
   `wire.Version=<tag>`, plus `-buildvcs=false`), packs exactly those two
   files into `controller-tools.tar.gz`, signs it with `cosign sign-blob`
   under the same GitHub OIDC identity as `release.tar.gz`, and publishes
   it with `controller-tools.cosign.bundle` as release assets.
2. `cd-controlplane` downloads the pair when the release has it, verifies
   the signature against `release.yml@refs/tags/<tag>` (the identity it
   already requires of `release.tar.gz`), checks the archive holds exactly
   the two files, and installs them into the bundle's `bin/`. The bundle's
   layout, `deployctl bundle-create`, the compact transport and activation
   are unchanged.
3. A release without the asset (every tag before this change) keeps the
   old path: the job compiles both tools from the checked-out tag. This
   covers a repair dispatch of an older tag.

They stay out of `release.tar.gz`. Its `release-manifest.json` is verified
by `releaseinstall` on every host, and `releaseinstall` refuses a
`tool_hashes` entry outside its fixed support-binary catalog. A release
whose manifest names `deployctl` would fail on any host still running
N-1's tooling, and release N must run beside N-1
(docs/release-compatibility.md). `SHA256SUMS` is unchanged too: deploy
jobs check every line of it, so a new line would fail older workflow
revisions that do not download the new asset.

## Consequences

- The deploy job no longer compiles anything for releases built after this
  change. The bundle step drops from 508–734 s to the inventory render,
  `bundle-create` and the transport archive (about 40 s on rc.306), so the
  control-plane stage is about 8–11 minutes shorter on every full release.
- The release's daemon-bundle job, on a GitHub-hosted runner with the main
  build cache restored, links two more commands from the package graph it
  has just built for the daemons and CLIs.
- Every binary the control-plane deploy runs is now signed by the release
  workflow. The CD runner's Go toolchain is no longer in the trust path,
  except for older tags.
- The controller tools have no SBOM or SLSA attestation of their own yet;
  neither did the runner-built copies.

## Rejected alternatives

- **Add them to `release.tar.gz` and the support-binary catalog.** One
  artifact and one SBOM, but N-1 hosts reject the new `tool_hashes`
  entries; see above.
- **Cache the build on the runner.** The runner already keeps its Go build
  cache between jobs (setup-go `cache: false` on the fleet runner); a new
  tag still rebuilds most packages, and the trust problem stays.
- **Build on a hosted runner inside cd-controlplane.** It adds a job and
  hosted-queue time to every rollout, and the bytes would still be unsigned.
