# ADR 20261023220532 · Move the Debian-based runtime bases to Debian 13

- **Status:** accepted (2026-10-24)
- **Date:** 2026-10-23
- **Related:** ADR-131 (CVE monitor), ADR-053 (runtimes staged over the Debian
  parent), ADR-510 (snapshot backing identity), ADR-567 and ADR-632 (base
  convergence), ADR-736 and ADR-737 (immutable runtime releases), ADRs 689–816
  (runtime upgrades), ADR-1500 (shared-runtime source apps), issue #5206

## Context

Four platform images are built from Debian 12 (bookworm):

| Image | Built from | Used by |
|---|---|---|
| `base-debian-parent` | `debian:12-slim` | staging parent of the two runtimes below (ADR-053) |
| `runner-node24` | `node:24-bookworm-slim` | `node24` functions; shared-runtime Node source apps (ADR-1500) |
| `runner-python312` | `python:3.12-slim-bookworm` | `python312` functions |
| `base-minimal` | `scratch` plus `debian:12-slim`'s `/usr/lib/x86_64-linux-gnu`, `bash` and CA bundle | every app without a runtime (`baseRefFor("")`) |

Debian 12's regular security support ended in mid-2026; volunteer LTS covers
it until 2028-06-30 and leaves issues the security team rates minor
unpatched (`<no-dsa>`). Two such issues are critical in Grype's database:
glibc CVE-2026-5450 and SQLite CVE-2025-7458. Both are fixed in Debian 13
(trixie), which has regular support until 2028-08-09 and LTS until
2030-06-30. They are the seven criticals left in #5206 after #5241. vmmd
refuses a base with a *fixable* critical only, so they do not block boots;
they stay open for as long as the images are on Debian 12.

The builds already run on Debian 13. Railpack 0.38's build image
(`ghcr.io/railwayapp/railpack-builder:mise-2026.8.13`, from
`buildpack-deps:trixie-scm`) is Debian 13 with glibc 2.41, and the result
runs on these Debian 12 bases with glibc 2.36. Native code compiled during a
build, for example a Node addon or a Python sdist with no prebuilt wheel, can
link symbols that glibc 2.38+ added (C++ `strtol` calls bind to
`__isoc23_strtol@GLIBC_2.38`). It then fails to load at runtime with
"version `GLIBC_2.38' not found". No issue reports it yet; it is latent for
any app whose dependencies compile from source.

`base-minimal` carries the same glibc 2.36 without showing it: it copies the
library files but not `/var/lib/dpkg/status`, so the scanner finds no
packages. The 2026-10-23 rescan (run 39938331972) reported CVE-2026-5450 for
`base-debian-parent` and nothing for `base-minimal`.

Measured on 2026-10-24 against the upstream bases, before Gregale's layers
(Grype 0.116.0, linux/amd64; "fixable+unfixed"):

| Upstream base | Critical | High | Medium | Version | Compressed |
|---|---|---|---|---|---|
| `debian:12-slim` | 0+2 | 0+70 | 0+72 | – | 28.2 MB |
| `debian:13-slim` | **0+0** | 0+54 | 0+47 | – | 29.8 MB |
| `node:24-bookworm-slim` | 0+2 | 3+71 | 8+72 | Node 24.22.1 | 81.0 MB |
| `node:24-trixie-slim` | **0+0** | 3+55 | 8+47 | Node 24.22.1 | 82.7 MB |
| `python:3.12-slim-bookworm` | 0+3 | 0+79 | 11+91 | Python 3.12.15 | 45.3 MB |
| `python:3.12-slim-trixie` | **0+0** | 0+55 | 11+48 | Python 3.12.15 | 43.3 MB |

The fixable Node findings are in npm's bundled modules, which
`runner-node24` deletes. The fixable Python mediums are CPython 3.12.15
(fixed only on the 3.13+ lines) and pip, which `runner-python312` already
replaces with a pinned release. All three Debian 13 candidates share one
Debian layer (`a70f6272`), so ADR-053's parent-prefix rule holds when they
move together.

## Options

1. **Stay on Debian 12** and accept the `<no-dsa>` criticals with expiring
   Grype ignores. Cheapest; keeps two unfixed criticals per image, the
   `base-minimal` blind spot, and LTS-only support until mid-2028.
2. **Move all four images to Debian 13.** Same Node and Python versions,
   same package manager, no change to the runner contract, and the runtime
   matches the Debian 13 builder. Proposed below.
3. **Move `runner-node24` and `runner-python312` to Wolfi**, like
   `runner-python313`. Gives the hardened all-critical gate, but changes the
   package manager and libc build and drops ADR-053's shared parent (each
   runtime stages its full chain). `runner-python313` already is the
   hardened Python lane, so this is not needed to close #5206.

## Decision

1. **`base-minimal` declares what it copies.** Its build stage writes a
   `/var/lib/dpkg/status` holding only the packages that own a file under the
   copied library trees or `/bin/bash`, plus `ca-certificates` (51 of the 88
   packages on the current Debian 12 image), and `/etc/os-release`, so Grype
   sees them. The stage also upgrades its packages before copying. This
   lands first, on Debian 12, and adds the hidden glibc critical to #5206.
