# ADR-867: Durable entity operational visibility

Status: Implemented; focused developer-workflow CI passed. Native acceptance pending.

## Decision

Add `gregale entities inspect` and bounded, changed-only `watch` commands over the
existing owned-app inspection API. Provide corresponding Node diagnostic helpers
and a scoped handle `observe()` method. Keep wire contracts and storage unchanged.

Reports retain the raw inspection and classify alarm/outbox recovery metadata.
They explain exhausted attempts, unavailable delivery evidence and successful
transport with pending entity acknowledgement. Clock-based due labels are not
stall detection. Unknown transport status cannot establish that no delivery occurred.

Watch emits newline-delimited JSON or readable text on changes, uses explicit
scope selectors and terminates within a configured deadline. Read errors fail
closed. Inspection and watch perform no mutation or automatic recovery.

Ownership, replay receipts and application payloads remain outside this API.
Reports expose these limits rather than deriving undocumented ownership claims.
Invocation results remain the source of committed/replayed outcome information.

## Validation

Author Go classification, CLI routing/watch/error tests and Node diagnostic tests.
The focused developer workflow runs Node coverage; changed-package CI covers Go.
Local tests/builds are deferred per user instruction. Documentation generation and
formatting are permitted source maintenance. Production gates remain unchanged.

Focused CI evidence and native acceptance boundaries are recorded in the
[release qualification report](../durable-entity-release-qualification.md).
