# ADR-20261102133056: runner-node22 moves to Debian (glibc)

- **Status:** accepted
- **Date:** 2026-11-02
- **Amends:** ADR-053 (Node 22 was the standalone Alpine runtime),
  ADR-20261029103512 decision 2 (the reason Node 22 source apps keep their
  own base)
- **Related:** ADR-736 (immutable runtime releases), ADR-510 (snapshots
  restore only onto their own base bytes), ADR-1500 (shared runtime for
  source apps), spec §4.6, §4.9

This record has a date-and-time identifier because numbered ADRs collide
when parallel changes pick the next number.

## Context

`runner-node22` is built on `node:22-alpine` (musl). Every other Node path is
glibc: Railpack's mise Node, the native addons it compiles, and
`runner-node24` on `node:24-trixie-slim`. Two things follow:

- **Node 22 source apps cannot use the shared runtime.** The first
  acceptance run of ADR-20261029103512 failed on the musl base
  (`node: cannot execute: required file not found`), so
  `sharedruntime.MuslRunner` keeps every Node 22 source app on its own base.
  Its layer carries the toolchain (about 369 MB on `base-minimal`), which
  fails Free's 256 MB layer cap; the fix copy tells customers to move to
  Node 24.
- **A Node 22 function cannot load a native addon.** A function build runs
  `FROM` its runner base, but Railpack installs dependencies with its own
  glibc Node (ADR-20261029103512), so a compiled `.node` file cannot load on
  the Alpine runner.

## Decision

1. **`runner-node22` builds on `node:22-trixie-slim`,** digest-pinned through
   `images/Dockerfile.lock`, with the same steps as `runner-node24`
   (apt-installed build tools for the profiling and tracing modules, purged
   afterwards; `apt-get upgrade`; npm and corepack removed; uid 1000 renamed
   from `node` to `app`). The function runner shims are static Go binaries
   and run unchanged.
2. **Node 22 stays a full-chain base** (no `ParentRef`). Its pinned trixie
   layer is not guaranteed to match `base-debian-parent`'s, and composing it
   over the parent is a separate change.
3. **The change ships in two PRs.** This one changes the image only. The
   `MuslRunner` rule and guest-init's skip of Railpack's default apt step for
   `node22` stay until a release pins the Debian digest in
   `FAAS_DEPLOY_BASE_REF_NODE22` everywhere. Units release independently
   (docs/release-compatibility.md): before that pin, a node can still stage
   the Alpine ref, and a builder that already offered Node 22 the shared
   runtime, or ran the apt step, would fail there. The second PR removes both.
4. **`agent-base` stays on `node:22-alpine`.** Its harnesses ship musl builds
   and its memory figures were measured on that base; it only no longer
   shares a digest with `runner-node22`.

## Consequences

- **Live Node 22 functions keep their exact base.** imaged binds every
  function build that records its runtime base ref to an immutable runtime
  release (ADR-736), and schedd boots the release's content-addressed key
  (`base/releases/runner-node22-<arch>-<id>.ext4`), not the logical
  `base/runner-node22.ext4`. Production-us on 2026-11-02 (read-only query):
  all 6 deployments of active `node22` apps are live functions, and all 6 are
  bound. Unbound rows exist only on deleted apps.
- **An unbound artifact would boot Debian.** A build from before ADR-736
  (2026-10-09) has no binding and cold-boots the logical base, which this
  change replaces. A pure-JavaScript function still runs; one with a
  musl-compiled addon would not. Its snapshots refuse to restore onto the
  new bytes (ADR-510) and it cold-boots. Redeploying rebuilds it on Debian.
- **New Node 22 function builds can use native addons,** since the addon and
  the runner now share glibc.
- **Moving an existing function to the new base is a rebuild.** An explicit
  runtime update (ADR-737) rebuilds the same source bytes against the
  target release, so addons are recompiled for glibc.
- **The base is larger than the Alpine one.** It is a drive0 base, staged
  once per node and counted in the base reserve, not per app (spec §4.6).

## Validation

- `make images-lock-check` passes with the new pin.
- CI's image workflow builds `runner-node22`, runs
  `validate-runtime-image.sh` and `smoke-runtime-image.sh` (which already
  treat `runner-node22` and `runner-node24` the same) and the fixed-only
  critical Grype gate.
- Before a release pins the digest: the Node 22 function metal tests on the
  native KVM host, then `make leakcheck`.
