# ADR-1258: Account-scoped MCP policy convergence

## Status

Accepted; refines ADR-1238.

## Context

A fleet-wide desired revision can delay one customer's MCP release because of
an unrelated customer's mutation. An app-only revision misses wildcard policies
owned by another app in the same account. Deleted apps cannot supply historical
ownership through a join against current metadata.

## Decision

Capture affected account IDs on each edge-rule ledger mutation, without foreign
keys to mutable account/app rows. A rule transfer records both old and new
owners. Leave historical ownership NULL rather than guessing it from current
app metadata. Preserve the existing commit-order transaction lock.

Account policy status compares the global fresh gateway replay watermarks with
the maximum of the account's latest mutation and the newest unknown-owner
historical revision. This compatibility floor covers all uncertain older
mutations; later known unrelated account changes do not raise it. Expose the
floor as optional `legacy_fleet_revision` on the edge-rule status component.
Keep legacy app scope and explicit fleet diagnostics unchanged.

MCP access audits require the account-scoped status capability. The account ID
comes from the authorized app, never a caller-supplied account selector. The
bounded wait, final fingerprint recheck and atomic stored-policy condition stay
in place. No change to gateway reporting is required: a global commit-ordered
watermark covers every account mutation up to that position.

Pruning retains the newest entry for each affected account and the newest
unknown-owner entry. This prevents desired revisions from disappearing after
policy deletion, ownership transfer or retention cleanup.

## Consequences

Apply the new migration before using account-scoped status. Older control planes
fail closed for upgraded access audits. Missing fleet membership, stale gateway
observations and an empty revision remain unverified. Replay remains observation
and invalidation evidence, not token/JWKS attestation or frozen gateway membership.
