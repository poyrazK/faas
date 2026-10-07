# ADR-640: Freeze Compose commands per image deployment

- **Status:** accepted
- **Date:** 2026-10-07
- **Decision:** Capture the accepted Compose CMD contract in the deployment's
  existing `inferred_profile` JSONB before publishing a claimable image row.
  The optional `image_command` object has its own `v1` version and a `cmd`
  field. A present object with `cmd: null` explicitly inherits OCI CMD; a
  non-null array replaces CMD while preserving the artifact's ENTRYPOINT.
- **Why:** Pinning an image digest does not pin the command used to run it.
  Previously, imaged read `apps.manifest.project_image_command` at processing
  time. A Compose edit or transition to a source build could therefore change
  the command of an already queued image or a retry of an older image.
- **Admission:** Project apply and GitHub image enqueue capture the command
  alongside the declared image and selected port from their accepted app
  metadata. JSON image admission, including image-published triggers, captures
  the command for apps configured with a project image. A new deployment reads
  the current declaration; an authenticated delivery replay retains the first
  accepted record.
- **Materialization:** Imaged uses the captured command before combining OCI
  ENTRYPOINT and CMD. Explicit deployment overrides keep their existing
  precedence. The captured image semantics survive removal of the app's
  project image declaration. Unsupported or malformed captured command
  contracts fail materialization instead of consulting mutable app metadata.
- **Persistence:** The existing deployment profile is copied by retries,
  promotions, and environment clones. Runtime port updates preserve the
  existing `image_command` atomically and cannot introduce or replace it.
  PostgreSQL uses the generated sqlc update; MemStore applies the same rule.
  No schema migration or public API/SDK field is introduced.
- **Compatibility:** Deployments without this optional record keep the legacy
  command behavior; historical accepted commands cannot be reconstructed.
  This change freezes accepted command semantics, not all app configuration.
  Compose command parsing, scoped environment values, secret rotation, security
  admission, and lifecycle controls retain their existing contracts.

This extends [ADR-638](638-compose-prebuilt-image-workloads.md) and the
[ADR-639 CI handoff](639-image-published-deployment-trigger.md) without adding a
VM lifecycle or executing customer commands at admission.
