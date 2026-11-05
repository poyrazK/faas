# ADR-20261105120000: Repair catalog references and retained outcome resolution

- Status: accepted for internal operator qualification
- Date: 2026-11-05
- Extends: [lossless types](20261103185741-repair-lossless-types-and-runtime-guards.md) and [apply resolution](2026101703-repair-apply-resolution.md)

## Problem

PostgreSQL's regclass and related catalog-reference aliases render object names
into JSON. Renaming an object and reusing its old name makes reconstruction
resolve a different object, even though the captured business row and its
version have not changed. A local capture round trip cannot prove that those
names retain their original meaning. Verification of the repair's own journal
can otherwise report that this incorrect restoration is unchanged.

A hosted apply can become uncertain before its first source lookup returns.
In that case the hosted approval exists, but the source plan remains unapproved.
Normal retention can later compact the expired source plan and its journal.
Rejecting outcome resolution solely because that payload was purged then leaves
the hosted operation unresolved and holding pending-operation capacity forever.

## Decision

Selected fields and primary keys cannot contain PostgreSQL catalog-reference
aliases: regclass, regcollation, regconfig, regdictionary, regnamespace, regoper,
regoperator, regproc, regprocedure, regrole and regtype. Follow the existing type
traversal through domains, composites, ranges and multiranges. Ordinary oid
values are not resolved through names and retain their existing eligibility.
Unselected references remain native values and cannot be changed by a repair.

Use the same type qualification in enrollment, runtime capture, planning,
replacement, apply, writer/executor readiness and observations. Existing unsafe
enrollments become blocked after the owner upgrade. Preserve old journal bytes,
plan digests, approvals and receipts. Validation and verification report
schema_changed rather than certifying those references; committed receipt replay
and historical reads remain available. No upgrade can infer an original object
identity from an ambiguous saved name.

Outcome resolution requires only the retained plan identity, integrity and
source-owned row lock. It can record not_committed for a compacted, unapplied
plan. Keep exact scope, digest and repair-operation checks, NOWAIT locking,
existing-receipt precedence and idempotent closure replay. A compacted plan's
retained_digest proves its compacted document; its original digest still binds
the closure. Never reconstruct payload, renew approval or authorize application.
Apply, replacement and ordinary payload access keep their existing purge fences.

The hosted resolution flow then settles the operation using this durable source
closure and releases its pending-operation slot. Regenerate the three leaf SDK
readiness probes from the new qualification contract and apply the explicit
owner schema upgrade before new executors and clients.

## Validation

PostgreSQL regressions cover every catalog alias in fields and keys, nested
aliases, an actual rename/name-reuse restoration under the preceding policy,
blocked pending historical plans, retained commit receipts and native unselected
references. Retention tests close plans after both plan and journal compaction,
exercise busy locks and identity mismatches, preserve business rows and retained
evidence, and ensure closure cannot revive writes. The managed HTTP regression
fails the first apply lookup, compacts the source plan, resolves through the
executor and confirms that pending-operation capacity is released exactly once.
