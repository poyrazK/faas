# ADR-20261104144425: Pipeline removal source guards and verification row limits

- **Status:** implemented for local/staging qualification; production disabled
- **Date:** 2026-11-04
- **Amends:** ADR-1591, ADR-20261103220000

## Context

Live-table removal validated the saved plan and remote ownership, but bypassed
normal source-schema inspection. A remaining source column could be dropped and
recreated while an unapproved preview left obsolete imported values readable.
Removal could then complete as live with the obsolete source baseline.

Verification truncated encoded JSONB only after constructing it. A supported
129 MiB bytea value expanded beyond PostgreSQL's JSONB string representation
limit before the verifier could report its existing 16 MiB row limit. The SQL
error also aborted the enclosing transaction needed to save an inconclusive run.

## Decision

Inspect the saved source baseline on every live-removal reconciliation, including
replay of a committed completion. Before retirement begins, require all original
tables to match. After the durable retirement intent commits, require only the
remaining selected tables: the retained table no longer depends on its source.
Use the existing logical-definition comparison, including source identity, keys,
column defaults, supported types, RLS and replica identity. Physical materialization
of an unchanged fast default remains compatible.

Hold bounded SHARE locks on that source scope while validating and committing
the initial retirement or final completion. Final validation occurs after the
Data API refresh. Check both the current catalog lease/intent and the continued
existence of the source transaction before committing destination changes.
Incompatible drift follows existing `source_schema_changed` recovery: gate
imported reads and require explicit resync, preserving imported rows and generation.
There is no automatic replacement copy or nested schema-change workflow.

Before constructing each verification row's JSONB text array, compare the sum of
its text byte lengths with the existing row budget. For bytea, calculate its hex
length from raw octet length under the verifier's fixed output settings. An
oversized row produces a NULL sentinel that terminates the recursive scan. Other
rows keep the existing nullable-text-v1 encoding and exact encoded-length check,
including JSON escaping. This does not change proof identity or limits.

Run each scan inside a savepoint. If a builtin output function still raises
PostgreSQL `program_limit_exceeded` (`54000`), roll back that savepoint and report
`verification_row_limit`. The enclosing transaction can then persist the
inconclusive result and retire tracking. Other SQL errors retain their existing
error semantics, and savepoint rollback failure must not produce a successful
inconclusive result.

## Validation and rollout

Native regressions cover source drift before preview, pending approval, approved
retirement, detachment, archival, runtime refresh and completion receipt replay.
They require gated reads, retained original values and unchanged generation.
Additional cases cover a dropped retained source, continued healthy CDC, and
source DDL blocked through a completion fence that rejects changed intent.

PostgreSQL regressions cover 129 MiB and 9 MiB bytea values, oversized text, JSON
escaping, SQL NULL, JSON null and ordinary supported values. They verify usable
parent transactions after both a row-limit result and a genuine lock timeout;
timeouts remain errors and permit a later successful retry.

No new catalog migration is needed. This remains local/staging qualification;
the combined branch still requires its existing append-only setup-ownership
migrations before deploying the updated control plane.
