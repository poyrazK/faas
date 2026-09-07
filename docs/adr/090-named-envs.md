# ADR-090 — Named envs: `app_envs.scope` + scope-aware layered merge (issue #395 follow-up)

- **Status:** proposed
- **Date:** 2026-08-10
- **Closes:** ADR-045 §Decision 7 ("API env is per-app, not per-deployment"),
  the in-flight `app_envs` mutation surface, and the named-env gap surfaced
  in the 2026-08-10 secrets+envs roadmap.
- **Depends on:** ADR-045 (`app_envs` table + 3 routes + 4-layer env merge),
  ADR-035 (audit kind taxonomy — `env.set` / `env.deleted` widens to include
  `scope`), ADR-053 (deploy-time env override shape that this ADR mirrors).

## Context

ADR-045 shipped `app_envs` as a per-app, single-scope, plaintext env store:
one row per `(app_id, key)`, all rows land in the guest's process env
together, precedence is `secrets > api_env > manifest_env > os.environ`.
The shape is correct for the "global runtime config" use case (LOG_LEVEL,
FEATURE_X, SENTRY_DSN) but it cannot answer the staging vs production split:

> "I want `LOG_LEVEL=info` on prod and `LOG_LEVEL=debug` on staging, with
> one shared image and per-environment env overrides that survive a
> redeploy."

Today the customer either (a) maintains two apps (waste of plan quota +
double the cold-boot cost), (b) writes `LOG_LEVEL` into the per-deployment
`overrides.env` map (ADR-053) — which costs a redeploy on every change and
loses the "applies on next wake" property of `app_envs`, or (c) routes
traffic by URL prefix and runs separate deployments of the same image
manually — operationally fragile.

The gap was tracked in the 2026-08-10 secrets+envs roadmap: **Phase 2**
(ADR-090) adds an `app_envs.scope` column so a single app can hold
multiple named env rows that merge per-scope at wake time. **Phase 3**
(ADR-091, future) widens `deployments.scope` so a deployment's `overrides`
can target one of the app's named scopes, closing the loop.

This ADR ships Phase 2. The wake-time layered merge in guest-init
(`BuildEnvWithSecrets`) is widened to accept a nested `map[string]map[string]string`
when multiple scopes exist, and a backwards-compatible flat
`map[string]string` when only `default` is in use — old guests read old
files; new guests read both.

## Decisions

### D1. `app_envs.scope` column (default `'default'`)

A new column on `app_envs`:

```sql
-- migrations/00193_app_envs_scope.sql
--
-- Widen the PK to (app_id, scope, key) so the same (app, key) can be
-- bound to multiple named scopes (e.g. default vs staging). Sits on
-- top of migration 00099 (orgs expansion), which added a nullable
-- org_id column and did NOT change the PK; this migration is the
-- first PK change since 00061.

alter table app_envs
  add column scope text not null default 'default';

alter table app_envs
  drop constraint app_envs_pkey;

alter table app_envs
  add primary key (app_id, scope, key);

alter table app_envs
  add constraint app_envs_scope_shape
    check (scope ~ '^[a-z0-9][a-z0-9_-]{0,63}$');

-- Composite index for the LIST path that filters by account_id +
-- app_id and orders by scope (so the ?scope=__all__ response sorts
-- scopes deterministically client-side).
create index if not exists app_envs_account_app_scope_idx
  on app_envs (account_id, app_id, scope);
```

- **PK widens** from `(app_id, key)` to `(app_id, scope, key)`. Scope is
  the outer key because nested merge precedence at wake is
  scope-targeted (D4) — the row's scope is the index it routes on.
- **Default value** is `'default'` so all existing rows backfill without
  an explicit migration rewrite (the column DEFAULT handles the backfill
  atomically — no `UPDATE … SET scope = 'default'` statement needed).
- **Scope shape** mirrors the existing `^[a-z0-9][a-z0-9_-]{0,63}$`
  pattern used by deployment slugs (`validSlug()` at
  `pkg/api/appmanifest.go:148`) — POSIX-portable, DNS-friendly, no upper
  case to keep case-insensitive collision away. The same regex is the
  Go-side validator (D2) so the DB CHECK and the handler stay in sync.
