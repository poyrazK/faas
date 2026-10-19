# ADR-1619: Filtered retained request discovery through management MCP

## Status

Accepted. Extends ADR-1618.

## Decision

Expose read-only `gregale_list_requests` using the existing native filtered and
cursor-paginated debugger endpoint. Accept app, positive lookback duration/day
alias, exact route path/template, deployment UUID, exact HTTP status, minimum
latency, optional cold/warm selection, page size and opaque native cursor.
Centralize transport budgets in `pkg/api/limits.go`: default/max 20/50 rows,
8192-byte cursor, 64-byte lookback and 86400000-ms minimum-latency ceiling.
Routes use the existing 256-byte diagnostic field budget and exclude query,
fragment and control characters. Strictly validate supplied lookbacks so native
fallback for malformed durations cannot silently widen an agent's selection.

Reuse the separately consented per-app diagnostics permission across environments.
The new typed argument's deployment ID is a diagnostic filter, not a grant to
ordinary deployment inspection. The shared authorization wrapper checks its app;
the discovery handler checks current diagnostic consent and immutable app identity,
then verifies the filter deployment belongs to that app before reading a page.
Other deployment tools retain their independent environment authorization.

Read one native page only. Never locally truncate/reorder rows or automatically
walk pages. Validate requested page bounds, echoed filters, retained window and
cursor/completeness consistency, and each row's filter/window membership. Preserve
native effective lookback, retention-clamped flag, pinned window, completeness and
opaque cursor. Native errors, including cursor/filter mismatches and retention
expiry, are returned without retrying a broader query or resetting the window.
Return `next_arguments` with the original filters/lookback, page size and cursor.
The native API remains authoritative for ownership, entitlement and retention on
every page, including invalidation after a changed retention horizon.

Reuse the redacted request projection and bounded output. Include a safe native
row/public lookup identifier for drill-down; counts retain aggregate semantics.
Deduplicate deployment identity reads within a page, recheck every attributed
deployment's app and scope after final generic authorization, then check the app
identity last before serialization. Unattributed rows retain unavailable scope.
Invalid native responses fail closed; an empty or terminal page does not establish
health, full capture or root cause.

Extend `gregale_request_diagnosis` with optional request ID and discovery filters.
Exact-ID mode stays exact; filters and an exact ID cannot be combined. Discovery
mode instructs the agent to select relevant candidates before evidence drill-down,
preserve pagination filters/window and report retention/coverage limits. Prompt
retrieval fetches no request evidence. Completion suggests consented apps only;
private request IDs and filter values are not enumerated by completion.

## Consequences

An agent can investigate recent slow/failing requests without a supplied ID.
No database migration, native API mutation, replay, probe or automatic collection
is added. Existing grants, refresh/restart and revocation keep their ADR-1618
permission boundaries. Portable and real Codex fixtures verify the bridge contract
and consent behavior; native/provider deployment qualification remains separate.
