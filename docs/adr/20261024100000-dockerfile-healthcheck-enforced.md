# Dockerfile HEALTHCHECK gates readiness on source builds

Date: 2026-10-24
Status: Accepted
Amends: ADR-20261019195000 (Compose healthchecks on source-built services)
Related: ADR-683 (image healthcheck readiness), ADR-684 (image healthcheck runtime recovery), ADR-682 (Compose image healthchecks)

## Context

A prebuilt image's HEALTHCHECK gates readiness and drives recovery (ADR-683,
ADR-684). A source build gets the same treatment only when its Compose service
declares a `healthcheck` (ADR-20261019195000). A `HEALTHCHECK` in the
Dockerfile alone reached the guest manifest and was polled, but the scheduler
ignored the result. The same image therefore behaved differently depending on
whether it was pushed to a registry or built by Gregale. That ADR left
enforcing Dockerfile healthchecks as a separate decision, because it changes
readiness for deployments nobody reconfigured. This record makes that decision.

## Decision

A source build's effective command HEALTHCHECK gates readiness and drives
recovery, whether it comes from the Dockerfile or a Compose override.

- **Verdict for every source build.** After assembling a built image's
  manifest, imaged records whether its effective HEALTHCHECK is a command check
  (`CMD` or `CMD-SHELL`) as `image_healthcheck_required` in the deployment's
  profile. It merges the field into the profile as stored, keeping fields it
  does not know, and starts a minimal profile when inference left none. A build
  without a command check writes nothing, unless its profile carried a verdict
  copied from an earlier attempt, which it clears.
- **Store.** The runtime-profile writer accepts that verdict for any deployment
  still pending, building or imaging. It no longer requires an image
  deployment or a frozen Compose healthcheck.
- **Scheduler.** A source deployment follows the verdict as an image deployment
  does: a fresh passing check before readiness, and recovery after repeated
  failures. A source profile that is missing or malformed still means ordinary
  readiness, so deployments assembled before this change keep theirs until
  they are redeployed.
- **Opting out.** `HEALTHCHECK NONE` in the Dockerfile, removing the
  instruction, or `healthcheck: {disable: true}` on a Compose service keeps
  ordinary readiness.

## Consequences

- The next deployment of a source build whose Dockerfile declares a command
  HEALTHCHECK waits for that check to pass before serving, and a check that
  keeps failing restarts the instance. A check that cannot run in the image (a
  missing `curl`, a wrong port) now blocks the deployment instead of being
  ignored. Release notes must say this, with the opt-outs above.
- Running deployments and their snapshots are unaffected until redeployed.
- Release skew: an imaged from before this change records no verdict for these
  builds, and a scheduler from before it ignores one, so either side running the
  previous release keeps today's readiness. Rolling imaged back stops new
  verdicts; deployments that already have one keep it until redeployed.
- Railpack builds declare no HEALTHCHECK and are unaffected.
- Native qualification of the gate on a built image is pending with the other
  ADR-683/684 native lanes.
