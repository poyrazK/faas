# ADR-20261105152500: Reviewed configuration rollback guards and outcomes

Date: 2026-11-05
Status: Accepted
Related: ADR-20261103171917, ADR-20261105140140

## Context

An MCP configuration rollback reviews one target revision and the current
configuration revision. A separate read before POST cannot protect the gap
before the native restore reads its own baseline. A lost response also leaves
an agent unable to distinguish a committed rollback from an uncommitted one.

## Decision

Allow reviewed rollback requests to carry the expected current revision and
immutable app and named-environment identities. The native restore checks the
revision, actual configuration and target identities under the same lock as
its writes. A stale or recreated target is refused, including when the restore
would otherwise be a no-op. Guard creation or supersession happens in that
transaction only after the reviewed checks. Legacy native requests keep their
existing behavior. New MCP plans use a distinct guarded operation kind and
never downgrade to an unguarded request; older MCP rollback plans cannot be
applied by the new bridge. An older bridge cannot restore the distinct operation
kind and fails closed instead of replaying it as an unguarded operation. An older
control plane rejects the added request fields before a write.

A new request UUID and 256-bit random proof bind a value-free native receipt to
the reviewed rollback. Its proof digest and receipt commit with the restore,
including a no-op, in the existing bounded configuration receipt store. A
protected GET requires the proof and current app access; named reads recheck
the immutable environment identity. A duplicate request is not replayed; it
returns a conflict directing callers to the original receipt. Receipt
retention follows the native configuration receipt window.

The bridge persists the original request UUID, private proof, expected and
target revisions, and immutable history binding before sending the write. The
encrypted reference contains no variable values. Separate additive
action and outcome namespaces keep older readers from misinterpreting new
history. The existing action outcome tool reads the receipt after plan expiry;
it confirms a historical configuration restore or no-op and does not prove
current configuration, secret restoration, restart or health. Missing evidence
remains unresolved. The read never repeats the rollback or renews authority.
