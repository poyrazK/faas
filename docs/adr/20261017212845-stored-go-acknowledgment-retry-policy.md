# ADR: Stored Go acknowledgment intent retry policies

- Status: Accepted
- Date: 2026-10-17

## Context

Adaptive retries exist in Go, Node and Python, but workers choose the next
settlement's policy. Rolling deployments or different settings can change timing
for the same callback. Bind a normalized policy atomically with new staging while
preserving unbound fallback.

## Decision

Add three nullable inline application-owned columns for version and initial/maximum
milliseconds. All null means unbound; all present requires version 1 and valid
positive caps up to one hour. Extend shared fresh DDL and provide a one-time
transactional upgrade. Updated Go staging/listing/recovery requires these columns
even for unbound work. Helpers never install DDL.

Add Go binding-aware completion, receipt-only, processing-fenced and negative
staging variants. Copy/normalize before operations; save callback and policy in
the existing receipt/business or negative-decision transaction. Lock the latest
intent under invocation serialization before duplicate comparison. Matching
normalized policy returns retained status; a differing policy or existing unbound
duplicate conflicts. Legacy staging preserves bindings and inserts unbound rows.

Bindings are immutable through SDK helpers; no adoption/update command is added.
Supersession keeps payload clearing and metadata history. Capture and validate
policy under the recovery row lock before recording a claim. It governs jitter
through settlement and overrides fixed/adaptive fallback. Missing columns and
malformed/unknown metadata fail closed. Keep claim, receipt, platform fence,
cancellation and uncertainty rules. Add optional payload-free policy status.

## Consequences

Schema upgrade precedes Go worker upgrade; compatible recovery precedes bound
staging. This increment makes only Go binding-aware. Node, Python and older Go
workers ignore the columns, so bound scopes must be exclusive to upgraded Go
recovery until parity is implemented. The constraint validates metadata rather
than worker compatibility.

Policies survive restart without changing identity or resetting counts/due times.
Worker timeouts/callback budgets still apply; validated fallback governs unbound
rows. There is no platform schema/registry, exhaustion, automatic disposition or
durable remote Retry-After change. See the
[stored policy contract](../event-acknowledgment-stored-retry-policy.md).

## Validation

Go SDK/example builds and vet passed. SQLC statically compiled all 14 staging
and recovery statements against both fresh and upgraded schemas without a
database. Shared schema generation parity, Node generation/build, source/DDL
comparisons, ADR and documentation checks passed. Existing callback/receipt/
conditional-settlement code remains byte-identical; the pass retains bounded
admission and joined shutdown. No tests/assertions were added or changed and no
local tests or SDK helper smoke checks were run. Hosted upgrade, replay,
concurrency, conflicts, precedence and uncertain-COMMIT qualification remains
pending.
