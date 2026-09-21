# ADR-198 · Customer-addressable deployment revisions

- **Status:** accepted
- **Date:** 2026-09-21

## Context

A deployment row has always been immutable. `apid` never mutates a prior
deployment's artifact fields on redeploy — it marks the row `superseded` and
inserts a new one, and every rollout mechanism the platform has (traffic
splits, the canary ladder, rollback, mirroring) is built on that property.

What was missing was a *handle*. The only customer-facing identifier was
`deployments.id`, a uuid. Every surface that names a specific revision made
the customer copy one:

```
gregale rollback my-api --to 8f14e45f-ceea-467a-9c8e-9b0e21c6d5a1
gregale traffic set --deployment 8f14e45f-... --percent 10
```

That is the difference between a platform that "understands deployments" and
one that stores them. `v42` is typable, diffable, and speakable in an
incident; a uuid is none of those.

There was already a second, half-built notion of the same number.
`Store.DeploymentOrdinal` (ADR-122 / SAFE-RELEASES-C.2) computes a per-app
1-based ordinal with `row_number() over (partition by app_id order by
created_at, id)` and stamps it into the `deploy-{N}-{slug}.gregale.dev`
preview hostname. Its docblock carries an explicit stability contract:

> N MUST be stable for an existing row across runs so a previously-issued URL
> doesn't silently rot when the customer deploys a fresh row.

So the platform already showed customers a deployment number — it just never
let them use it, and recomputed it on every read.

## Decision

Add `deployments.revision`, a per-app monotonic integer, and accept it as
`v42` (or a bare `42`) anywhere the API and CLI take a deployment id.

**The column materializes `DeploymentOrdinal`; it does not compete with it.**
The backfill uses that method's exact window, and `DeploymentOrdinal` now
reads the column. There is one number, spelled one way, everywhere.

Three consequences follow from that choice, and each was the deciding factor
against an alternative:

1. **Not partitioned by scope.** A scope-partitioned counter (production and
   each `pr-N` preview owning independent ladders) reads better — production
   stays v1, v2, v3 with no gaps from open pull requests. It was rejected
   because it forks into a second N that disagrees with the ordinal already
   baked into issued preview hostnames, silently rotting them. The cost
   accepted instead is that a preview deploy consumes a production revision
   number, leaving gaps in the production sequence — which is already true of
   the preview hostnames today.

2. **Assigned under an existing lock.** `CreateDeployment` already holds
   `select 1 from apps where id = $1 ... for update` for the whole
   transaction, so `(select coalesce(max(revision), 0) + 1 ...)` in the same
   statement is serialized per app with no new locking and no sequence
   object. A per-app Postgres sequence was rejected: it would need one
   sequence per app, and sequences are not transactional, so a rolled-back
   deploy would burn a number and produce gaps that are *not* explainable to
   a customer. `deployments_app_revision_uniq` is the schema-side backstop if
   a future caller ever inserts without the lock.

3. **`RetryDeploymentFromStage` mints a new revision** rather than reusing the
   failed row's. A retry produces a new immutable row; two rows sharing a
   revision would break the handle's only real promise.

`revision = 0` is an "unassigned" sentinel for rows written by raw-SQL
fixtures predating the column. Both stores always assign a positive value, so
zero is unreachable through the supported write path. The API projection
omits it (`omitempty`) and the CLI prints the uuid instead, rather than
rendering a misleading `v0`. The unique index is partial (`where revision >
0`) so unassigned rows are not a collision with each other.

### Resolution and trust boundary

A revision is only meaningful relative to one app, so resolution is always
app-scoped:

- **`rollback`** resolves server-side (`server.resolveDeploymentRef`), because
  the route already carries the app. Every API client — SDKs, Terraform, the
  dashboard — gets `v42` for free.
- **`traffic set`** resolves client-side, because `PATCH
  /v1/deployments/{id}/traffic` is addressed by deployment id alone and has no
  app context. The CLI gains an optional `--app` flag, required only when the
  reference is a revision.

An unknown revision returns the same not-found problem an unknown uuid does,
so the two reference forms are indistinguishable to a prober and the existing
IDOR posture is unchanged. A uuid always contains non-digit characters, so it
can never be misparsed as a revision.

## Consequences

- Rollout surfaces become typable: `gregale rollback my-api --to v41`.
- `DeploymentOrdinal` is now a point read on `deployments_pkey` instead of a
  full-table window scan, and is strictly more stable — a stored value cannot
  shift when an earlier row is removed, while `row_number()` can.
- Production revision sequences contain gaps whenever PR previews or retries
  land between production deploys. This is intentional; see decision 1.
- Pre-existing rows keep the ordinal their preview hostnames were issued with,
  because the backfill mirrors the ordering `DeploymentOrdinal` used.
- Two parse implementations exist (`cmd/apid/deployment_ref.go` and
  `cmd/gregale/deployment_ref.go`) and must agree on what `v42` means. They
  are each ~20 lines and deliberately duplicated rather than shared, because
  `cmd/gregale` must not import `cmd/apid`. The divergence risk is pinned by a
  19-case table asserted identically on both sides —
  `cmd/apid/deployment_ref_test.go::TestParseDeploymentRevisionRef` and
  `cmd/gregale/deployment_ref_test.go::TestParseRevisionRef`. The two tables
  must stay byte-identical; a change to one parser that is not mirrored fails
  the other side's test. If they ever diverged, a customer would watch the same
  string resolve in `traffic set` (client-side) and 404 in `rollback`
  (server-side).

## Validation

`pkg/state/conformance` gains
`deployment_revisions_are_monotonic_and_addressable`, which runs against both
`MemStore` and `PgStore`. Per that suite's standing rule it asserts absolute
values (1, 2, 3) rather than that the two stores agree — an agreement check
would have passed while both implementations were identically wrong, which is
exactly how the uppercase-state-literal bug survived.

The case pins: revisions are 1-based and increment per deploy; a preview-scope
deploy shares the app's single ladder; `DeploymentOrdinal` equals the stored
revision for every row; `DeploymentByRevision` resolves the right row and
returns `ErrNotFound` for unknown, zero, and negative revisions; and a second
app has an independent ladder whose numbers do not leak across apps.
