# ADR-1180: Paginated capacity-command history

Status: Accepted
Date: 2026-10-13
Related: ADR-1178, ADR-1179

## Context

The original capacity-command receipt can be inspected by UUID, but an operator may lose that UUID or need to discover recent successful changes. The existing retained table already holds the required metadata.

## Decision

Expose read-only GET `/v1/apps/{slug}/event-subscriptions/{subscriptionID}/delivery-control/capacity/commands` using read scope, MFA, a five-second request timeout and no-store responses. Return scope, normalized outcome filter, observed time, receipts, has_more and optional next_after. Outcome is any (default), applied or unchanged. Pages default to 25 receipts and allow 1 through 100; explicit HTTP limit zero is invalid. Quotas and cursor size live in pkg/api/limits.go.

Apply scope, logical expiry and outcome filters before a limit-plus-one SQLC query. Order descending by recorded_at and command UUID, with strict tuple comparison on continuation. Cursors are canonical base64url JSON, versioned and bound to account, app, subscription and normalized outcome filter; page size may change. Validate bounded size, canonical fields, nonzero command UUID and microsecond UTC timestamp. The cursor is a boundary, not a credential or signed audit evidence. Ownership is checked on every read.

Postgres uses a read-only repeatable-read transaction without locks. MemStore reads under its mutex, filters and sorts equivalent tuples, and decodes independent receipt values. Accessible targets with no matching retained evidence return 200 and an empty array. Missing owned targets return 404. Reads never prune receipts, reserve UUIDs, modify controls or extend deadlines. The existing primary key bounds per-subscription scans under the 128-receipt limit; no migration or extra index is added.

Expose the list through Go, Node and Python SDKs and `events subscription-capacity-commands APP SUB` with outcome, limit, after and timeout flags. The CLI fetches one page, verifies scope, original receipt consistency, uniqueness, ordering and continuation metadata, and supports JSON. It never follows all pages automatically or writes commands/files.

## Consequences

Each page is a fresh observation rather than a frozen export. Expiry can remove older receipts, and commands inserted above a continuation boundary require a refresh from the first page to discover. UUID ordering only breaks timestamp ties and does not establish causality, execution order or delivery order. History is evidence of successful original capacity requests, not current controls or actor attribution. Empty history does not prove that commands never ran.

No local tests are added or run under the user's constraint. Build and generator checks, SQLC parity, SDK coverage with prepared mappings, and static checks qualify implementation; database concurrency and retention qualification remain for CI. The pending operational table entry and GET SDK route mappings remain separate approval-gated registry patches.
