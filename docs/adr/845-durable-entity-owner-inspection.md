# ADR-845: Account-scoped durable entity inspection

Status: Implemented locally; verification pending.

## Decision

Expose `GET /v1/apps/{slug}/entities/inspect` to the app's owning account with
`apps:read` or admin scope and the existing MFA requirements. The preview engine
and app allowlist must be configured; inspection remains available while the
outbox relay and guest v2 flags are off. Customer self-service tokens cannot use
this route.

Required query selectors are `namespace` and `key`. Optional selectors are
`environment` and `platform_tenant_id`. Unknown, repeated and empty selectors
are rejected. Account and app identity come from authentication and app lookup;
project environments resolve to their current immutable catalog identity.
Deleting and recreating an environment does not expose its predecessor's state.
Account owners may inspect suspended customers and existing state after a plan
downgrade. This exception grants no execution or mutation permission.

The engine reads and verifies one manifest and its committed snapshot. It does
not acquire a lease, create an entity, repair an index, list objects or run guest
code. The response includes the entity scope, business version, whether any
transition committed, alarm deadline/attempts/exhaustion, pending outbox count
and head identity/attempts/exhaustion. State data, message payloads, receipts,
lease owners/tokens and provider object keys are excluded. Responses carry
`Cache-Control: private, no-store`.

Missing entities return 404. Missing or corrupt committed snapshots fail closed
with 503, rather than presenting an empty entity. A missing snapshot whose
manifest changed during reclamation is a retryable conflict. A request has the
existing bounded durable-entity request timeout.

## Delivery observation

For the observed head only, an existing webhook delivery row may provide
`pending`, `in_flight`, `succeeded`, `failed` or `dead`. Its delivery ID, account,
app and complete entity/message envelope must match. A missing/pruned row,
lookup error, malformed envelope or scope mismatch reports `unknown`.

This SQL observation happens after the bucket read and is not an atomic
cross-store snapshot. `unknown` does not prove that work was never accepted.
An empty outbox does not prove receiver completion. Inspection neither queries
nor replaces the durable transport acceptance receipt. No public retry, payload
export, lifecycle deletion or historical delivery guarantee is introduced.

## SDKs and source correction

The Go client exposes `InspectDurableEntity` with exact uint64 versions. Node
and Python clients are generated from OpenAPI. The Node convenience helper
`inspectDurableEntity` additionally rejects imprecise numeric versions and
missing required selectors.

The reservation example and shared guest fixture now use the engine's actual
initial `{}` state at version zero. The example refuses a committed empty
object, so it cannot silently reset an existing entity.

## Verification handoff

No tests, builds, lint, native runtime or live provider checks were run here,
per the user's instruction. Source generation, formatting and diff review are
not qualification evidence. Added engine, API and SDK cases cover read-only
inspection under an active claim, initial/missing/corrupt snapshots, read scope,
metadata redaction, strict selectors, unknown delivery history, exact envelope
scope, encoded selectors and version precision.

The testing agent should run the relevant Go packages and nested Go SDK tests,
Node SDK tests including `durable-entity-inspection.test.ts`, reservation example
tests, and Python generation/import checks. Also qualify suspended tenant and
plan-downgrade reads, cross-account and environment isolation, customer token
rejection, exhausted alarm/outbox metadata, snapshot reclamation races and
provider failures. Native KVM/private bucket acceptance remains separate.

Work remains local. No PR or deployment is created; feature gates retain their
existing disabled defaults.

ADR-846 adds the separate owner recovery endpoint and an opaque comparison
revision to inspection. The read endpoint itself remains non-mutating.
