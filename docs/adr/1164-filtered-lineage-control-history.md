# ADR-1164: Filtered lineage control history

Status: Accepted
Date: 2026-10-13
Related: ADR-1161, ADR-1163

## Decision

Add optional exact action, actor_id and credential_type query filters to the
existing guarded control-history GET. Empty values mean no filter; nonempty
filters combine with AND. Validate action and credential enums and normalize
nonzero hyphenated actor UUIDs to lowercase; also accept the exact actor ID store
for direct internal calls. Reject invalid filters with 400. Filtering never
changes account/app/subscription ownership, retention or creation-guard checks.

Apply every filter before LIMIT in the SQLC query, and before sorting/paging in
the memory store. Action uses the existing authoritative action column. Actor
and credential type use immutable receipt JSON fields. The per-job receipt bound
of 128 keeps scans bounded; no new indexes, columns or migration are needed.
Legacy receipts remain visible without attribution filters and can match an
action filter. A nonempty actor or credential filter excludes receipts missing
that field; never infer attribution from current ownership.

Keep ascending original sequence order and exclusive after semantics. Fetch one
extra matching receipt to determine next_after; the cursor is the last returned
matching receipt's original sequence, not its filtered position. Gaps are normal.
Clients must preserve filters between pages; restart at after=0 when changing
filters to inspect the full new selection. Listing remains live and append-only,
not a snapshot or reservation; future matching commands may arrive after a page.

Expose filters as optional Go variadic filter options, preserving ordinary
existing calls, plus generated Node/Python query parameters and CLI flags. Go
rejects multiple filter objects. Filters describe the original recorded actor,
not the credential making the history read. Existing no-store, read scope, MFA,
page limits, timeouts and metadata-only response semantics remain unchanged.
