# ADR: Go acknowledgment retry budgets, parking and guarded resume

- Status: Accepted
- Date: 2026-10-17

## Context

Stored v1 policies control timing but permit unlimited recovery claims. Operators
need a durable stop for undeliverable callbacks, preserving their original action,
capabilities and receipt evidence, plus an explicit way to grant more recovery.

## Decision

Add a separate Go RetryBudget type and five budget-aware staging methods. Existing
fallback RetryPolicy APIs retain their two-field contract. Store version 2 with
immutable initial max claims and a separate current total grant. Both are bounded
1..2147483647. Original staging remains idempotent after an operator raises the
grant; omitted/v1 staging cannot reset or downgrade a budgeted binding.

Extend application-owned fresh DDL and provide a one-time upgrade from v1 policy
storage. Keep protected callback inputs for parked rows, forbid their claims, and
tie parked state/exhaustion code to a v2 count at or beyond its current grant.
Preserve all other payload, claim, count and policy invariants. Helpers require
the expanded columns even for v1 work; no existing row is budget-enabled by DDL.

Under the existing claim lock validate policy/grant/count before increment. A due
expired crashed claim already at its limit parks without a capability. The final
allowed claim can confirm/supersede normally; only unconfirmed settlement parks.
Parking clears the local claim and preserves action, protected inputs, count and
due time. Explicit claim parking returns typed metadata only after known commit;
uncertain commit returns none. Passes count parking separately. Health includes
parked work as outstanding but excludes it from automatic eligibility metrics.

Resume requires timestamp/count/current-limit guards, no claim, no newer invocation
generation/attempt and a strictly larger total above consumed claims. Serialize
commands and invocation updates; commit grant/state change with a payload-free
receipt in an independently retained application-owned table. Retain conflicts and
missing outcomes too. Exact retries replay historical evidence without another
COMMIT; changed requests conflict and unknown commit returns no receipt. Resume
preserves the initial binding and due time. New staging supersedes parked history
and clears its secrets. No business disposition, callback, job acknowledgment or
platform renewal is introduced by parking/resume.

## Consequences

Upgrade schema before Go helpers, then exclusively use budget-aware Go staging,
recovery and diagnostics for enabled scopes until Node/Python parity. Shared fresh
schema packaging does not imply runtime support. Older version-aware workers fail
closed on v2; pre-binding workers must also be excluded. Applications own
authorization, scheduling, timeouts, command/receipt retention and parked alerts.
Budgets count committed claims including crashes, not HTTP sends. There is no
automatic negative disposition or pruning. See the
[budget and rollout contract](../event-acknowledgment-retry-budgets.md).

## Validation

Go SDK/example build and vet passed. PostgreSQL 16 canonical schema generation
produced matching fresh/upgrade columns, defaults, constraints and indexes, with
its temporary catalog removed. SQLC statically compiled all 27 staging, claim,
settlement, adoption, parking, resume and health statements against fresh, v1
upgrade and pre-policy upgrade paths. Shared schema generation parity, Node
generation/build and isolated Python regeneration (6520 byte-identical source/schema
files) passed. ADR uniqueness and docs route checks passed. Callback transport,
receipt preflight and conditional settlement SQL retain their existing fences.
Shared fresh DDL bytes match all three SDKs, the Node package and Python
wheel/sdist; generated schema/declaration files match the final Node build.
No tests/assertions are added or changed and no local tests or SDK helper smoke
checks are run. Hosted behavioral qualification remains pending for final claims,
crashes/unknown commit, parking, resume replay/conflicts, supersession, concurrency
and cancellation. Node/Python runtime parity is subsequent work.
