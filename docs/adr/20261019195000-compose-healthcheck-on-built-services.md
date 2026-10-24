# Compose healthchecks on source-built services

Date: 2026-10-19
Status: Accepted
Amends: ADR-682 (Compose healthchecks applied to prebuilt images only)
Related: ADR-683 (image healthcheck readiness), ADR-684 (image healthcheck runtime recovery), ADR-1337 (Compose key reporting)

## Context

ADR-682 applies a Compose `healthcheck` to a prebuilt image: imaged merges the
partial override onto the image's own HEALTHCHECK, the result is frozen per
deployment, and ADR-683/684 require a fresh command check before readiness and
recover the instance when it keeps failing. A service with `build:` got none of
this; its healthcheck was warned and dropped. Its Dockerfile `HEALTHCHECK`
reaches the guest manifest, but the scheduler never enforces it, because the
readiness gate is keyed to image deployments.

## Decision

A Compose `healthcheck` on a source-built service is applied and enforced as it
is for a prebuilt image.

- **Scan and app.** The scan validates the healthcheck for built services too,
  and reconcile stores it as the app's `project_image_healthcheck`, so a change
  is an ordinary `image_healthcheck` app update.
- **Frozen per deployment.** Enqueue freezes the override in the deployment's
  inferred profile (`image_healthcheck`), next to the Dockerfile path and build
  options, so a queued build uses its plan's healthcheck.
- **Applied to the built image.** imaged merges the frozen override onto the
  built image's HEALTHCHECK with the prebuilt-image rules (partial overrides
  inherit, `disable: true` is `NONE`), then records whether the result is a
  required command check (`image_healthcheck_required`) through the existing
  profile writer, which now accepts a source deployment that froze a Compose
  healthcheck.
- **Enforced.** The scheduler requires the check for such a deployment exactly
  as for an image deployment: a fresh pass before readiness, and recovery from
  repeated failures.
- **Unchanged otherwise.** A source build without a Compose healthcheck keeps
  today's readiness, even if its Dockerfile declares a `HEALTHCHECK`. Enforcing
  Dockerfile healthchecks on every source build would change readiness for
  deployments nobody reconfigured; that is a separate decision.

## Consequences

- Built Compose services get the readiness gate and recovery their file
  describes. A failing check now blocks promotion instead of being ignored.
- A stage retry copies the failed deployment's inferred profile, so it keeps
  the frozen override along with the Dockerfile path and build options.
  (An earlier revision of this record said retries lost it; they do not.)
- An older scheduler reads the verdict only for image deployments, so during
  rollout a built service's check reaches the guest but is not enforced, which
  is today's behavior.
- Native qualification of the gate on a built service is pending with the other
  ADR-683/684 native lanes.
