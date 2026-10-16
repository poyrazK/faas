# ADR 1056: Consumer-group event deduplication

Status: accepted for preview implementation; migration/runtime validation pending.

## Decision

Add immutable creation settings deduplication_metadata_key and
deduplication_window_seconds. An empty key disables deduplication and requires zero
window. With a key, an omitted/zero window defaults to 3600 seconds; explicit windows
are 60 to 86400 seconds. Existing groups default to disabled. Reuse the existing
64-byte metadata-key validator. Event IDs are exact metadata values (already bounded
by publishing metadata validation); no trimming, case folding or Unicode normalization.
Missing/empty values process normally and consume no remembered-ID capacity.

After the immutable message filter matches, check the configured ID during source
assignment under the group lock. The first filter-matching assigned copy wins. Store
only SHA-256 of its ID and expiry, scoped to group UUID. Subsequent source copies
within the window advance the assignment cursor and increment duplicate_messages,
without allocating a snapshot, attempt, lease, fence, claim-rate credit or metric/
delivery-history event. Filtered events neither register IDs nor increment duplicates.
IDs are remembered atomically with snapshot admission and cursor advancement; any
transaction error rolls all of them back. Already-assigned snapshots bypass this check.

Deduplication suppresses additional source copies, not processing retries. Retry,
lease expiry, cooperative release and explicit single/bulk dead-letter replay retain
normal behavior. It neither proves processing succeeded nor guarantees exactly-once
handler effects. First-copy admission can produce a dead snapshot due to invalid/
expired deadlines; later copies with the same ID are still suppressed. Publishers
must assign distinct IDs to distinct logical events, including intended corrections
or changes with different contents. Same-ID payload differences are not compared.

## Bounds and backpressure

At most 4096 remembered IDs per group. Expiry starts at first assignment and duplicate
hits never refresh it. Prune expired rows before keyed assignment. Idle expired storage
remains physically bounded; status counts only live entries. An expired ID can be
assigned again, even if an earlier snapshot remains outstanding. The window is not
a permanent uniqueness constraint. No raw IDs, payloads or credential tokens are
stored in the ID table. Group deletion cascades it; resync preserves remembered IDs
and the cumulative counter, so rewinding source history does not bypass the window.

Never evict live IDs. At capacity, cached duplicate IDs can still be skipped, and
missing-ID events still process normally. A new keyed event stops assignment before
cursor advancement with a retry hint based on the next expiry. Source-order scanning
means that event can hold later source events until space becomes available.
Existing snapshots remain claimable, including ordering-aware and priority selection.
Priority prefill tolerates this admission blocker before selecting existing work.
All assignment paths share the existing 128-event scan budget, including duplicates.

Single claims return 429 realtime_group_deduplication_full with Retry-After and
retry_after_seconds. Batch claims can return partial work with deduplication_blocked
and retry_after_milliseconds. Long polling returns promptly on this blocker. Node
processing handles the single error/batch flag and polls within the expiry hint at
the configured pollIntervalMs (default one second) to discover existing ready work,
with cancellation; active
local settlements interrupt a blocked batch wait so retried snapshots are claimable.
Settlements and their receipts remain available while ID capacity is full.

## Visibility and rollout

Creation/status responses expose the immutable settings. Account and scoped status
also return duplicate_messages as a decimal string and deduplication_keys as the
current live count. The account inspector shows the field/window, duplicate count
and remembered IDs. These are separate from filtered_messages and completions.
No extra worker grant, webhook or raw-ID inspection endpoint is introduced.

New SQL is SQLC-generated. Apply migration 20261012102700530 before the new binary,
and roll out all API replicas before creating enabled groups: older binaries ignore
the policy and can admit duplicates. Node/Python generated SDKs expose the fields.
Downgrade drops the remembered IDs/policy/counter; advanced cursors cannot restore
previously skipped events. Re-create/recover from application state deliberately.

Go/Node builds, SDK generation, Python AST, OpenAPI and SQLC parity, and whitespace
checks only. Runtime tests, UI rendering and migration execution are deferred by request.
