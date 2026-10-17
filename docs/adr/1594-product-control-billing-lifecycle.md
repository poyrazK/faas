# ADR-1594 · Product Control billing review, finalization and invoice handoff

- **Status:** accepted for internal implementation
- **Date:** 2026-10-15
- **Decision:** Close Product Control admission-month receipts into immutable
  statements; retain terminal outcome evidence, allow explicit commercial
  waivers of unresolved receipts, and append bounded credits as separate notes.
  Keep every API, CLI operation and customer billing view behind the existing
  default-off `FAAS_PRODUCT_CONTROL_ENABLED` gate.

## Evidence and period closure

Each commercial receipt now retains the first verified `succeeded`, `failed` or
`cancelled` outcome and its timestamp in the same transaction as the logical
Operation transition. Result expiry does not remove this evidence. Migration
backfills only independently recorded successes. Missing legacy failure evidence
remains unresolved, even if no Operation record remains.

An owner reviews one app/customer/UTC admission month. Invoice lines group by
feature, immutable catalog revision, plan and admitted unit price. They partition
starts into charged successes, failures, cancellations, waivers and unresolved
receipts. Integer millicents are preserved without currency conversion or rounding.
The SHA-256 confirmation hash binds the owner, app, period, counts, money and
latest evidence timestamps. Reads use a consistent PostgreSQL snapshot. Receipt
and document lists are explicitly paginated at 100 rows; a review with more than
1,000 invoice groups fails with a limit error and is never silently truncated.

Only a closed UTC month with no unresolved, unwaived receipts can be finalized.
The caller must submit the hash it reviewed. The account admission lock excludes
a submission begun just before month rollover: admission reads its clock after
that lock, so it either commits before closure or belongs to the new month.
Shared app and tenant locks precede ordered receipt locks, including before
document foreign-key checks. Finalization and receipt settlement serialize on
the receipt rows; billing never locks invocation, Operation, deployment or pin
rows. Existing execution lock ordering stays intact.

Finalization writes one immutable statement per app/customer/month and returns
that same document on a same-hash retry. The frozen review remains available
after subsequent execution evidence arrives. Current usage remains provisional;
it does not become a statement automatically.

## Waivers and credits

For an unresolved receipt, the owner can record a permanent waiver with a reason.
This waives any eventual charge; it neither cancels execution nor declares its
outcome. It is also valid for a still-running run if the owner deliberately
accepts that commercial write-off. The same operation/reason retry returns the
original evidence. A changed reason conflicts. A verified terminal receipt
cannot be waived; a finalized verified charge uses a credit note instead.

Late verified success still records execution truth, but a waived receipt never
becomes billable. This ensures period closure cannot produce an unexpected later
charge. A waiver cannot be removed or edited through this service. Owner reasons
must contain no secrets; credit reasons are visible to that customer.

A positive credit amount targets one successful, unwaived receipt in an already
finalized statement. The owner supplies a stable UUID for the credit note. A
retry must preserve tenant, month, operation, amount and reason. Concurrent
credits cannot exceed that receipt's original price in total. Free, failed,
cancelled and waived runs cannot be credited. A credit note stores a negative
amount and refers to the original statement and Operation. Statements retain
their original totals; credits are exported as distinct adjustment documents.

## Invoice handoff and customer access

Each statement or credit note can acquire one immutable external invoice or
credit-note reference, unique within the owner's Product Control handoffs.
Retries return the retained reference, currency and signed amount. Replacing a
reference or reusing it for another document conflicts. A read endpoint recovers
a handoff after a lost response. The owner must use this stable document ID as
the invoicer's idempotency key. Persist or reserve the handoff before creating
the external invoice; after a lost external response, query that invoicer by the
same key before retrying. Gregale cannot enforce another provider's behavior.

Gregale does not collect payment, refund money, calculate tax, round invoices or
act as merchant of record. These product documents do not feed existing request
rate-card or platform infrastructure statements automatically. The owner chooses
the correct customer charging basis and never exports both as the same charge.

Owner reads use existing read/MFA policy; writes use deploy-write/MFA and
verified-email policy, and emit decision audit events without reason text.
Customers have read-only month views, finalized lines and paginated credit notes.
Their tenant always comes from a verified customer cookie mapping or tenant
credential, and the product assignment is checked. Browser views never sum a
partial page into a claimed net balance. Logout, workspace changes and newer
month selections fence stale billing responses. All routes are private and
excluded from the public OpenAPI/SDK coverage contract during this internal phase.

## Validation and rollout

Memory and PostgreSQL contract suites cover three customers on two plans,
closed-month guards, durable nonbillable evidence, stale reviews, concurrent
finalization, late waived success, credit caps, retries, invoice uniqueness and
pagination. A PostgreSQL concurrency test holds an uncommitted success and proves
closure waits for settlement before rejecting stale confirmation. HTTP tests
exercise default-off behavior, owner/customer separation, scoped cookie and
tenant billing views, invoice retry conflicts and flag rollback. Browser tests
cover signed credit amounts, pagination and stale-response fencing.

The real export/Auth/file/interruption pilot still requires an Operations-qualified
native host and separately configured internal customer identities. Portable
tests are evidence for this billing contract and do not qualify a native lane.
