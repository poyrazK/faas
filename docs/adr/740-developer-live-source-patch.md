# ADR-740 · Developer live source patches

- **Status:** proposed
- **Date:** 2026-10-08
- **Decision:** For eligible `gregale dev` environments, apply a bounded source
  delta to the already-running developer instance and restart its process,
  while the normal developer build continues and supersedes the patch when it
  becomes live. The patch is delivered via a guest-initiated long-poll on the
  existing runtime-config vsock channel; it never changes the immutable
  artifact, snapshot, or production path.
- **Why:** Every developer save is a full build → boot → readiness → route
  cycle (ADR-153..159). Caching keeps a cached edit near the 15 s target, but
  interpreted-source edits that touch no dependency or build input cannot get
  much below that while a build is on the critical path. Developers expect
  roughly 1 s save-to-reload for that edit class.

## Context

ADR-154 already transfers only changed archive entries from the CLI to apid
and reconstructs the full source server-side. ADR-155 keeps one in-flight
developer deployment and cancels obsolete ones. guest-init already holds a
guest-initiated vsock stream to vmmd on `VsockRuntimeConfigHostPort` (1031) for
live env/secret configuration, and already restarts the supervised workload
process in place when a sealed-secret generation changes. The listener is bound
per instance, so the stream itself authenticates the app and account.

Constraints that rule out the obvious designs:

- No shared host directories with guests; block devices only (spec §11).
- Builds run only inside builder microVMs (ADR-003). A patch must therefore
  never run a build step or a dependency install; it may only replace files
  that the last real build copied verbatim.
- Snapshots are cache, the artifact is truth (ADR-005, invariant 3). A patched
  instance has diverged from its artifact and must never become one.
- apid must not call vmmd; components communicate via Postgres rows +
  `pg_notify` or their owned gRPC sockets.

## Decision

### Eligibility (decided by the server, reported to the CLI)

A developer deployment is patch-eligible only when all of these hold:

1. The app is a developer session (`preview_pr_number = 0`, a developer
   `preview_of_slug`). Production apps, PR previews, and named environments
   are never eligible.
2. The builder recorded a **verbatim source map** for the live deployment: the
   archive prefix that was copied unchanged into a single image directory
   (e.g. Railpack Node/Python with no build command, or a Dockerfile whose last
   source `COPY` is not followed by a `RUN` that reads it). The map is emitted
   by `builderd` from inside the builder VM as build metadata, never inferred on
   the host.
3. The delta touches only paths under that prefix, contains no lockfile,
   manifest, Dockerfile, `.gregaleignore`, or build-input path from the
   runtime profile, and stays within `DeveloperPatchMaxBytes` /
   `DeveloperPatchMaxEntries` (new per-plan values in `pkg/api/limits.go`).

Anything else falls back to today's build path with no behavior change.

### Delivery

1. The CLI keeps uploading the ADR-154 delta. apid, the only writer of customer
   intent, validates eligibility and inserts a `developer_source_patches` row
   (app id, base deployment id, monotonically increasing generation, sanitized
   entries + deletions as a bounded bytea, digest, expiry), then `pg_notify`s.
   It then starts the normal developer deployment exactly as today.
2. vmmd's runtime-config receiver gains a `dev_patch_wait` request kind. guest-init
   in a developer instance holds one long-poll with its current generation;
   vmmd answers when a newer row for the instance's app *and* base deployment
   exists, or with `unchanged` on a bounded timeout. Instances of a different
   deployment never receive the patch.
3. guest-init applies the entries into the verbatim directory on the writable
   upper layer as uid 1000: no absolute paths, no `..`, no symlinks or
   hardlinks, no device files, setuid/setgid bits cleared, modes masked to
   0755/0644. Application is staged and renamed per file; a failed apply
   reports an error code and leaves the instance unpatched.
4. guest-init restarts the workload via the existing supervised restart path
   (or sends `dev.reload_signal` when declared, for frameworks with their own
   in-process reloader), waits for the existing readiness probe, and acks the
   generation. The CLI surfaces the ack as `patch=0.6s · live`.

### Convergence and lifecycle

