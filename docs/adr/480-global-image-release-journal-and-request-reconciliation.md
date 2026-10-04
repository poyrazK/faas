# ADR-480: Global image release journal and request reconciliation

Date: 2026-10-04
Status: Accepted; request-driven implementation.

## Context

The directory binds one global app to independently authorized regional apps.
ADR-478 receipts make retries safe and ADR-479 fences delayed older intent.
A global release still needs a durable generation, an immutable target snapshot,
and recovery from partial regional acceptance and lost responses.

## Decision

The directory apid accepts `PUT /v1/global-apps/{slug}/image-releases/{release_id}`
with a digest-pinned image and request-only regional account-admin keys indexed
by region. The release UUID is canonical and chosen by the caller. Allocation
locks the global app and snapshots every selected, currently verified target,
its regional owner/app UUID and the exact cell origin/generation. A release
with no targets, too many targets, or any stale binding is rejected atomically.
The desired generation increases once per new release. Repeating the same UUID
and image returns the original generation and snapshot; changing its image
conflicts. Attachment changes affect future releases only.

Each target has a durable pending/accepted/expired/blocked outcome and a closed,
sanitized diagnostic code. The coordinator checks the current desired generation
and current binding/registry before network I/O and before recording results.
It verifies regional ownership using only that region's supplied key, reads the
regional receipt, and issues the same ordered PUT only if the receipt is absent.
A timeout remains pending: resuming the same request after a directory restart
reads the regional receipt and cannot create a second deployment. Receipts must
match the full global/regional identity, image and generation. Redirects are
refused and request duration, response bytes and fan-out are bounded.

A newer global generation supersedes older coordinator work. Late responses
cannot publish an old release as current. A detached or changed identity blocks
that snapshot; a retry never follows a changed origin or adopts a new app.
Matching release retries may be inspected without resupplying regional keys;
only pending targets need keys to resume. Keys are never persisted, returned,
forwarded between regions or included in audit data or remote diagnostics.
Global authorization still uses the directory's existing personal account and
regional authorization still uses independent regional keys. This is explicit
request-driven recovery, not autonomous background deployment. Scoped, sealed
regional delegation is a subsequent change before unattended reconciliation.

The routing policy remains `all_selected_regions`. `accepted` means every
snapshotted region durably accepted image intent; it does not assert readiness,
health, global quota reservation, simultaneous cutover or ingress publication.
Already serving regions have no directory dependency on request/wake paths.
The new journal is directory backup state; it cannot be removed by downgrade
while populated. Existing regional ownership transfer/stream retirement rules
remain in force.

## Validation

Exercise concurrent generation allocation, immutable snapshot replay, target
changes, all-or-nothing allocation, SQL mutation/downgrade guards, store restart,
partial acceptance, response loss after regional commit, ownership mismatch,
redirect refusal, malformed/mismatched receipts, secret exclusion and delayed
older workers across two real authenticated regional APIs. Run PostgreSQL and
portable race checks; native KVM recovery, global quota, health and ingress remain
required for the complete multi-region feature.
