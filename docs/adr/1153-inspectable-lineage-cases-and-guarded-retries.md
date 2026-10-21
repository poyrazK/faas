# ADR-1153: Inspectable lineage cases and guarded retries

Status: Accepted
Date: 2026-10-13
Related: ADR-969, ADR-1151, ADR-1152

## Decision

Record unresolved and conflicting repair evaluations as scoped cases keyed by
subscription and durable outbox identity. Store only identifiers, the observed
acceptance and root incarnation, status/reason codes, revision, retry count and
retention timestamps. Case recording commits with the forward scan checkpoint.
Cases are last recorded evaluations, not a live ownership view; records from
before this ledger was installed cannot be reconstructed from aggregate counters.

Expose ascending exclusive-outbox-cursor pagination, default 50 and maximum 200.
GET requires read scope. Retained rows can expire between pages; this is a live
retained listing, not a snapshot or proof that all historical gaps are represented.
Cases with successful explicit retries remain inspectable until retention ends.

POST a selected case with a nonzero request UUID and positive expected revision.
Lock coverage and case ownership, then check the durable retry-result ledger
before checking the current revision. Identical request/guard retries return the
original saved result; UUID reuse with another guard or a stale revision returns
409. Revalidate the same retained receipt acceptance, known candidate identity
and root incarnation, and reuse the authoritative evidence checks from ADR-1152.
Missing, incompatible and competing evidence stays unresolved or conflicted.
A successful retry associates lineage only; it does not execute customer code,
choose between conflicting owners or reconstruct historical outcomes.

Root association, revised case and immutable request result commit in one
transaction. Memory-store equivalents run under the existing mutex. Failed
transactions do not consume the request identity. Retry records do not move the
forward cursor, alter scan counters, clear loss markers or reset a known coverage
boundary. Remaining eligibility changes occur through normal reconciliation.

Retain cases for 30 days after their latest recorded evaluation and request
results for their retained lifetime, at least 30 days after retry. Expired records
are excluded before physical cleanup. Bounded retention removes retry records and
then cases without remaining retry records, avoiding large retention cascades.
Hard application deletion cascades through coverage. Two operational tables add
no payload storage or new daemon. Clone registries classify them operational.

## Consequences

Users can review concrete reasons and deliberately recheck changed evidence.
Duplicate retries can return an older result than the current case; refresh the
list to see its latest revision. Once retained results expire, stale revisions
still prevent repeated evaluation. A removed subscription/control, expired case
or hard-deleted app can make the operation unavailable. Metadata retention is
independent of event receipt retention, so a retained case may report a missing
receipt. No inference can overcome missing authoritative history.
