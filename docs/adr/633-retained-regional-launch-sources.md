# ADR-633: Retained original regional launch sources

Date: 2026-10-05
Status: Source-bound issuance and fenced retirement recovery implemented; lifecycle integration and native acceptance pending.

## Context

The cell retirement owner (ADR-631/632) needs the original full permit and the
actual native lookup key. A physical receipt retains the permit but UUID
canonicalization loses whether the actual instance key was compact or hyphenated.
Current app, deployment, instance and node rows can change or disappear. A lost
native reply must not strand an otherwise recoverable fenced hold indefinitely.

## Decision

Persist an immutable launch source before marking a permit issued, in the same
read committed transaction and app quota journal lock as physical and initial
serving-seat issuance. Retain the complete original permit and the exact actual
instance key. Accept only its lowercase compact or canonical UUID spelling.
The scheduler captures the key before canonicalizing the permit's identities.
Once issuance is attempted, an uncertain commit reply or a failed post-issuance
check returns the original placement to its caller for cleanup. A returned
placement on error does not prove issuance; cleanup must read its retained
journal. Issuance never authorizes execution on its own.

The new dispatch writer is mandatory through the Store interface. Retain the
older placement writer for explicit bookkeeping. Never backfill a source onto
an already-issued receipt or change an equivalent lookup spelling on retry.
Source identity survives collection and signing-key rotation. SQL guards refuse
updates, deletion, truncation, mismatched permits, wrong actual instance keys,
and source insertion after issuance, fencing or release. The additive migration
has no destructive rollback and no mutable-row foreign keys or backfill.

A configured schedd cell owner scans only source-bound issued, fenced,
unreleased holds for its immutable signer cell. Composite app/permit cursors
provide ordered bounded pages because a permit ID is only unique within an app.
Each attempt uses the original key, permit and first cell fence through the
authenticated retirement protocol. It holds no database transaction during RPC.
An unsuccessful attempt advances the cursor to prevent one unreachable host
from starving later holds. Empty pages reset traversal so retained failures and
new rows behind the cursor are retried. Restart begins at the first page.

Recovery processes at most 64 items per page, with a 30-second per-attempt
deadline and a 30-second page cadence, all in `pkg/api/limits.go`. It runs outside
the scheduler loop and joins on daemon shutdown. Unconfigured owners do not
start recovery. No current app/head/instance rows are needed for retirement.

Missing source, unfinished native production, missing historical launch frame,
unverified native absence or independent host/cell first-fence conflict retains
capacity. This scan proves no global inventory completeness and never creates a
stop from missing rows or a generic Destroy response. It does not discover
unfenced orphans or synthesize identity for older issued holds. All quota heads
remain frozen.

Lifecycle failure callers, actual dispatch phase recovery, historical/orphan
inventory, first-fence reconciliation, conservative restart RAM accounting,
native capture/pause/migration, directory authority and receivers, global billing
and routing to all selected healthy eligible regions remain required for the
end-to-end objective. In particular, a permit issued before any native dispatch
may have no host frame and cannot be released by inferring native absence.

## Validation

Exercise both stores for atomic source/physical/seat issuance, exact retry,
changed lookup aliases, legacy backfill refusal, cancellation, quota exhaustion,
composite pagination, cell filtering, first fences, release and collected apps.
Check PostgreSQL source retention/SQL guards and restart recovery. Exercise lost
reply recovery over actual mTLS, refusing wrong peers and preserving capacity on
native uncertainty. Portable Linux tests do not replace dedicated native Linux
x86_64 KVM test-metal, leakcheck and recovery acceptance.
