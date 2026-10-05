# ADR-569: Preserve managed PostgreSQL accounting through shutdown

- **Status:** accepted
- **Date:** 2026-10-04
- **Decision:** Collect every catalog row with a known provider resource ID,
  regardless of lifecycle state. Include these resources, and ready rows, in
  admission completeness. A provider-confirmed deletion requires contiguous
  coverage through the UTC policy-window ceiling of `deleted_at`. Refresh every
  covered window in the final three-window correction tail at or after that
  endpoint plus three policy windows before completing automatic accounting.
  Compute the oldest tail observation from the ledger, so one successful
  correction cannot conceal a different failed correction. Completed terminal
  coverage remains valid without periodic freshness expiry and stops provider
  requests. Active resources retain their existing freshness requirements. Deduplicate
  logical IDs during keyset discovery, since lifecycle writes can move an
  already-seen row beyond a later cursor.
- **Why:** Both stores admitted an unmetered resource after it left `ready`.
  `ClaimDelete` removed it from collection before shutdown, and `FinishDelete`
  rejected further ledger writes. A three-hour backlog disappeared from the
  account's completeness checks even though no consumption had been recorded.
  Provisioning, updating, and failed resources with known IDs had the same gap.
- **Consequences:** Deletion does not wait for accounting. Tombstones retain
  provider identity and the confirmed endpoint already stored in the catalog;
  no schema change is needed. A shutdown inside a window waits for that window
  to close. Deleted rows accept canonical complete-window writes through their
  endpoint, with the existing owner/backend/fingerprint and contiguous coverage
  checks, and reject post-shutdown windows or observations before window closure.
  New reservations remain blocked until missing history and the bounded final
  correction tail are covered. With hourly policy the correction horizon ends
  three hours after the final window closes. Ready database counts continue to
  report ready resources; a zero-ready account can correctly report stale usage.
  Restore descendants inherit their root aggregate's accounting lifecycle
  and endpoint through deletion, with no independent provider reads or duplicate
  quantities. Deleting a child of a live root keeps the active aggregate's
  freshness requirement; it does not introduce a separate final-window wait.
- **Rejected alternatives:** Dropping tombstones or counting missing history as
  zero admits accounts with unrecorded consumption. Waiting for usage before
  destroying compute prolongs resource spend. Requiring tombstones to keep up
  with the current time invents post-shutdown windows and permanent staleness.
  Treating the newest observation alone as completion loses failed corrections
  elsewhere in the final tail. Scanning old ledger rows to infer initial
  coverage would conceal historical gaps.

This extends [ADR-565](565-managed-postgres-fleet-usage-recovery.md) and the
bounded replay policy in [ADR-516](516-managed-postgres-usage-correction-replay.md).
The final endpoint anchors replay even when collection resumes after a long
outage; it never rolls forward beyond shutdown. Fleet recovery priority and the
shared maximum of 24 requests per database per sweep remain intact.

Completion means the existing bounded guardrail policy has sufficient evidence;
it is not final provider invoice settlement. Provider corrections outside this
horizon still need reconciliation. Post-deletion Neon history is not qualified:
a missing or unavailable history response retains stale admission and does not
prove zero usage. An operator import/diagnostic workflow for retained exports or
invoices remains required for history that cannot be fetched automatically.

Unknown provider IDs remain a separate gap. After an ambiguous provisioning
response, deletion can discover and remove a project using its logical name
without returning that identity to the catalog. Never-attempted reservations
cannot be assumed to have consumed resources, but uncertain attempts need
explicit durable discovery/accounting evidence. This change covers known IDs
and must not be described as resolving that path.

Discovery still scans retained known-resource tombstones. Completed rows use no
provider requests, but per-sweep catalog work and memory grow with retained
history. A bounded durable selection/index strategy remains follow-up work.
