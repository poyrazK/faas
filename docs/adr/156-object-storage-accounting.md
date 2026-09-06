# ADR-156 · Direct object storage accounting and safety budgets

- **Status:** accepted for the accounting milestone; paid launch remains gated.
- **Date:** 2026-09-05
- **Decision:** retain direct S3 transfers and the single hot `s3_enabled` flag; add durable capacity reservations, inventory observations, provider usage reports, and fail-closed URL admission.
- **Why:** direct reusable URLs cannot enforce an instantaneous request/egress spending ceiling. The user accepted delayed budget cutoffs without introducing a Gregale transfer proxy.

## Accounting contract

`apid` owns bucket admission and its accounting records. PostgreSQL serializes
decisions per account. Before signing a PUT, reserve the highest authorized
size for that bucket/key; repeated or smaller PUTs do not reserve bytes again.
Zero-byte PUTs still reserve a key. Every GET/PUT authorization consumes a
separate monthly issuance allowance, including signer failures. Issuance is
an abuse-control counter, never actual provider usage or invoice quantity.

Capacity is conservative: the initial complete inventory baseline plus key
grants, or the latest observed inventory, whichever is larger. Reservations
are not refunded when URLs expire, objects disappear, or an HTTP response is
lost. A request may start before expiry and finish afterward. Confirmed bucket
deletion releases its capacity; object deletion alone does not. Reclaiming
capacity without deleting a bucket requires a future provider-qualified
quiescence/rebase workflow. Existing-object overwrites may be double-counted
against the initial baseline. This is deliberate and visible in the usage API.

Inventory scans use durable two-minute claims, a 45-second deadline, bounded
pagination, and publish only complete results. Partial, failed, cyclic, or
oversized scans do not refresh usage. Samples are durable operational
observations, not a claim of exact byte-hour billing across a paginated scan.

The S3 data API has no portable billing endpoint. An external provider adapter
exports cumulative UTC-month reports per account/backend: stored byte-hours,
actual request count, actual egress bytes, and total cost in EUR millicents.
The report source must include relevant upstream costs; conversion to EUR and
attribution from physical buckets must be validated by the operator. Each
backend may provide an atomic operator-owned JSON export file, imported by
apid. A strict operator-session API provides a manual import/diagnostic seam.
Provider-specific exporters and pricing are not fabricated by the generic S3
driver. A provider without adequate usage attribution cannot qualify for launch.

Reports are immutable, idempotent by account/backend/month/observation time,
and monotonically cumulative. Conflicting duplicate, regressing, future,
wrong-placement, missing-field and null-field reports are rejected. Late
corrections that reduce a total need an explicit future adjustment workflow;
do not rewrite old evidence or silently reset budgets. Deleted buckets do not
erase the month's costs.

## Enforcement and consequences

- All replicas must use the same explicit accounting policy. No implicit price,
  allowance, unlimited budget, second enable switch, or account allowlist exists.
- Missing policy, incomplete/stale inventory or missing/stale provider reports
  blocks new signed GET/PUT URLs. Month rollover requires fresh monthly reports.
- Reported cost/request/egress and URL-issuance ceilings block new URLs. Storage
  byte/key limits block additional PUT reservations but preserve reads.
- The global disable flag still blocks new provisioning/signing. Listing,
  object deletion, empty bucket deletion and recovery cleanup remain available.
- Budget overshoot is possible during report lag, polling, outstanding URL
  validity, and in-flight transfers. There is **no bounded monetary overshoot**.
  Retained data continues to cost money, and cleanup calls can incur charges.
- An optional provider-neutral rate card may expose a deterministic estimate in
  the usage API, but no plan allowances or invoice lines ship in this
  milestone. Reported costs remain operator cost accounting, not permission to
  charge customers those amounts.
- These records follow existing account hard-deletion semantics. They are not
  a substitute for a legally retained invoice ledger.

## Rejected alternatives

- Counting signed URLs as successful uploads/downloads: unused and replayed
  URLs make the numbers false.
- Releasing reservations at URL expiry: expiry does not terminate accepted
  in-flight uploads, and a scan is not an atomic snapshot across pages.
- Treating missing reports as zero: hides outages and permits unlimited spend.
- Proxying every transfer: changes the approved direct-transfer architecture
  and introduces a new bandwidth/availability dependency.

## Validation

Memory and PostgreSQL parity tests cover concurrent admissions, byte/key
ceilings, same-key replacement, report idempotency/regression, stale inputs,
month rollover, inventory fencing, and deletion without cost refunds. API tests
cover authorization, error values, disabled/stale/budget denial before signing,
cleanup, strict operator import, and partial/cyclic inventory scans. The launch
gate still requires a real provider exporter and live-provider qualification.
