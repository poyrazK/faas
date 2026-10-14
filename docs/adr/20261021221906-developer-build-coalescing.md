# ADR-20261021221906 · Coalesce developer builds behind live patches

- **Status:** proposed
- **Date:** 2026-10-14
- **Decision:** When a `gregale dev` sync reaches the running instance as a
  live patch (ADR-740), its convergence build waits until the developer stops
  saving. The build is queued with `builds.not_before` set to the end of a
  coalescing window (`api.DevBuildCoalesceWindow`, 3 minutes); every claim
  path skips it until then; the next sync cancels it before it runs and
  queues its own. A developer who saves twenty times in a burst gets one
  build after the last save instead of twenty.
- **Why:** each save already reaches the developer through the patch, yet
  every save also started a full developer build. The control plane
  guarantees one builder slot, so a few active developers kept it busy with
  builds nobody was waiting for and queued real deploys behind them. Each
  of those builds then went live and replaced the patched instance, which
  restarted the development server and dropped its in-memory and hot-reload
  state for no visible gain.

## Context

ADR-740 delivers a cumulative source patch to the live developer instance
and keeps the normal developer build running so the environment converges
on a built image: patched instances are never snapshotted, and a later cold
boot needs an image that contains the edits. Two facts make the build
deferrable:

- A booting developer instance asks vmmd for the newest cumulative patch of
  its deployment (`cmd/vmmd/runtime_dev_patch.go`), so a cold boot or wake
  of the live deployment replays every published edit without a build.
- Published patches live for `DevSourceCacheTTL` (24 hours), far longer than
  any coalescing window.

What was missing is a way to hold a queued build back and to drop it when a
newer sync makes it obsolete. `CreateDeployment` supersedes only pending
deployments, and a deployment whose build is queued is already `building`,
so each sync's build would otherwise run.

## Decision

1. `builds.not_before` (nullable). Every claim path — the notification
   claims by ID, the polling claim, the fairness and node-affinity claims —
   skips a queued build whose `not_before` is in the future, and the queue
   gauge leaves it out. `NULL` keeps today's behaviour for every other build.
   The column is written by the same insert that creates the build, so no
   claim can take it early. builderd's 2-second poll picks it up once due.
2. apid defers the build of a developer-source sync when the sync is
   eligible for a live patch, has changes, delivery is on, and the CLI did
   not ask to build now (`dev_source_build_now`, sent by `gregale dev
   --once`). If the patch is then not published, the build starts at once.
3. After every developer-source sync is enqueued, apid cancels the app's
   other deployments whose build is still deferred
   (`CancelDeploymentTx`, reason `system`): the new sync carries their source.
4. When the CLI reads a patch status of `failed`, apid starts the app's
   deferred builds at once: the edit did not reach the instance, so the
   build is what delivers it.
5. The upload response's `dev_patch.build_deferred_until` tells the CLI
   that the patch is the sync. `gregale dev` reports the sync live when the
   patch applies (or, with no running instance, when it is published) and
   leaves the deployment queued; a failed patch hands the sync back to the
   build, which the CLI follows as before.

`FAAS_DEV_BUILD_COALESCE_SECONDS` overrides the window for tests; `0` turns
coalescing off.

## Consequences

- One developer build per burst of saves instead of one per save; the
  development server keeps running across the burst.
- The environment converges a few minutes after the last save rather than
  after each one. Until then, a wake that cold-boots replays the patch, and
  the patched instance is not snapshotted, so wakes in that window are
  cold boots (ADR-005 is unchanged).
- `gregale dev --once` and every sync that is not a live patch (dependency
  or lockfile changes, Dockerfile apps, delivery off) build at once.
- `apid_dev_build_deferrals_total{outcome}` counts deferred builds, builds
  started early, and builds superseded before they ran; the last is the
  number of builds saved.

## Rejected alternatives

- **Skip the convergence build entirely while a session is active.** Wakes
  would cold-boot and replay a growing patch for hours, and a session that
  ends without another sync would never converge.
- **Debounce in the CLI.** Only the CLI that started a build could cancel
  it; a second terminal, a restart or a crashed CLI would leave every build
  running.
- **Let builderd drop builds of superseded deployments when it claims
  them.** The waste is the builder slot and the instance swap; a build that
  is never claimed is the only one that costs nothing.
