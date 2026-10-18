# ADR-1678: Read-only team order aggregation

Status: Accepted

## Context

The team Data API offers filtered, paginated orders but no business-data summary
contract. Summing one returned page can produce incomplete totals. Aggregation
must cover all matching accessible rows, retain current membership and order
RLS, and preserve integer cents above JavaScript's safe-integer range.

## Decision

Append `0012_team_order_summary.sql` to the teams starter. Its approved RPC,
`summarize_team_orders(team_id, status_filter, minimum_total, maximum_total)`,
is STABLE, SECURITY INVOKER, uses a pinned pg_catalog search path and revokes
PUBLIC execution. Normal owner permission setup grants it to the API login.

The function explicitly checks the verified subject's current membership for
the requested team. JWT tenant/role hints do not grant access. It then queries
current `api.team_orders` through ordinary RLS, including any additional
restrictive policies. Owners, admins and members can read a summary. Absent or
revoked membership returns SQLSTATE 42501 / HTTP 403, never a zero summary that
could be mistaken for an authorized empty dataset.

One GROUPING SETS aggregate computes the overall result and per-status buckets
from the same filtered statement snapshot. The response is one composite row:
`tenant_id`, `order_count`, `total_cents`, `by_status`. Buckets include every
declared order status in enum order, with explicit zeros for missing groups.
The current starter declares draft, submitted and fulfilled. Authorized empty
teams and filters with no matches return zero totals and all zero buckets.

Counts and sums cross the API as canonical decimal strings. SUM casts int4
amounts to exact numeric before aggregation, avoiding int8 monetary overflow.
No floating point is used. Optional amount filters are inclusive whole cents
in the existing order column's PostgreSQL integer range; null means absent.
Negative or reversed ranges and empty/null teams return PT400 /
`invalid_order_summary`; invalid enum/integer wire values retain PostgreSQL
input errors. No sort, pagination, arbitrary grouping or arbitrary SQL inputs
are accepted. Existing request, response and statement budgets apply.

`teamsClient.summary(filters)` preserves the native RPC builder, errors and
cancellation and disables retries. `parseOrderSummary(value, team, filters)`
validates exact fields, the expected team, canonical numeric strings, enum
order, totals equal to the sum of buckets, and compatibility with the selected
status/amount range. It returns copied, frozen values and never coerces counts
or money to Number. The paired client must be reviewed when the enum changes.

## Consequences and validation

This is a live read. The normal Data API statement sees committed membership
and orders at its snapshot; a request already in flight can finish against that
snapshot after a concurrent revocation. There is no membership cache, materialized
summary, additional write work, advisory lock or background job.

The fixed result size does not bound rows scanned. Large teams or broad filters
can hit existing deadlines; errors return no partial or cached totals. Existing
tenant/status and tenant/amount indexes serve filtered reads. This is a specific
customer-owned summary contract rather than unrestricted aggregate operators.

Client tests exercise precision, consistency, context binding, malformed input,
cancellation and native errors. Native PostgreSQL/PostgREST acceptance uses a
stable schema owner and a restricted RLS-active API role, checks current policies
and revoked memberships with unchanged JWTs, compares complete summaries beyond
the page cap, and verifies read-only execution. A disposable security-invoker
view models 5,000,001 int4 orders to prove exact HTTP totals above 2^53 with
little disk use. Statement cancellation verifies the serving budget remains
effective. Existing team workflows and packaged client types are also checked.
