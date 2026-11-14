# ADR-20261105131434: Retained management MCP scaling outcome recovery

Date: 2026-11-05
Status: Accepted
Related: ADR-20261104104521, ADR-20261105000513, ADR-20261026152009

## Context

A reviewed scaling apply uses a deterministic native request ID and a revision
guard. The native API retains a receipt, so an unexpired plan can recover from a
lost response. The MCP plan expires after 15 minutes, however, and its receipt
reader cannot recover the original request after expiry. Agents need a way to
distinguish a verified historical commit from an unknown outcome without
repeating the operation or treating present configuration as historical proof.

## Decision

Retain bounded, connection-local `operation_scale` action history and an
encrypted outcome reference in separate namespaces. Persist the reference with
the attempted plan and history before the native write. The reference contains
the original request ID, expected revision, a digest of the expected resulting
settings, and an immutable target/retention binding. It contains no replayable
request body. Existing unexpired attempted plans can use their deterministic
original identity; already-expired legacy plans cannot be reconstructed.

`gregale_get_action_outcome` reads the exact native receipt under current
connection, app and environment consent. It verifies the app and environment
identities, scope, request ID, previous and resulting revision, commit time and
settings digest. Missing or mismatched evidence remains unresolved. A verified
receipt represents a historical configuration commit, not current settings,
runtime convergence or health. An explicit `gregale_get_app` observation
inspects current configuration separately. Reads do not rewrite history, replay
mutations, extend expiry or grant retry authority.

The history links an outcome-only recovery recipe for attempted scaling plans;
it does not advertise saved-plan review or check tools that do not support
scaling. History, references and active plans share atomic persistence and the
existing history capacity, retention and grant-revocation lifecycle. Separate
namespaces let older bridges preserve unfamiliar records during rolling upgrades.
Authorization and reference bindings are checked again before output release.

The performance prompt and error-group result also identify when the connection's
selected catalog omits diagnostic drill-down tools. Changing toolsets remains
subject to operator policy and current diagnostic consent.

## Validation

Exercise lost responses across encrypted restart and plan expiry, unchanged
single-write behavior, missing and mismatched receipts, grant and immutable app
isolation, final read guards, grant revocation and local/hosted catalog limits.
Test both supported MCP protocol versions. Check catalog guidance with core,
diagnose and combined toolsets.
