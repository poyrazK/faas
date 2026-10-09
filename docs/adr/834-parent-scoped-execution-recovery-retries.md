# ADR-834: Parent-scoped execution recovery retries

Date: 2026-10-09
Status: Accepted

## Context

Durable terminal recovery results and execution-finished notifications identify
which admitted replays failed. Starting another app-wide execution recovery can
select unrelated failures or newer replay descendants. Operators need a child
selection scoped to one recovery's exact failed deliveries, with durable creation
idempotency and historical links.

## Decision

Extend the existing recovery preview/create request with `parent_job_id` and
`request_id`; no new routes, scopes or worker are required. A parent must belong
to the same account/app and be a retained execution-mode job with completed or
cancelled admission, including automatic expiry. Other queued handlers may still
be running: select only the parent's known saved failures. A running/paused or
routing parent returns conflict; an unowned, wrong-app or pruned parent returns
not found. Preview is read-only and reserves neither a selection nor request ID.

Select only queued parent items with saved `failed` or `dead_lettered` results
matching their admitted replay UUID, generation and creation time. Existing
`outcome=failed|dead_letter`, subscription, source/type and minimum-age filters
can narrow that set. Omitted outcome selects both; minimum age uses terminal
completion time or saved-result capture time if completion is unavailable.
Succeeded, cancelled, expired, superseded, active, unknown and untracked parent
items cannot enter this selection. It never follows a root's latest descendant.
Counts describe saved selected failures, not guaranteed replay eligibility.

Freeze expected invocation state, attempts, generation, creation/completion time
from the saved result. Retained dead-letter evidence supplies an unreplayed DLQ
identity only for that exact invocation incarnation. Missing invocation/receipt
or DLQ evidence does not hide a saved failure: the child worker/preflight reports
changed, receipt-expired or expired evidence using existing guards. A subsequently
uncertain invocation is changed and cannot be replayed by this child selection.
Existing lane locks, one-child replay identities, dead-letter generation guards,
capacity admission and savepoints prevent a concurrent job or manual replay from
admitting the same expected failure twice. Existing parent results remain frozen.

Store `parent_job_id` and `parent_position` in each selected item's existing
`expected_progress` JSON and expose them in item preview/read and CLI output.
The child's immutable selection records its immediate parent. Links are
historical metadata with no parent FK: parent pruning cannot erase child lineage
or prune the child early. Reads clone memory lineage pointers. Each subsequent
retry points to its immediate parent, forming an inspectable chain.

Child creation requires an explicit caller-generated request UUID. Add one
nullable operational `request_id` job column, a CHECK tying it to execution-mode
parent/request selection fields, and a unique account/request index. Ordinary
creation continues with no request identity. Under the existing account range
lock, look up and key-share-lock a retained child **before** parent selection and
quota checks. Repeat requests return that child's current report with the same
job ID; a changed app or normalized selection with the same UUID returns conflict.
UUID spellings and default admission rate normalize before comparison. Reason
is excluded: the first creation's audit reason wins and repeats create no history
or notifications. The parent's row is locked for new creation to prevent pruning
during selection. Memory storage mirrors lookup and creation under its mutex.

Request identity lasts with the existing child job retention; it does not pin the
parent or extend thirty-day retention. A retained child can be returned after
parent pruning. Do not reuse an ID after its child is pruned. New requests against
a pruned parent cannot reconstruct a selection. Existing optional HTTP
`Idempotency-Key` behavior still applies separately; when using that header,
preserve the identical body across transport retries.

Expose CLI `--parent-job UUID` on `recovery-preview` and `recovery-create`.
Parent-job implies execution unless mode is explicitly supplied; explicit routing
is rejected. `--request-id UUID` is optional on preview and required on creation.
Creation retains `--yes`, existing pacing, operator reason, receipt protection,
quota limits, audit history, cancellation and execution notifications. Preview
can change before creation; a new request ID freezes the then-current selection,
and repeating it never expands that selection when more parent failures settle.
OpenAPI and existing Go/Node/Python methods expose the extended DTOs.

## Consequences

Apply the additive migration before upgrading API/scheduler/CLI binaries. No
automatic retries or webhook receivers are created by this feature. Recovery
remains an explicit operator action and handler side effects remain at least once;
use application idempotency. Whole-subscription ordering is not restored by a
later recovery. Skipped admissions remain visible separately from queued handler
success. Empty selections produce the existing admission-completed lifecycle and
no execution-finished event.

Downgrade binaries before Down. Dropping the request column removes the uniqueness
index; immutable parent/request lineage remains in JSON. Up restores retained
request identities from that JSON before enforcing the CHECK and unique index.
Parent-scoped creation is unavailable with downgraded binaries; only send these
requests after the migration and compatible binaries are restored. Pruned child
identities cannot be recovered. Existing recovery item/job retention and
notification delivery behavior otherwise apply.