2. **The Debian parent and its two runtimes move together** to
   `debian:13-slim`, `node:24-trixie-slim` and `python:3.12-slim-trixie` in
   one change. `scripts/ci/check_parent_base_chain.py` (images.yml) proves the
   chain.
3. **`base-minimal` moves after an audit.** Apps boot it as a mutable logical
   base, so every runtime-less app moves at the release that converges it.
   Debian 13's slim image no longer ships 13 libraries that Debian 12's did:
   `libffi.so.8`, `libgnutls.so.30`, `libgcrypt.so.20`, `libgpg-error.so.0`,
   `libidn2.so.0`, `libp11-kit.so.0`, `libtasn1.so.6`, `libunistring.so.2`,
   `libapt-pkg.so.6` and the e2fsprogs libraries. Before the move, list the
   ELF `DT_NEEDED` entries of every live app layer on `base-minimal`. Apps
   that need a removed library are rebuilt (source apps) or their owners are
   told (image deploys) before that release.
4. **Not decided here:** Python 3.12's own CVEs (only a move to 3.13 fixes
   them), and moving the Debian lanes to the all-critical image gate that
   `runner-python313` uses. Both are separate decisions.

## Effect on running workloads

- **Functions** (`node24`, `python312`) bind an immutable runtime release
  when imaged builds them (ADR-736). They keep booting their Debian 12 base
  until redeployed or moved through the runtime upgrade pipeline
  (ADRs 689–816). New function deploys get Debian 13.
- **Shared-runtime Node source apps** (ADR-1500) and **runtime-less apps**
  (`base-minimal`) boot the runtime's logical base. They move when a node
  converges the new base (ADR-567, ADR-632). Their snapshots fail ADR-510's
  identity check, so each cold-boots once and re-snapshots (ADR-005). Expect
  one cold-boot wave per node at that release.
- **Compatibility:** glibc 2.41 runs binaries built against older glibc, so
  every existing app layer keeps working on the new libc. Builds and runtime
  then match: native code compiled on Railpack's Debian 13 builder loads on
  a Debian 13 base. The libraries removed in step 3 are the known breakage,
  and only for apps on `base-minimal`: the Node and Python images install
  their own runtime libraries.
- **Disk:** each base changes by less than 2 MB compressed; bases are shared,
  so `snapshot_fleet_avg_mb` is unaffected.

## Rollout

1. PR: `base-minimal` dpkg status (step 1).
2. PR: parent, `runner-node24` and `runner-python312` to Debian 13. images.yml
   builds, smoke-tests and scans each image before merge. Merging publishes
   the images under `sha-<commit>` tags; a `node24` and a `python312`
   function boot on n2 metal against those digests before any release
   carries them. Building the images for a pre-merge boot needs an e2e
   bundle from the branch, which the n2 host and the developer machine
   could not hold on 2026-10-24.
3. Release, once the n2 boot passes. New function deploys and
   shared-runtime Node apps use Debian 13.
4. Audit `base-minimal` apps (step 3); PR moving `base-minimal`; release.
5. The next nightly cve-check shows zero criticals on all four images; close
   the Debian 12 part of #5206.

### Rollout record

- **Step 2 n2 boot (2026-10-24):** `TestMetalRestoreUserspaceRNGDiffers`
  passed 4/4 on the published `runner-node24@c1fc7466` and
  `runner-python312@aa3ae08a`. Both booted in Firecracker with guest-init on
  Debian 13 (glibc 2.41), and two restores of one snapshot served different
  userspace RNG values.
- **Step 3 audit (2026-10-24/25):** production had 114 live deployments on
  `base-minimal` (96 source, 12 Dockerfile, 6 image), all in two
  platform-owned accounts; 78 belong to active or evicted-cold apps and 36 to
  deleted apps. On `gregale-compute-1`, each layer was fetched from the
  artifact bucket, mounted read-only (`ro,noload`), and every ELF binary's
  `DT_NEEDED` list was compared with the 13 removed libraries, ignoring any
  library the layer ships itself. All 78 active layers were scanned; none
  needs a removed library. Of the deleted apps' layers, 21 were scanned
  clean and 3 no longer exist in any store.
- **Step 3 image:** on Debian 13, `base-minimal` declares
  `libc6 2.41-12+deb13u4` and its scan shows no criticals. Its loader link
  `/lib64/ld-linux-x86-64.so.2` is relative in Debian 13, so it resolves to
  the copied `/lib/x86_64-linux-gnu` loader.

## Consequences

- Closes the Debian 12 criticals and the `base-minimal` scanner blind spot.
- Runtime and builder use the same Debian release, which removes the
  `GLIBC_2.38 not found` failure for natively compiled dependencies.
- Existing functions stay on Debian 12 until redeployed; their old base
  generations stay in the release catalogue meanwhile.
- One cold-boot wave per node for logical-base apps at each of the two base
  releases.
- Unfixed Debian highs and mediums drop by about a third but do not reach zero.
