# ADR-1350: Revisioned restoration of environment cache inheritance

Status: Accepted

## Context

ADR-1348 distinguishes missing inherited policies from explicit overrides,
including empty cache-disabled overrides. ADR-1349 acknowledges positive
policy revisions in current gateway sessions. Users need a scoped way to
restore live app cache inheritance with the same review and recovery guarantees.
Deleting the override would reset its revision and lose propagation evidence.

## Decision

Add an optional `inherited` flag to the existing guarded native PUT request.
True requires an explicit empty rules list. False or omission retains existing
replacement semantics. Store inheritance in a revisioned policy row with a
non-null boolean and an inherited-implies-empty database check. Missing rows
still report inherited revision zero; restored rows have positive revisions
and an updated timestamp. Never reset the counter when switching modes.

Reuse existing account/app/environment locking, immutable identity checks,
expected-revision guard and atomic request-hashed receipt. Omit the false flag
from Go JSON so existing request hashes and persisted reviews remain valid.
The true flag participates in request hashes, preventing a restoration receipt
from authorizing a cache-disabled override with the same ID. Replay preserves
the original result even after later writes. Registry deletion still cascades
policy, receipt and observation state.

Add separate MCP plan/apply inheritance tools. Bind the action, identity,
reason, expected revision and inherited flag into the review and encrypted
plan journal. Check current environment write consent before apply and before
returning a saved result. The override and inheritance apply tools cannot
consume each other's plans. Planning is read-only and reviews expire after
15 minutes. App rule bodies are neither returned nor frozen by this review:
inheritance deliberately follows live app-owned rules at gateway reload.

Gateway overlay evaluation leaves inherited rule semantics intact and partitions
cache-rule IDs by app, environment and restored revision. This prevents old
response objects from reappearing after an override is removed. Copy rule
records before rewriting IDs; never mutate app intent. Existing observation
scans include the positive inherited row and require exact revision capture,
enabled cache-rule compilation count and installed generation/IDs. Failed
compilation/read or a restart cannot reuse an old override acknowledgement.
The existing read-only watch works for restored positive revisions; revision
zero remains unobservable. It confirms inheritance-mode propagation, not a
frozen app policy version or continuous serving attestation.

## Consequences

Environment cache policy now supports override, cache-disabled, and inherited
modes without revision ABA or retry ambiguity. Storage keeps one small row per
configured app/environment. Default hosts, shared intent, other rule kinds,
cloning and manifest ownership are unchanged. Apply the new migration and
upgrade apid and gateways before enabling inherited writes: older readers
cannot interpret the flag. Migration rollback refuses while inherited rows
remain, so operators must deliberately resolve those rows before rollback.
