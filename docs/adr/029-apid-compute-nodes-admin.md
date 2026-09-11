# ADR-029 · apid Compute-Nodes Admin Surface

- **Status:** accepted v1.2 (2026-09-11). v1.2 moves routine CLI enrollment behind the authenticated API, makes deferred activation atomic, preserves metadata outside the enrollment payload, and emits trace-linked audit events.
- **Superseded (in part, PR-E):** prose referred to the monolithic
  `cmd/gatewayd/` daemon split by ADR-070 into `gatewayd-public` (TLS-only
  edge) and `gatewayd-internal` (routing + wake + proxy). Body is preserved
  verbatim; readers should substitute "gatewayd-internal" for the
  routing/wake/proxy path and "gatewayd-public" for the certmagic/TLS path.
  `cmd/gatewayd/<file>.go` citations in this body are stale; see PR-E for
  the new file locations.
- **Date:** 2026-07-22 (proposed); 2026-07-31 (accepted v1.1); 2026-09-11 (accepted v1.2)
- **Issue:** #98
- **Decision:** Add operator-facing CRUD on `compute_nodes` to apid:
  `GET /v1/compute-nodes`, `GET /v1/compute-nodes/{name}`, `POST
  /v1/compute-nodes`, `DELETE /v1/compute-nodes/{name}`. Gated on an email allowlist loaded
  from `FAAS_ADMIN_EMAILS`. RFC 7807 errors throughout.

## Context

ADR-028 makes `compute_nodes` the placement authority. Until now,
the only writer was schedd (the watchdog) or vmmd (self-
registration); an operator adding a new box to a fleet had to
`INSERT` SQL by hand.

That works for one node but breaks down at fleet scale: a typo in
`target_url` is a 503 from the customer's first request, not a
loud-fail at registration time. The slice needs an HTTP surface so
operators can pre-register a box (catch typos before vmmd boots),
audit active vs drained rows, and remove retired boxes.

## Decision

Four routes, all admin-gated, all RFC 7807:

| Method | Path                            | Purpose                                  |
|--------|---------------------------------|------------------------------------------|
| GET    | `/v1/compute-nodes`             | list; `?include_inactive=1` shows drained |
| GET    | `/v1/compute-nodes/{name}`      | detail plus live-instance count           |
| POST   | `/v1/compute-nodes`             | upsert by name (admin POST = idempotent) |
| DELETE | `/v1/compute-nodes/{name}`      | soft-delete (default) or `?hard=1`        |

**Auth:** Bearer-token auth (the same as `/v1/*` customer routes)
AND email allowlist membership. The allowlist is
`FAAS_ADMIN_EMAILS` (comma-separated) read at startup via
`server.WithAdminAllowlist`. Empty allowlist ⇒ every route 403
`admin_required` — there is no implicit "any authenticated caller
is admin" path. Customer-tier accounts never reach the handler;
even an account with a valid API key but a non-allowlist email
gets 403.

**Soft-delete vs hard-delete:**

- Soft-delete (default): flips `active=false` on the row. The
  `compute_node_changed` trigger (migration 00026) fires, gatewayd
  evicts its per-node client cache, schedd's watchdog treats the
  row as drained, and placement skips it. Re-POSTing with the
  same name reactivates (UPSERT).
- Hard-delete (`?hard=1`): `DELETE FROM compute_nodes WHERE id =
  $1`. Refused on the synthetic `default-local` row (HTTP 409
  `default_local_protected`) — every legacy instance row from
  migration 00024's backfill references it.

**Rate limiting:** The routes share `s.apiAuthLimiter` via
`s.authLimited`, so spec §11's 10/min/IP budget applies. A
brute-force on admin endpoints costs the attacker the same
budget they'd burn trying customer keys.

## Why this and not a separate daemon

A separate `admind` daemon would mirror the apid/gatewayd split,
but the operator surface for v1.0 is tiny (3 routes on 1 table)
and the auth/limiter/idempotent middleware already exists in apid.
Splitting the surface into a new binary would mean re-implementing
Bearer auth, RFC 7807, idempotency, and the per-IP limiter — work
that exists in apid and would be a duplicate. The right time to
split is when the admin surface grows beyond CRUD (e.g. live
config editing, billing overrides) — that's a v1.1 conversation.

## Consequences

- **Operator workflow:** A new box is added in three steps:
  1. Provision the box via the `overlay` ansible role.
  2. `gregalectl compute-nodes add` authenticates and `POST`s name,
     target_url, and capacity to apid. `--defer-activation` atomically stores
     lifecycle `unavailable` until readiness checks pass.
  3. vmmd self-registration UPSERTs the same row on first boot.
- **Metadata ownership:** re-enrollment preserves release, PKI, topology, and
  routing metadata not exposed by the request instead of clearing it through
  the state layer's full-set upsert.
- **Audit trail:** pg_notify `compute_node_changed` fires on every mutation,
  and successful enrollment emits `operator.action.node_enroll` with actor,
  reason, action, deferred-activation state, and request trace ID.
- **Break glass:** routine CLI enrollment never opens PostgreSQL. Direct
  mutation remains available only with explicit `--break-glass-db --yes` and
  an incident reason during an apid outage.
- **Default-local protection:** Hard-delete on `default-local` is
  refused at the handler; soft-delete is allowed (an operator
  draining the box is a valid operation). This matches the
  spec's "default-local is the backfill target" invariant.

## Out of scope

- Per-row RBAC beyond the email allowlist — a future slice adds
  scopes (e.g. "view-only operator").
- Live config editing of an existing node's `target_url` outside
  of the UPSERT path. Today, `POST` with the same name re-applies
  capacity; the operator also gets to rotate `target_url` by
  re-POSTing. The `compute_node_changed` trigger fires on UPSERT
  too, so gatewayd evicts the cached conn for the renamed IP.
- Tailscale ACL minting — operators own tailnet admin console.

## Reference call sites

| Site                                                | Change                                  |
|-----------------------------------------------------|-----------------------------------------|
| `cmd/apid/compute_nodes.go`                         | 3 handlers + admin allowlist + payload  |
| `cmd/apid/server.go`                                | `adminAllowlist` field + route mounts   |
| `cmd/apid/main.go`                                  | `WithAdminAllowlist(FAAS_ADMIN_EMAILS)` |
| `pkg/state/store.go`                                | `DeleteComputeNode` interface method    |
| `pkg/state/pgstore.go`                              | `DeleteComputeNode` SQL impl            |
| `pkg/state/memstore.go`                             | `DeleteComputeNode` in-memory impl      |