- **Reserved scope strings**: `default` and `__all__` are the only ones
  with special meaning. `default` is the implicit scope every app
  starts with; `__all__` is a sentinel used by `GET /v1/apps/{slug}/env`
  with `?scope=__all__` to mean "every scope" (D3). No other reserved
  names; the DB does NOT enumerate them because we do not want a future
  scope (e.g. `preview`) to require a migration. The DB CHECK on the
  shape allows `default` and `__all__`; the handler-layer
  `ValidateScope()` rejects `__all__` on the PUT/DELETE paths (D2).
- **`org_id` interaction**: 00099 added a nullable `org_id` column.
  The orgs surface was deliberately NOT propagated to the public
  `pkg/state/types.go::AppEnv` Go API — it lives only in the sqlc-
  generated row model. This ADR follows the same posture: `scope` is
  part of the public Go API (every consumer needs it for merge
  semantics) while `org_id` remains a sqlc-only column. PR-A of the
  cluster adds `Scope` to `AppEnv`; nothing in this ADR changes the
  `org_id` shape.

### D2. New scope query parameter + path segment on env routes

**GET `/v1/apps/{slug}/env`** gains an optional `?scope=<name>`:

```
GET /v1/apps/{slug}/env             → returns {env, ...} for scope=default only
GET /v1/apps/{slug}/env?scope=staging → returns {env, ...} for staging only
GET /v1/apps/{slug}/env?scope=__all__ → returns {env_by_scope: {default:{...}, staging:{...}}}
```

The default behavior (no `?scope=`) is **unchanged** — returns the
`default`-scope env rows, exactly as ADR-045 did. This is the load-bearing
backwards-compat property: every existing CLI consumer
(`gregale env list --app <slug>`, dashboard, third-party integration)
keeps working without a code change.

**PUT `/v1/apps/{slug}/env/{key}`** gains `?scope=<name>`:

```
PUT /v1/apps/{slug}/env/LOG_LEVEL?scope=staging
Body: {"value":"debug"}
```

Default scope when `?scope=` is absent is `default`. A 400 returns when
the scope string fails the D1 shape check.

**DELETE `/v1/apps/{slug}/env/{key}`** mirrors the same `?scope=`
parameter. Deleting from `default` is unchanged; deleting from a
named scope requires explicit `?scope=`.

The `?scope=__all__` value is reserved for the GET path only — PUT and
DELETE reject it with 400 `env_scope_invalid`. Mutating every scope at
once is not a supported operation in v1; it would have ambiguous audit
semantics ("which scope did this set belong to?").

### D3. Wire shape: backwards-compatible nested map on the wake path

The wire shape widens from `map[string]string` (ADR-045) to support both
shapes via a discriminated union:

```go
// pkg/api/env.go — new DTO
type ScopedAppEnvResponse struct {
    Key       string `json:"key"`
    Scope     string `json:"scope"`     // always present
    CreatedAt string `json:"created_at"`
    UpdatedAt string `json:"updated_at"`
}

type AppEnvListResponse struct {
    // Additive: zero-value (nil) when ?scope=__all__ was NOT passed.
    // Single-scope GET (?scope=<name> or default) populates Env
    // (the existing flat shape), EnvByScope remains nil.
    Env []AppEnvResponse `json:"env,omitempty"`
    // Additive: populated only when ?scope=__all__ is passed. Each
    // key is a scope name; each value is the per-scope env rows.
    EnvByScope map[string][]AppEnvResponse `json:"env_by_scope,omitempty"`

    Quota int `json:"quota_max"`
    Count int `json:"count"`
}
```

**Why a discriminated union, not always-nested:** the SDK generator
walks `pkg/api/*.go` for response types; a flat `Env []AppEnvResponse`
is the most-compatible shape for the 95% case (single-scope GET) and
keeps `env_pull`-style tooling simple. `env_by_scope` is only populated
on the explicit `?scope=__all__` request; clients that never ask for
all scopes never see the nested field.

### D4. Wake-time layered merge — scope-aware precedence

Today the schedd path is `Engine.loadAPIEnv` (`pkg/sched/engine.go:4153`)
→ `[]fcvm.APIEnvEntry{Key, Value}` → `AppSpec.APIEnv` proto field 9 →
vmmd's `Manager.Wake` marshals to a flat `map[string]string` and writes
`/etc/faas/env.json` (`pkg/fcvm/manager.go:2104-2125`). guest-init's
`loadAPIEnv` decodes the file as a flat map (`guest/init/env_linux.go:44`).

**Widened contract (Phase 2):**

