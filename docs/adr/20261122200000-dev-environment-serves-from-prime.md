# ADR 20261122200000 · A developer environment goes live from its prime instance

- **Status:** accepted (owner, 2026-11-26)
- **Date:** 2026-11-22
- **Amends:** the deployment prime (spec §6, `snapshot_prepare`) for developer environments only
- **Related:** ADR-005 (cold boot always legal), ADR-063 (shared authoritative snapshot blob), ADR-740 (live patches), ADR-1170 (watch mode), ADR-20261115213230 (snapshot drive deltas)

This record has a date-and-time identifier because numbered ADRs collide
when parallel changes pick the next number.

## Context

`gregale dev` reports its first sync as sync → build → **boot** → ready. On prod
(bhs-3, 2026-11-22) a Next.js 15 watch session spent 35–40 s in boot. Boot is
the deployment's `snapshot_prepare` stage: schedd's prime cold-boots the
instance, waits for health, captures a snapshot, parks the instance, and only
then promotes the deployment. The first request then wakes the instance from
that snapshot.

The measured boot:

| step | time |
| --- | --- |
| cold boot to process start | ~0.85 s |
| Next ready | 1.1 s |
| first compile, triggered by the health probe | 31.8 s at 512 MB (2.3 s at 2048 MB; #7881 sizes watch environments at 1024 MB) |
| snapshot capture total | 24–26 s |
| · create / export_mem / publish_vmstate | 0.65 / 0.87 / 0.75 s |
| · publish_mem (~745 MB, zstd, to S3) | 11.6–13.4 s |
| · publish_drive (31 MB written; whole drive uploaded because `FAAS_STORAGE_DRIVE_DELTA` is off on prod) | ~10 s |

About 25 s of every first sync, and of every rebuild that is not a live patch
(dependency change, config change), is spent uploading a snapshot that no
request needs yet. The developer is waiting on an S3 upload of a VM that was
already healthy and serving-capable.

A production deployment needs the snapshot before promotion: promotion is when
traffic may land on any node, and ADR-063 makes the shared blob authoritative
so every node can wake it. A developer environment is different:

- it has one user, and at most a few instances;
- it is short-lived (lease-bound) and rebuilt often;
- its first request arrives seconds after `ready`, and a development server
  keeps its compile cache in process memory, which a wake from snapshot keeps
  but a cold boot loses.

## Decision

For a `gregale dev` environment, behind the hot runtime setting
`dev_prime_serves_enabled` (default off). `state.IsDeveloperApp`
(`PreviewOfSlug != ""` and `PreviewPrNumber == 0`) is not enough on its own:
environment branches and rehearsal runs deploy into apps of the same shape.
The gate (`state.DeveloperPrimeEligible`, checked by schedd and again by
imaged) is a developer app's own request or service deployment whose reason
is neither `environment-branch` nor `environment-branch-seed`, that no
environment branch created, of an app that is not a rehearsal member.

1. **The prime instance becomes the live instance.** After the prime's health
   check passes, schedd promotes the deployment and moves the prime instance
   to `RUNNING` as an ordinary serving instance of the new deployment, instead
   of `RUNNING → SNAPSHOTTING → PARKED`. `gregale dev` reports `ready` at that
   point.
   - **Concurrency (invariant 1).** The prime is admitted as a snapshot
     prime, outside serving concurrency. Before it serves, schedd checks the
     app's serving capacity with the same gate a wake uses (including the
     ADR-199 rollout grant when it applies) and converts the reservation to a
     serving one. A rebuild next to the old deployment's one instance at
     `max_concurrency` 1 serves through the rollout grant, the same overlap
     a wake of the new deployment gets today while the old instance drains.
     If there is no room even with the grant, the prime captures and parks
     as today. No overlap beyond the existing gate is allowed.
   - **Billing.** Unchanged. The prime is a RUNNING instance of the app and
     is metered like one during the prime today; it keeps running and keeps
     being metered as the serving instance, and parks at the app's idle
     timeout.
   - **Activation proof.** schedd sends `deployment_ready` with
     `no_snapshot_reason=developer_prime` and the prime's instance id. imaged
     checks eligibility and that the instance is the deployment's RUNNING
     prime; the store's activation transaction re-checks that runtime (node,
     wake, start time) and its configuration fence, as it does for the
     ephemeral-secret proof.
2. **No snapshot is captured on the critical path.** The environment's first
   capture happens at its first idle park, through the normal park path, which
   uploads synchronously as ADR-063 requires before the snapshot row is
   written. Nothing is recorded as a usable snapshot before its blobs are in
   the shared store, so ADR-063's durability rule is unchanged. With clean
   recapture on (the default, ADR-20261115113000) that park does not capture
   the used instance: it queues a fresh boot, capture and park in the
   background, off the developer's path. A manual park leaves the app
   inactive, which clean recapture skips; the next wake then cold boots
   (metal run 2026-11-27).
3. **Cold boot stays the fallback (ADR-005).** Until the first park
   succeeds, the deployment has no snapshot. A second instance (scale-out),
   a restart of the promoted one, or a wake after a failed first park
   cold-boots and pays the dev server's first compile again. For a developer
   environment that costs time, not correctness. A background capture after
   promotion would close the gap; it is deferred until the rollout shows
   whether scale-out happens often in developer environments.
4. **Rollback is unchanged in shape.** A failed health check fails the
   deployment as today, and the previous deployment keeps serving. A
   successful promotion drains the previous deployment's instances as any
   promotion does.
5. **Production, previews of pull requests and environment branches keep the
   existing prime.** They need the snapshot before traffic can land anywhere
   in the region.

## Consequences

- Expected first-sync boot for the measured Next.js app: health plus first
  compile instead of 35–40 s. The first compile at 1024 MB is not measured
  yet; it lies between 31.8 s (512 MB) and 2.3 s (2048 MB). Rollout step 2
  measures it. The ~25 s capture
  moves to the first idle park, when nobody is waiting.
- A rebuilt developer deployment (not a live patch) goes live just as fast.
  `edit_to_live` for such edits drops by the capture time.
- The development server's in-memory compile cache survives into the first
  requests, because there is no restore between health and traffic.
- The first park of a developer environment now pays the capture. Parks run
  off the request path, and a park that fails leaves the instance to be cold
  booted next time.
- Independently of this decision, enabling `FAAS_STORAGE_DRIVE_DELTA=1` on
  prod (allowed by ADR-20261115213230 now that every node runs the decoder)
  cuts ~10 s from every capture, including production primes.

## Rollout

1. Implement behind `dev_prime_serves_enabled`, with a metal test: a developer
   deployment goes live from its prime without a `snapshot_written` event,
   serves, parks on idle with a normal capture, and wakes from it.
2. Enable on prod for internal accounts and compare `gregale dev` boot
   (`dev.phase` timings) and `TestDevLoop`'s `edit_to_live`.
3. Enable for everyone.

## Alternatives considered

- **Asynchronous shared upload for every prime.** Promote once the local
  snapshot files exist and upload in the background. This weakens ADR-063 for
  all deployments: a node that is lost before the upload finishes leaves a
  snapshot row with no blob. Rejected for production; for developer
  environments the decision above is simpler and also removes the
  park-and-restore round trip.
- **Only enable drive deltas.** Saves ~10 s of the ~25 s, and should be done
  anyway (see Consequences), but the memory upload (11–13 s) remains.
