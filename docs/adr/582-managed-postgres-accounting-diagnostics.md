# ADR-582: Managed PostgreSQL accounting diagnostics

Status: accepted · 2026-10-05

## Context

ADR-569 and ADR-581 retain consumption obligations across deletion and uncertain
provider mutations. The account usage view reports aggregate freshness, but
operators cannot identify the local resources and coverage preventing admission.
Legacy unknown tombstones and unavailable provider history need an explicit
reconciliation workflow; diagnosing them is its first read-only step.

## Decision

Expose `GET /v1/admin/managed-postgres/accounting/{account_id}` behind the existing
admin scope, MFA middleware, and operator email allowlist. Return database IDs,
lifecycle state, identity-known/accounting-required flags, accounting root,
required and collected ranges, observation times, final correction deadlines,
and stable blocker codes. Never return opaque provider IDs, backend fingerprints,
lease tokens, credentials, connection URLs, or provider error bodies.

The local query resolves each catalog row and its effective coverage in one
snapshot. It shares the admission coverage query and freshness evaluator rather
than maintaining a separate interpretation of complete accounting. Shared restore
descendants report their root's creation, shutdown, and correction evidence.
Unknown legacy tombstones have no confirmed terminal deadline. Disabled policies
report `blocking=false` and empty reasons; identity and coverage metadata remain
visible. An empty reason list does not establish remaining budget or final invoice
settlement.

Use database-ID keyset pagination, default 50 and maximum 100 resources per page.
An extra row establishes whether `next_cursor` exists. Each page is a live local
view, not a frozen account-wide snapshot across requests. Account scope applies
before pagination. Include confirmed terminal resources and uncertain obligations;
exclude reservations that never started provider I/O unless already `ready`.

`gregale postgres diagnostics ACCOUNT_ID [--after UUID] [--limit N]` displays the
page in text or `--json`. Reads never contact a provider, advance coverage, settle
an obligation, or clear an accounting flag. Reconciliation mutations, retained
export import, provider history qualification, and final invoice settlement are
separate work.

## Validation

Memory and PostgreSQL regressions exercise pagination, account isolation,
unattempted reservations, unknown identities, legacy tombstones, shared restored
resources across month rollover, and cancellation. Boundary tests cover active
observation freshness, policy window changes, terminal backlogs, shutdown evidence,
and final correction deadlines. HTTP tests cover operator and MFA restrictions,
parameter validation, missing accounts, `no-store`, and safe projection. CLI tests
cover typed decoding, text/JSON output, and rejection before network I/O.