- **schedd** still emits `[]fcvm.APIEnvEntry`, but each entry carries a
  `Scope` field. The schedd remains scope-agnostic — it does NOT pick
  which scope to merge; vmmd does that. Reason: a wake may be
  scope-targeted in the future (ADR-091 Phase 3 wires the deployment's
  scope into the wake path; not in this ADR) and we don't want to
  revisit schedd then.
- **vmmd** does the scope-aware merge. Given the entries + a target
  scope (default: `default`), vmmd:
    1. Filters entries where `scope == target` OR `scope == 'default'`
       (default is always merged — it's the implicit fallback).
    2. Group keys by scope. **For each key, the more specific scope
       wins** (e.g. `LOG_LEVEL=debug` in `staging` beats `LOG_LEVEL=info`
       in `default`). This is the "nested overlay" semantics — same
       pattern as Kubernetes ConfigMap overlays.
    3. Marshals to JSON. The format is backwards-compatible:

    ```jsonc
    // Single-scope wake (only `default` rows present, OR target scope
    // == 'default' and only default rows qualify):
    {"LOG_LEVEL":"info","FEATURE_X":"on"}

    // Multi-scope wake (default + staging both have rows for the target
    // scope, OR target scope != 'default' and default still layers in):
    {"default":{"LOG_LEVEL":"info"},"staging":{"LOG_LEVEL":"debug"}}
    ```

    vmmd chooses the format based on `len(scopeSet) <= 1`. Always-flat
    when only `default` is in play — backwards-compatible with old
    guest-init.
- **guest-init** widens `loadAPIEnv` to accept both shapes:

    ```go
    func loadAPIEnv(log *slog.Logger) (map[string]string, error) {
        // First try the nested shape: {"default":{...},"staging":{...}}
        // If any top-level value is a JSON object, treat as nested.
        // Otherwise decode as the legacy flat map[string]string.
    }
    ```

    The new code path iterates entries across all scopes and overlays
    them in the order: `default` first, then named scopes in
    alphabetical order (stable, deterministic — same key ordering as the
    existing JSON marshal). The merged flat map is what
    `BuildEnvWithSecrets` already consumes — the 4-layer precedence
    (`secrets > api_env > manifest_env > os.environ`) is preserved.

    **Why alphabetical scope order, not insertion order:** the wire is
    JSON; json.Unmarshal of a map does not preserve insertion order in
    Go. Alphabetical sort is the deterministic contract the guest can
    reason about. Tests pin this in `guest/init/env_linux_test.go`.

### D5. Audit payload widens with `scope`

The existing `env.set` / `env.deleted` audit kinds keep their taxonomy
position (no new kinds — `scope` is a property of the env row, not a
distinct operation). The audit payload widens from `{app_id, name}` to
`{app_id, name, scope}`:

```go
s.audit.Emit(r.Context(), "env.set", &acct.ID, map[string]any{
    "app_id": app.ID,
    "name":   key,
    "scope":  scope, // ADR-090 — "default" for the implicit path
})
```

**Why widen payload, not add `env.scope_set`:** every existing
`env.set`/`env.deleted` consumer (dashboards, SIEM, GDPR export)
already deserializes the payload as `map[string]any`; a new optional
key is backwards-compatible. Old consumers ignore the field; new
consumers can filter by `data.scope != "default"` to surface named-
scope activity. No SQL migration is needed (`events.data` is JSONB).

**GDPR interaction:** the existing `listEventsForAccountExport` union
already reads `data` as `map[string]any`; the new `scope` key flows
through unchanged. No GDPR code change.

### D6. Quota: `EnvVarsMax` keeps its per-app meaning; per-scope cap deferred

The quota `Limits.EnvVarsMax` continues to bound **total env rows across
all scopes** for an app. Free 16 / Hobby 32 / Pro 64 / Scale 256 are the
same numbers; they bound the sum of `default + staging + preview + …`.
A per-scope cap (`EnvScopesMax`) is **not introduced** in this ADR:

- ADR-045 already chose the simpler posture (no per-key quota, just
  per-app row count). Adding per-scope complexity in the same ADR
  makes the rollout review heavier.
- The 8/32/64/256 numbers bound blast radius well — a customer cannot
  wedge a runaway process by creating 10,000 staging scopes because the
  total row count is still capped.
- The dashboard-side use case (per-deployment scope assignment, ADR-091)
  is the natural place to introduce `EnvScopesMax` if dashboards ever
  need it. ADR-091 is the explicit dependency for that cap.

### D7. No sealed secrets per scope in Phase 2

The 2026-08-10 roadmap decision is **sealed secrets stay per-app, not
per-scope**, in Phase 2. The `(account_id, app_id, key)` PK on
`app_secrets` does NOT widen; secrets remain a single flat namespace per
app. Rationale:

- Sealing is a per-app concern (the host key is one-per-host). A
  per-scope seal would require either a second envelope layer (cost:
  twice the seal bytes, twice the unseal compute) or a per-scope
  key-derivation (cost: per-scope key material in PG).
- The customer can express "different DB credentials per environment"
  via the existing deploy-time `overrides.env_secrets` map
  (ADR-053 §Decision 4): bind `DATABASE_URL=secret:STAGING_DB_URL`
  in staging overrides, `DATABASE_URL=secret:PROD_DB_URL` in prod
  overrides. The secret names are stable across scopes; only the
  binding changes. This is the v1 contract.
- Per-scope sealed secrets is **explicitly deferred** to a future ADR
  (likely ADR-092 or later) once the dashboard has a "scope secrets"
  UI surface to drive.

## Files

### New

| Path | Purpose |
|---|---|
| `migrations/00193_app_envs_scope.sql` | `scope` column + new PK + scope-shape CHECK. |
| `migrations/00193_app_envs_scope_test.go` | Slot-fence + idempotency + default backfill test. |
| `pkg/api/env.go` (extended) | `ScopedAppEnvResponse` + `EnvByScope` on `AppEnvListResponse`. |
| `pkg/api/env_scope.go` (new) | `ValidateScope()` helper + reserved-name handling. |
| `pkg/api/env_scope_test.go` (new) | Round-trip scope-shape + reserved-name tests. |
| `pkg/fcvm/api_env_scope.go` (new) | `APIEnvEntry.Scope` field + scope-aware merge helper. |
| `pkg/fcvm/api_env_scope_test.go` (new) | Single-scope backwards-compat + multi-scope overlay tests. |
| `guest/init/env_linux.go` (extended) | Nested-shape decode + alphabetical scope overlay. |
| `guest/init/env_linux_test.go` (extended) | `TestLoadAPIEnv_NestedShape_MergesScopes` + ordering test. |
| `cmd/e2e/named_envs_e2e_test.go` (new) | Full PG-backed: PUT default + staging, wake, assert merged. |
| `docs/adr/090-pr-cluster-outline.md` (new) | PR-A/B/C split (same shape as ADR-089). |

### Modified

| File | Change |
|---|---|
| `pkg/state/store.go` | `AppEnv` struct gains `Scope`; `UpsertAppEnv` / `DeleteAppEnv` / `ListAppEnv` / `CountAppEnv` widen to accept / return scope. |
| `pkg/state/pgstore.go` | Queries widen: PK columns now include `scope`; INSERT/UPDATE/DELETE rewrite to `(app_id, scope, key)` triple-key. |
| `pkg/state/memstore.go` | Mirror. |
| `pkg/api/apikey.go` | `validScopes` accepts `env:read` / `env:write` unchanged — scope is per-row, not per-token. |
| `cmd/apid/handlers_env.go` | `?scope=` parsing on all three routes; audit payload includes `scope`; default-scope path is byte-identical when `?scope=default` (or absent). |
| `cmd/apid/handlers_env_test.go` | Scope-parse + quota-with-mixed-scopes + nested-list tests. |
| `pkg/sched/engine.go` | `loadAPIEnv` returns `[]fcvm.APIEnvEntry` with `Scope` populated; no merge logic at this layer. |
| `pkg/fcvm/manager.go` | `stageAPIEnv` writes nested JSON when `len(scopeSet) > 1`; flat JSON otherwise. |
| `pkg/fcvm/vmm.go` | `VMM.StageAPIEnv` signature unchanged — still accepts a JSON blob. |
| `api/proto/onebox/faas/vmmd/v1/vmmd.proto` | `APIEnvEntry` gains `string scope = 2;` (proto3 default-empty = "default" legacy semantics). |
| `api/openapi.yaml` | `?scope=` parameter on the three env routes; `env_by_scope` field on `AppEnvListResponse`; `scope` field on `ScopedAppEnvResponse`; new error responses (`env_scope_invalid`, `env_scope_reserved`). |
| `pkg/apid/openapi.yaml` | `make spec-sync` mirror. |
| `docs/ops/named-envs.md` (new) | Operator runbook: how to migrate from per-app env to per-scope env. |

## Consequences

### Positive

- **Staging vs prod from one image.** A customer can PUT `LOG_LEVEL=debug`
  in `staging` and `LOG_LEVEL=info` in `default`, redeploy without a
  rebuild, and vmmd's overlay gives each scope the right value at wake.
- **Redeploy preserves scoped env.** Same wire contract as ADR-045 §7
  — env rows survive any number of redeploys because they're
  per-(app, scope), not per-deployment. The dashboard's "this scope's
  env" view is just a filtered `GET /v1/apps/{slug}/env?scope=…`.
- **Audit signal is unambiguous.** A `env.set` audit row with
  `data.scope=staging` is filterable in the customer audit log without
  a join; a row with `data.scope=default` is the legacy global view.
- **Backwards-compatible wire.** `?scope=` absent == `?scope=default` ==
  the existing flat-map wire == the existing flat-shape env.json on
  disk == the existing guest-init code path. Every old client, old
  CLI, old guest binary keeps working.

### Negative

- **guest-init grows a nested-decode branch.** `loadAPIEnv` gains ~30
  lines of `json.RawMessage` probe + iterate-entries. The new branch
  is non-load-bearing for the 95% single-scope case — it runs only when
  vmmd wrote a nested JSON. Pinned by `TestLoadAPIEnv_FlatShape_StillWorks`.
- **Wake payload widens.** `APIEnvEntry` gains a `Scope` field; the
  proto regeneration is mechanical. vmmdgrpc clients built against
  the old proto see an empty `scope` string which the handler maps to
  `default` — wire-compatible with the PR-A rollout.
- **Quota is global across scopes.** A customer cannot use 4 staging
  scopes + 4 prod scopes to spend 8× the cap. The `EnvVarsMax` total
  is the same number; this is intentional (D6).
- **Pre-existing CLI/SDK gaps are not closed by this ADR.** The
  inventory surfaced three that pre-date ADR-090 and are explicitly
  **out of scope**:
    1. `cmd/gregale env pull|push` (`commands5.go:179-345`) currently
       reuses the sealed-secret HTTP API (`ListSecrets` / `SetSecret`)
       rather than the dedicated env endpoints. A customer running
       `gregale env push --app <slug>` writes to `app_secrets`, not
       `app_envs`. ADR-090 does not change this; a follow-up that
       introduces `gregale env list/set/unset` (using the new
       `?scope=` parameter) is the right surface.
    2. The Go SDK (`sdk/go/scopes.go`) does not re-export
       `ScopeEnvRead` / `ScopeEnvWrite`; only the first 7 scopes are
       re-exported. The Python + Node SDKs have full env DTO coverage.
    3. The `gregale env list/set/unset` subcommands do not exist yet —
       the only `env` subcommands today are `pull` and `push`.

  These gaps are tracked as **out of scope (deferred)** below; closing
  them is the natural follow-up once the API surface ships.

### Out of scope (deferred)

- **CLI surface** — `gregale env list/set/unset --scope <name>` to
  drive the new `?scope=` parameter. PR-A ships the API; PR-D
  (cluster follow-up) closes the CLI gap.
- **Go SDK env-scope re-export** — `ScopeEnvRead` / `ScopeEnvWrite`
  in `sdk/go/scopes.go`. Mechanical; PR-A or PR-D.
- **Per-scope sealed secrets** — see D7. Deferred to a future ADR
  once the dashboard has a "scope secrets" UI surface to drive.
- **Per-scope cap (`EnvScopesMax`)** — see D6. ADR-091 (Phase 3) is
  the natural follow-up if dashboards ever need it.
- **Wake-time scope selection by deployment** — Phase 3 (ADR-091)
  will wire `deployments.scope` into the schedd's wake path so a
  deployment targets one of the app's named scopes. ADR-090 does
  NOT touch schedd's wake-target selection — every wake still merges
  the `default` scope; named scopes layer on top per D4.

### Compatibility

- `app_envs` table widens (additive column + PK change). All existing
  rows backfill `scope = 'default'` via the column default. No
  data-loss migration.
- `AppEnvResponse` widens: `Env` field is unchanged when `?scope=` is
  absent or `default`. New `EnvByScope` field is `omitempty`.
- `PutAppEnvRequest` is unchanged — scope is in the query string, not
  the body. Body remains `{value}`.
- Audit payload widens: existing consumers see an extra `scope` key in
  the `data` map; consumers that don't read the field are unaffected.
- `/etc/faas/env.json` wire format is dual-shape: flat when only one
  scope is in play (legacy), nested when multiple are. vmmd chooses
  the format; guest-init handles both.

## Rejected alternatives

- **Make `scope` required and reject the no-scope path.** Rejected:
  breaks every existing dashboard / CLI / third-party integration that
  calls `PUT /v1/apps/{slug}/env/KEY` without `?scope=`. The implicit
  `default` mapping is the simpler migration story.
- **Per-scope sealed secrets in the same ADR.** Rejected: doubles the
  audit kind surface (we'd need `secret.scoped_set` or similar) and
  introduces per-scope key material in PG. The deploy-time
  `overrides.env_secrets` map already handles the "different DB URL
  per env" use case (ADR-053). Deferred to a future ADR.
- **Always-nested wire on `/v1/apps/{slug}/env` even for the
  single-scope path.** Rejected: the existing flat `Env []AppEnvResponse`
  shape is the most SDK-friendly and the 95% case. `env_by_scope` is
  only populated when explicitly requested via `?scope=__all__`.
- **Move the merge logic to schedd instead of vmmd.** Rejected: vmmd is
  the natural seam because it already writes `/etc/faas/env.json`
  (the format discriminator). Moving the merge to schedd would split
  the file-format decision across two processes. vmmd owning both the
  filter and the marshal keeps the wire contract in one place.
- **`scope` as a JSON path inside the value column.** Rejected:
  bypasses the per-row SQL audit trail and the quota path. Storing
  scope as a top-level column keeps the `(app_id, scope, key)` PK and
  the `EnvVarsMax` count query simple.

## Acceptance

- `make test` — unit tests pass: scope-shape validator, quota count with
  mixed scopes, nested-decode guest-init, vmmd scope-aware merge, audit
  payload widening.
- `cmd/e2e/named_envs_e2e_test.go` — full PG-backed: seed default +
  staging rows, PUT to one scope, assert the other scope's rows are
  untouched; kill+restart apid, assert the rows survive; audit emit
  payload includes `scope`.
- `make metal-lima` — wake path unchanged for single-scope apps
  (flat env.json wire + flat guest-init decode); multi-scope case
  merged correctly per D4 alphabetical overlay.
- `make spec-check` — `api/openapi.yaml` and `pkg/apid/openapi.yaml`
  in sync; `?scope=` parameter + `env_by_scope` field + new error
  codes (`env_scope_invalid`, `env_scope_reserved`) all present.
- Migration 00193 backfills `scope='default'` on every existing row
  without explicit UPDATE statements (column DEFAULT does the work);
  smoke-tested against a copy of the reference node's PG.

## References

- `pkg/api/env.go` — the v1 DTOs this ADR extends.
- `pkg/fcvm/manager.go:2104-2125` — the v1 vmmd merge seam this ADR
  widens (scope-aware filter + nested marshal).
- `guest/init/env_linux.go` — the v1 guest-init decode path this ADR
  widens (nested-shape probe + iterate-entries overlay).
- `pkg/sched/engine.go:4153-4167` — the v1 schedd read path (unchanged
  shape, returns Scope on every entry; merge deferred to vmmd).
- `pkg/state/types.go:2544-2551` — the v1 `AppEnv` struct this ADR
  widens with `Scope`.
- `api/proto/onebox/faas/vmmd/v1/vmmd.proto:383-503` — the v1
  `APIEnvEntry` proto this ADR widens with `scope`.
- `docs/adr/045-app-env-mutable-store.md` — the foundation ADR whose
  D1 / D7 this ADR extends.
- `docs/adr/053-deploy-time-overrides.md` — the deploy-time env
  override map that complements `app_envs` (and which ADR-091 Phase 3
  will widen with `scope`).
- `docs/adr/035-auth-audit-events.md` — the audit-kind taxonomy this
  ADR's payload widening follows.
- Roadmap: 2026-08-10 `secrets-envs-roadmap-decisions-2026-08-10.md` —
  this ADR is Phase 2 of that three-phase plan.
