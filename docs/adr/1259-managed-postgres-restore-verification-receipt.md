# ADR-1259: Durable restore verification for drifting historical positions

- **Status:** accepted
- **Date:** 2026-10-14
- **Amends:** ADR-677

## Context

ADR-677 proves a Neon restore target by independently mapping the requested
time T through a historical connection and requiring that LSN to equal the
target's `parent_lsn`. It repeats the mapping on every reconcile, after a
worker restart and before readiness.

A live diagnosis on production-us (2026-10-14) ran six disposable-project
probes against production Neon with the canary configuration. It showed that
Neon maps T to *some* LSN inside the commit-free interval that follows T. The
chosen LSN depends on the WAL head at the moment of asking:

- Idle source: T resolved to the WAL head `0/1968180`, equal to the branch.
- After one empty commit landed after T, the same T resolved to `0/19681A0`.
  That LSN is inside the commit record not yet replayed, so it holds the same
  data.
- After more WAL, T resolved to `0/1968180` again.
- Before any later commit, two resolutions seconds apart returned `0/19681D8`
  and `0/1968210`.

Every LSN in that interval holds the same committed data, so equality across
two separate resolutions is a coincidence. In one run, a credential rotation
landed on the source after the restore branch existed. That target then failed
the re-check on 10 of 12 reconciles with `conflict`, under exponential backoff
from 30 s to 15 min. Restores of actively written databases are the normal
case.

Across 8 live restores, the first verification right after creation matched in
7. The failures came from the later re-observations.

A snapshot-based equivalence check was also ruled out. Neon's time-travel
computes report a synthetic snapshot (`1024:1024:`).

## Decision

1. **Verification record.** An adapter that verifies a restore target
   independently, under ADR-677 steps 1–4, also returns a
   `RestoreVerification`. It holds the exact target provider identity, the exact
   source, the requested point, and an adapter-defined evidence fingerprint of
   the target's immutable metadata. For Neon the fingerprint covers project,
   branch ID, parent branch, parent LSN, creation instant and initialization
   mode. The fingerprint carries identifiers only, never credentials. A lineage
   proven by exact provider timestamp metadata needs no record, because that
   metadata does not drift.
2. **Recorded once, under the lease.** The service writes the record durably to
   `managed_postgres_restore_verifications` in the same reconcile that observed
   it, before publishing readiness. The write requires the current provisioning
   lease and the database's recorded target, source and point. The record is
   append-once: a second, different record for the same database is a conflict.
   Gregale observed this evidence. It is not a receipt fabricated from request
   parameters, so ADR-677's prohibition does not apply. A record lives exactly
   as long as its database row: final account erasure removes deleted
   tombstones and their records with them. The down migration refuses to drop
   retained records.
3. **Later observations confirm instead of re-resolving.** Inspection,
   readiness, recovery of an acknowledged creation, and idempotent replay all
   receive the record. The adapter re-reads the exact target and requires it to
   reproduce the evidence fingerprint and to be settled. It then reports the
   recorded lineage without mapping T again. Any of the following is a
   conflict:
   - a reset or replacement target;
   - a changed parent or parent position;
   - a parent timestamp after the request;
   - a record that does not match the request.

   Without a record, ADR-677's behavior is unchanged.
4. **A mismatch before the first record means "not proven yet".** An
   LSN mismatch while no record exists returns `ErrLineageUnproven`, a form of
   `ErrUnavailable`. The service records `restore_lineage_unavailable` and
   retries at the poll interval for the first 20 attempts, then falls back to
   the ordinary backoff. Each retry is a fresh, independent attempt at ADR-677's
   proof. A target with a parent timestamp after the request remains a
   conflict, as before.
5. **Qualification.** Qualification still requires a fresh verification of the
   newly created target. It then carries that record into its readiness poll
   and its idempotent replay. What qualification version 7 proves is unchanged,
   so existing approvals stay valid.

## Validation and limits

Regression tests cover:

- record write and replay, conflicting records, and stale-lease writes;
- confirmation success, and conflicts for a changed parent LSN, creation
  instant or branch identity;
- the "not proven yet" classification and its poll-interval retry;
- qualification carrying the record through readiness and replay.

Live acceptance needs one production-canary restore of an actively written
source. It must show the record written on the first verified observation and
readiness reached without re-resolving T.

The first proof still depends on one matching mapping. On a busy source that
can take several 15 s attempts. Creating the target from an LSN resolved
beforehand (`parent_lsn` instead of `parent_timestamp`) would make the first
match deterministic, but that path is not validated on Neon and is not adopted
here.

The record is as trustworthy as the store, like ADR-638's custody receipts.
The fingerprint re-check detects a provider-side reset or replacement, but not
a tampered database row.