- The real developer build always continues. When it becomes live, it
  supersedes the patch; the patch row for the old base deployment is retired.
  Correctness therefore converges to the built artifact on every save.
- schedd must not capture a snapshot from an instance that has acked any
  patch generation; such an instance is marked `source_diverged` and is
  discarded on park. A restored or cold-booted instance asks for the latest
  generation of its base deployment before readiness and re-applies it.
- Patch rows expire with the developer lease and are pruned by the existing
  preview janitor.

## Consequences

- Interpreted-source edits on eligible apps go live in about 1 s instead of a
  full build cycle; compiled runtimes, dependency changes, and non-verbatim
  builds are unchanged.
- A new guest-init request kind, a new vmmd receiver branch, a new apid table
  and migration, builder metadata, and schedd's snapshot-capture guard. guest-init
  remains the only in-guest writer.
- Metal tests must prove: snapshot capture is refused after a patch; restore
  re-applies the generation; path-escape and oversize patches are rejected;
  `leakcheck` stays clean.
- New limits go in `pkg/api/limits.go`; metrics `dev_patch_apply_seconds` and
  `dev_patch_rejected_total{reason}`.

## Rejected alternatives

- **Host directory share (virtiofs / 9p).** Violates the block-devices-only rule.
- **Run a file watcher such as nodemon inside the VM against a synced disk.**
  Still needs a host-to-guest file channel. It also gives up server-side
  eligibility, size limits, and path confinement.
- **Hot-swap a fresh block device per save.** Requires vmmd drive hot-plug and
  remount in the guest for every edit, which is heavier than a bounded
  in-guest file write.
- **Skip the build when eligible.** This would make a patched instance the only
  copy of the source, which breaks ADR-005. Keeping the build makes the patch
  purely an accelerator.

## Rollout

1. Builder verbatim source map + apid eligibility check, reported in
   `--json` receipts only (no delivery). *Implemented:* guest-init classifies
   the plan `railpack prepare` wrote (`pkg/devpatch`), builderd stores it in
   `build_provenance.dev_patch` (also through build-cache hits), and apid
   returns a `dev_patch` preview on developer uploads, which `gregale dev
   --json` copies into each `developer_sync` receipt. Railpack v0.38.0 plans
   are verbatim for Node without a `build` script and for Python with
   `requirements.txt`; Node with `npm run build` and uv projects (which run
   `uv sync` over the full source) are not.
2. vmmd/guest-init delivery behind an operator flag, with the schedd
   snapshot guard and metal tests. *Implemented behind
   `FAAS_DEV_PATCH_DELIVERY=1`, which must be set on both apid and vmmd;
   metal tests and `leakcheck` remain to be run on the native hosts.* The
   implementation refines the decision above in four places:
   - **Patches are cumulative.** A developer delta is relative to the
     previous sync, not to the live build: a sync whose build was cancelled
     would otherwise leave a gap. apid records every developer deployment's
     full source manifest (`dev_source_manifests`) and each patch is the
     difference between the live deployment's manifest and the newest
     reconstructed source. A live build without a manifest reports
     `no_base_manifest`.
   - **Short poll instead of a long-poll.** guest-init asks once a second
     with the last generation it applied. vmmd answers `dev_patch_disabled`
     when the flag is off or the app is not a developer app, which ends the
     loop after one request for every other workload.
   - **Instances are marked when a patch is served,** not when the guest
     acks, so a crash mid-apply cannot leave a partially patched instance
     unmarked. `PauseAndSnapshot` destroys a marked instance and returns
     `dev_source_diverged`; schedd records the park as STOPPED with that
     reason, outcome `source_diverged`. `WarmSnapshot` refuses before
     pausing and the scheduler's warm-failure path destroys the VM.
   - **Restart and scope.** guest-init applies a patch with `os.Root`
     confinement under `/app` (no `..`, no symlink traversal, regular files
     only, modes masked, staged renames), then restarts the main workload
     outside its restart policy and crash budget. Sidecars are never
     patched. A patch that fails to apply is skipped; the next sync
     publishes a newer generation. An instance restored from the
     deployment's snapshot runs the unpatched source for up to one poll
     interval before re-applying the newest patch.
3. CLI `patch` phase in the timing summary and `dev history`; docs.
