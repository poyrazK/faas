# ADR-158: Explicit ephemeral disk boundary

Status: accepted

Date: 2026-09-06

## Context

Gregale runs stateless workloads in microVMs. The main `drive1` image is a
writable ext4 upper layer, while `/tmp` is tmpfs and sidecar images are
read-only. The image builder already rejects a padded app layer above the
plan's `AppLayerMaxMB` value, but the API described that boundary only with the
implementation-oriented name `app_layer_max_mb`. Customers could not tell from
the limits response how much ephemeral runtime disk their app receives, and
the name could be mistaken for a durable storage allowance.

## Decision

Treat the existing plan app-layer cap as the explicit ephemeral disk ceiling.
Expose it as `ephemeral_disk_max_mb` on account limits and app effective limits.
Keep `app_layer_max_mb` and the `app_layer_too_large` problem code for wire
compatibility. The API reports the plan ceiling; it does not claim to provide a
live in-guest free-space meter.

No new quota column or migration is introduced. `EphemeralDiskMaxMB` and
`EphemeralDiskMaxBytes` alias the existing `AppLayerMaxMB` value so image-build
enforcement and API reporting cannot drift. Persistent customer volumes remain
out of scope; durable state belongs in object storage or an external database.

## Consequences

Customers can size ephemeral scratch and extracted assets against a named
storage limit, while existing clients continue to decode the legacy field.
Build failures identify both the app-layer build cap and its runtime-disk
meaning. Future live usage telemetry can add observations without changing the
quota contract or introducing a second source of truth.
