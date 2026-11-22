# ADR 20261122200000 · A developer environment goes live from its prime instance

- **Status:** proposed (needs owner approval before any code)
- **Date:** 2026-11-22
- **Amends:** the deployment prime (spec §7 `snapshot_prepare`) for developer environments only
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

For a developer environment (`PreviewOfSlug != ""` and `PreviewPrNumber == 0`),
behind the platform flag `dev_prime_serves` (default off):

1. **The prime instance becomes the live instance.** After the prime's health
   check passes, schedd promotes the deployment and moves the prime instance
   to `RUNNING` as an ordinary serving instance of the new deployment, instead
   of `RUNNING → SNAPSHOTTING → PARKED`. `gregale dev` reports `ready` at that
   point.
2. **No snapshot is captured on the critical path.** The environment's first
   capture happens at its first idle park, through the normal park path, which
   uploads synchronously as ADR-063 requires before the snapshot row is
   written. Nothing is recorded as a usable snapshot before its blobs are in
   the shared store, so ADR-063's durability rule is unchanged.
3. **Cold boot stays the fallback (ADR-005).** If the promoted instance dies
   before its first park, the next request cold-boots the deployment, as it
   does today for a deployment whose snapshot was evicted. For a developer
   environment that costs one dev-server start, not correctness.
4. **Rollback is unchanged in shape.** A failed health check fails the
   deployment as today, and the previous deployment keeps serving. A
   successful promotion drains the previous deployment's instances as any
   promotion does.
5. **Production, previews of pull requests and environment branches keep the
   existing prime.** They need the snapshot before traffic can land anywhere
   in the region.

## Consequences

- Expected first-sync boot for the measured Next.js app: health plus first
  compile (a few seconds at 1024 MB) instead of 35–40 s. The ~25 s capture
  moves to the first idle park, when nobody is waiting.
- A rebuilt developer deployment (not a live patch) goes live just as fast.
  `edit_to_live` for such edits drops by the capture time.
- The development server's in-memory compile cache survives into the first
  requests, because there is no restore between health and traffic.
- Placement and accounting are the open risk. Implementation must show that
  the prime's admission, capacity ledger entry, ownership row and metering
  become those of an ordinary serving instance after the switch, or move it
  there explicitly.
- The first park of a developer environment now pays the capture. Parks run
  off the request path, and a park that fails leaves the instance to be cold
  booted next time.
- Independently of this decision, enabling `FAAS_STORAGE_DRIVE_DELTA=1` on
  prod (allowed by ADR-20261115213230 now that every node runs the decoder)
  cuts ~10 s from every capture, including production primes.

## Rollout

1. Implement behind `dev_prime_serves`, with a metal test: a developer
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
