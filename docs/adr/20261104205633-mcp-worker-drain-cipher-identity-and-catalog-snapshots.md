# ADR-20261104205633: MCP worker drain budgets, cipher identity and catalog snapshots

## Status

Accepted; extends ADR-20261104201707 and ADR-20261104195018.

## Context

Native worker handoffs used the candidate configuration and a 30-second fallback
to judge previous workers' stop grace. Node actually defaults to 10 seconds and
honors an environment override, and the previous generation may have different
configuration. Go and Python rewrapping selected payloads by envelope version
and key ID even though native and explicit v2 formats can share that ID while
using different KDFs and AAD. Node catalog registration retained nested metadata
references, allowing changes without revisions or subscription notifications.

## Decision

1. Node worker startup events include `shutdownTimeoutMs` from the resolved
   runtime settings. Native handoffs read current-generation, per-instance
   startup receipts and require stop grace to exceed their maximum budget.
   Repeat validation before promotion, immediately before each previous-worker
   park and during candidate retirement. Reject missing, invalid, conflicting
   or incomplete receipts instead of guessing from candidate configuration.
   Older worker sources need updated receipts before native handoff.
2. Rewrapping authenticates v2 fields with the active cipher, including its KDF
   and AAD, and preserves ciphertext that is already current. Overlapping key
   IDs use keyset passes of at most eight rows per batch to bound field buffers; a fixed upper bound and a wrap at the end keep
   earlier current rows from starving later native payloads and revisit skipped
   locks. Cursor progress is local, serialized and committed only after the
   batch succeeds. Distinct IDs retain the metadata-based selection path.
   Rewrapping changes no Task status, timestamps or notifications, and ownership
   key retention remains required independently of payload conversion.
3. Node catalogs snapshot plain nested metadata and JSON schemas when entries
   are registered or replaced, and return independent metadata copies to readers,
   policy callbacks and definition normalizers. SDK schema objects retain their
   immutable APIs and prototypes. Cyclic metadata is rejected before a catalog
   mutation. Explicit replace APIs remain the path for observable changes.

The startup receipt field is additive. No platform schema, quota, Task wire
format or encryption-envelope changes are introduced.

## Validation

Native tests cover default, overridden and equal budgets, missing and conflicting
receipts, multiple replicas and grace changing between promotion and park.
Go and Python tests cover every encrypted field, same-ID format migration,
already-current prefixes, locked rows, distinct-ID fast paths, repeat passes
without ciphertext churn and transaction/cursor rollback on authentication
failure. Node catalog tests cover retained input references, reader and policy
callback mutations, immutable SDK schemas, cyclic metadata and matching pins
remaining healthy until explicit replacement.
