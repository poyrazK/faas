# ADR-092 — Scoped sealed secrets: `app_secrets.scope` + per-scope wire surface (issue #879 follow-up)

- **Status:** proposed
- **Date:** 2026-08-18
- **Closes:** ADR-090 D7 ("per-scope sealed secrets deferred to a future
  ADR"), the `secrets + envs roadmap` 2026-08-10 §Phase 4 gap, and the
  `account_secrets_by_scope` deferred ask from the 2026-08-12 customer
  sync.
- **Depends on:** ADR-090 (named envs — reuses `?scope=` query param +
  `ValidateScope()` + the reserved-sentinel posture + the discriminated-
  union wire shape), ADR-089 PR-B (sealed-secret envelope + `kid` column +
  `secretbox.Seal` shape), ADR-091 (deployments.scope — closes the wake-
  time scope selection loop that the schedd pre-positioned for).

## Context

ADR-089 PR-A/PR-B shipped sealed secrets as a per-app, single-scope
flat namespace: one row per `(account_id, app_id, key)` in
`app_secrets`, all rows land in the guest's process env together. The
shape is correct for the "single production credential" use case but
cannot answer the staging vs production split:

> "I want `DATABASE_URL=postgres://prod-host/db` on prod and
> `DATABASE_URL=postgres://stg-host/db` on staging, with one shared
> image, sealed at rest, and no per-deployment redeploy."

Today the customer has two workarounds, both suboptimal:

1. **Per-deployment `overrides.env_secrets` map** (ADR-053 §Decision 4)
   binds `DATABASE_URL=secret:PROD_DB_URL` in the prod deployment
   overrides and `DATABASE_URL=secret:STAGING_DB_URL` in the staging
   overrides. The secret NAMES are stable; the binding changes.
   Cost: every env split requires a redeploy, defeating the
   "applies on next wake" property of `app_secrets`.
2. **One app per environment**. Waste of plan quota (Free 1
   deployed-app / Hobby 5 / Pro 25 / Scale 100), and every credential
   rotation fans out to N apps.

The gap was deferred by ADR-090 D7 ("No sealed secrets per scope in
Phase 2") as the explicit follow-up once the dashboard had the `?scope=`
parameter to drive. PR #849 (ADR-092 PR-A, merged 2026-08-18 at commit
`441043f7`) shipped the **data + wake plumbing** for per-scope secrets:
`app_secrets.scope` column, `(app_id, scope, key)` PK, schedd's
`loadSealedEnvFor(ctx, accountID, appID, scope, overrides)` strict-per-
scope resolution, vmmd's `SealedEnvEntry.Scope` for the per-scope
guest-mount demux. **This ADR + PR-B** ship the **customer-facing wire
surface** that completes Phase 4 of the 2026-08-10 roadmap.

## Decisions

### D1. `app_secrets.scope` column + PK widening (default `'default'`)

**Already shipped by PR-A** (PR #849, migration `00217`). Reaffirmed here
for completeness because the surface this ADR opens is impossible without
the PK widening.

```sql
-- migrations/00217_app_secrets_scope.sql (PR-A)
alter table app_secrets
  add column scope text not null default 'default';
alter table app_secrets
  drop constraint app_secrets_pkey;
alter table app_secrets
  add primary key (app_id, scope, key);
alter table app_secrets
  add constraint app_secrets_scope_shape
    check (scope ~ '^[a-z0-9]([a-z0-9-]{1,38})[a-z0-9]|__all__$');
```

- PK widens `(app_id, key)` → `(app_id, scope, key)` so the same
  `(app, key)` can be bound to multiple scopes (`prod` and `staging`
  each get their own `DATABASE_URL`).
- Default value is `'default'` — same posture as ADR-090 D1's `app_envs`
  column default. Every existing row backfills atomically without an
  explicit `UPDATE … SET scope = 'default'`.
- Shape mirrors `EnvScopePattern` (`pkg/api/env_scope.go:23`):
  `^[a-z0-9]([a-z0-9-]{1,38})[a-z0-9]|__all__$`. The Go-side validator
  (`api.ValidateScope`) and the DB CHECK are the same regex so the two
  stay in sync.
- Reserved scope strings: `default` and `__all__` — same posture as
  ADR-090 D1. `default` is the implicit scope every app starts with;
  `__all__` is the sentinel used by `GET /v1/apps/{slug}/secrets` with
  `?scope=__all__` to mean "every scope" (D3). Handler-layer
  `ValidateScope()` rejects `__all__` on PUT/DELETE/POST.

### D2. `?scope=` query parameter on the four secrets routes

**GET `/v1/apps/{slug}/secrets`** gains `?scope=<name>`:

```
GET /v1/apps/{slug}/secrets                          → {secrets:[...] for scope=default only}
GET /v1/apps/{slug}/secrets?scope=prod               → {secrets:[...] for prod only}
GET /v1/apps/{slug}/secrets?scope=__all__            → {secrets_by_scope:{default:{...}, prod:{...}, ...}}
```

Default behavior (no `?scope=`) is **unchanged** — returns the
`default`-scope secret rows. This is the load-bearing backwards-
compat property: every existing CLI consumer (`gregale secrets list
--app <slug>`, dashboard, third-party integration) keeps working
without a code change. Pre-PR-B callers see byte-identical responses
with `scope: "default"` echoed on every row (mirrors `AppEnvResponse.
Scope`'s `default` echo from ADR-090 PR-B).

**PUT `/v1/apps/{slug}/secrets/{key}`** gains `?scope=<name>`:

```
PUT /v1/apps/{slug}/secrets/DATABASE_URL?scope=prod
Body: {"value":"postgres://prod-host/db"}
```

Default scope when `?scope=` is absent is `default`. A 400 returns
when the scope string fails the D1 shape check.

**DELETE `/v1/apps/{slug}/secrets/{key}`** mirrors the same
`?scope=` parameter.

**POST `/v1/apps/{slug}/secrets/{key}/rotate`** mirrors the same
`?scope=` parameter. The rotation's per-scope audit emit gains the
`scope` field (D5).

The `?scope=__all__` value is reserved for the GET path only — PUT,
DELETE, and POST reject it with 400 `env_scope_reserved`. Mutating
every scope at once is not a supported operation in v1; the audit
semantics would be ambiguous ("which scope did this rotation belong
to?").

### D3. Wire shape: backwards-compatible nested map on the GET path

Mirror of ADR-090 D3. The wire shape widens from a flat list to a
discriminated union:

```go
// pkg/api/secrets.go — new DTOs
type ScopedAppSecretResponse struct {
    Key       string `json:"key"`
    Scope     string `json:"scope"`     // always present
    CreatedAt string `json:"created_at"`
    UpdatedAt string `json:"updated_at"`
    Kid       string `json:"kid,omitempty"`
}

type SecretByScope = map[string][]ScopedAppSecretResponse

type AppSecretListResponse struct {
    // Additive: zero-value (nil) when ?scope=__all__ was NOT passed.
    // Single-scope GET (?scope=<name> or default) populates Secrets
    // (the existing flat shape); SecretsByScope remains nil.
    Secrets []AppSecretResponse `json:"secrets,omitempty"`
    // Additive: populated only when ?scope=__all__ is passed. Each
    // key is a scope name; each value is the per-scope secret rows.
    SecretsByScope SecretByScope `json:"secrets_by_scope,omitempty"`
    Quota int `json:"quota_max"`
    Count int `json:"count"`
}
```

`AppSecretResponse.Scope` is **always populated** (even on the flat
list path with scope=`default`), mirroring `AppEnvResponse.Scope`. The
echo lets the CLI stamp render `prod/DATABASE_URL` without a second
lookup.

**Why a discriminated union, not always-nested:** the SDK generator
walks `pkg/api/*.go` for response types; a flat `Secrets []AppSecret
Response` is the most-compatible shape for the 95% case (single-scope
GET) and keeps `secrets_pull`-style tooling simple. `secrets_by_scope`
is only populated on the explicit `?scope=__all__` request; clients
that never ask for all scopes never see the nested field.

**Map shape is inline, not a named schema.** The corresponding Go DTO
is a type alias `SecretByScope = map[...]...`, which the AST parity
test in `cmd/apid/spec_compliance_test.go` deliberately filters out
(map types, not struct types). A named `SecretByScope:` schema entry
would surface as "schema in spec but no DTO in code" — env uses the
same inline posture for the same reason.

### D4. Strict per-scope resolution — no silent default overlay

**Sealed secrets do NOT layer the way env vars do.** ADR-090 D4 chose
"the more specific scope wins" for env vars (Kubernetes ConfigMap
overlay semantics — `staging:LOG_LEVEL=debug` beats `default:LOG_LEVEL=
info`). Sealed secrets deliberately **reject** that posture:

- Env vars are *runtime config* (LOG_LEVEL=info vs debug). Overlay is
  natural — the more-specific environment wins.
- Sealed secrets are *credentials*. If a customer writes
  `DATABASE_URL=postgres://prod-host/db` to `prod` scope and
  `DATABASE_URL=postgres://stg-host/db` to `staging` scope, a wake on
  the `prod` deployment must see exactly the `prod` value and nothing
  else. A silent overlay from `default` could mask a missing rotation:
  the customer removed the row from `prod` but didn't notice the
  `default` row was still there.

Schedd's `loadSealedEnvFor(ctx, accountID, appID, scope, overrides)`
filters rows where `secrets.scope == targetScope` — strict equality,
no overlay. vmmd's `SealedEnvEntry.Scope` carries the scope through
the wake path so the guest-init sees per-scope entries, not a merged
flat list. This is the opposite of env; the rationale (credentials
vs config) is the discriminator.

**Update-time `scope` change on a sealed secret is a DELETE + re-PUT
at the new scope.** No API surface is added; existing DELETE + PUT
handles it. The PUT path does NOT accept a body-level `scope` field —
scope is in the query string (`?scope=`), never in the JSON body. Same
posture as ADR-090 D2.

### D5. Audit payload widens with `scope`

The existing `secret.set` / `secret.deleted` / `secret.rotated` audit
kinds keep their taxonomy position (no new kinds — `scope` is a
property of the secret row, not a distinct operation). The audit
payload widens from `{app_id, name}` to `{app_id, name, scope}`:

```go
s.audit.Emit(r.Context(), "secret.set", &acct.ID, map[string]any{
    "app_id": app.ID,
    "name":   key,
    "scope":  scope, // ADR-092 — "default" for the implicit path
})
```

**Why widen payload, not add `secret.scope_set`:** every existing
`secret.set`/`secret.deleted`/`secret.rotated` consumer (dashboards,
SIEM, GDPR export) already deserializes the payload as `map[string]
any`; a new optional key is backwards-compatible. Old consumers
ignore the field; new consumers can filter by `data.scope !=
"default"` to surface named-scope activity. No SQL migration is needed
(`events.data` is JSONB).

**GDPR interaction:** the existing `listEventsForAccountExport` union
already reads `data` as `map[string]any`; the new `scope` key flows
through unchanged. No GDPR code change.

### D6. Quota: `SecretCountMax` keeps its per-app-across-all-scopes meaning

The quota `Limits.SecretCountMax` continues to bound **total secret
rows across all scopes** for an app. Free 8 / Hobby 25 / Pro 50 /
Scale 100 are the same numbers; they bound the sum of `default + prod
+ staging + …`. A per-scope cap (`SecretScopesMax`) is **not
introduced** in this ADR:

- ADR-090 D6 already deferred per-scope env cap (`EnvScopesMax`) for
  the same reason: the per-app quota bounds blast radius well, and a
  customer cannot wedge a runaway process by creating 10,000 staging
  scopes because the total row count is still capped.
- A Free-tier customer with 4 prod secrets + 4 staging secrets = 8
  total reaches the cap; the next write gets `ErrPlanLimitSecrets`. Same
  posture as env: total row count is the cap, not per-scope.

**The cross-scope posture is pinned at the wire surface** by
`secrets_scope_e2e_test.go::TestSecretsScopeSurfacePg` assertion 11:
on a Free plan, a 4th PUT (across any scope) returns 403
`plan_limit_secrets`. Without that assertion the quota posture could
regress silently in a future PR.

### D7. Error codes reuse `env_scope_*` — no `secret_scope_*` minted

ADR-092 does NOT introduce `secret_scope_invalid` /
`secret_scope_reserved`. The error codes are **reused**:

- `env_scope_invalid` — `?scope=` failed `ValidateScope` (empty, too
  long, out-of-shape slug, leading/trailing dash).
- `env_scope_reserved` — `?scope=__all__` on PUT/DELETE/POST.

The rationale is wire-uniformity: the secrets and env surfaces have
identical `?scope=` semantics. Minting `secret_scope_*` codes would
force every dashboard / SIEM consumer to special-case the same
rejection under two names. RFC 7807's `code` field is the stable
identifier dashboards key off; doubling the vocabulary adds
maintenance burden without customer benefit.

**Wire shape is uniform:** `EnvScopeInvalid` Problem response (in
`components/responses/`) is shared by both the env and secrets PUT /
DELETE / POST routes. Its `description` widens to "Applies to both
env and secrets surfaces (ADR-090 PR-B, ADR-092 PR-B)".

### D8. Out-of-scope deferrals (carried into a future ADR)

- **`SecretScopesMax` cap.** Parallel to `EnvScopesMax` (deferred by
  ADR-090 D6). The `SecretCountMax`-global posture is the load-bearing
  guard against runaway scope proliferation for secrets, same as for
  env.
- **`secret.X` overlay semantics.** Named-scope rows override
  `default`. Explicitly rejected (D4): customer chose strict per-
  scope, and the rationale (credentials vs config) is load-bearing.
- **Update-time `scope` change on a sealed secret.** Customers
  DELETE + re-PUT at the new scope. No API surface needed; existing
  DELETE + PUT handles it.
- **CLI `secrets list-all --scope <name>`.** The existing `list-all`
  is account-wide enumeration; the `--scope` flag is accepted but
  documented as ignored for cross-cutting ops. A future ADR can wire
  per-scope account-wide iteration if dashboards need it.
- **Dashboard "scope secrets" UI surface.** The `?scope=` parameter
  is exposed on the API but the customer-facing UI for "manage
  prod/staging secrets side-by-side" is the natural follow-up once
  the API lands. This ADR ships the surface; the dashboard is
  sequenced behind it.

## Files

### New

| Path | Purpose |
|---|---|
| `pkg/api/env_scope.go` (reused) | `ValidateScope()` helper + reserved-name handling. |
| `cmd/e2e/secrets_scope_e2e_test.go` | 11 wire-surface assertions: per-scope GET, sentinel rejection, invalid shape rejection, nested map shape, cross-scope quota. |
| `docs/adr/092-scoped-secrets.md` (this file) | The ADR itself. |
| `docs/adr/092-pr-cluster-outline.md` | PR-A/B split (PR-A shipped the data + wake plumbing in PR #849; this PR-B ships the customer-facing wire surface). |

### Modified

| File | Change |
|---|---|
| `pkg/api/secrets.go` | `AppSecretResponse` gains `Scope`; `AccountAppSecretResponse` gains `Scope`; new `ScopedAppSecretResponse` + `SecretByScope` + `AppSecretListResponse.SecretsByScope`. |
| `pkg/api/limits.go` | `SecretCountMax` doc comment clarifies per-app-across-all-scopes posture (mirror of ADR-090 D6). |
| `pkg/state/store.go` | `AccountAppSecret` struct gains `Scope` (PR-A already shipped the `…InScope` state methods). |
| `pkg/state/pgstore.go` | `ListAppSecretsForAccount` SQL widens with `s.scope`; Scan populates `r.Scope`. |
| `pkg/state/memstore.go` | Mirror of `AccountAppSecret.Scope`. |
| `cmd/apid/handlers_secrets.go` | `?scope=` parsing on all four routes; `writeSecretListAll` helper for the `__all__` arm; audit payload includes `scope`; default-scope path is byte-identical when `?scope=default` (or absent). |
| `cmd/apid/handlers_secrets_rotate.go` | `?scope=` parsing; `GetAppSecretInScope` + `UpsertAppSecretWithKidInScope` (PR-A seam); audit payload includes `scope`. |
| `cmd/gregale/commands3.go` | `secretsCmdScopeFlag = "scope"` constant; `--scope` flag on `secrets list/set/unset`; `scopeOrDefault()` helper for human-facing messages; nested `secrets_by_scope` rendering. |
| `cmd/gregale/commands_secrets_rotate.go` | `--scope` flag on `secrets rotate`; rotation hint reads "rotating %s in scope=%q". |
| `pkg/api/client.go` | `ListSecretsWithScope` / `SetSecretWithScope` / `UnsetSecretWithScope` / `RotateSecretWithScope` are the canonical typed surfaces; pre-PR-B variants stay as scope="" wrappers for backward-compat. `scopeQuery()` path-append helper. |
| `sdk/go/internal/api/client.go` | Same scope-aware siblings; per C6 the Go SDK is hand-extracted, so the siblings are hand-rolled. (The C6 regen replaces the Node + Python SDKs; the Go SDK was updated in C4.) |
| `sdk/node/src/generated/services/SecretsService.ts` | All four operations gain `scope?: string` arg + `?scope=` query string. (Regen output.) |
| `sdk/python/faas_sdk/api/secrets/*.py` | All four operation modules gain `scope: str \| Unset` arg. (Regen output.) |
| `api/openapi.yaml` | `?scope=` parameter on all four secrets routes; `AppSecretResponse` schema gains `scope` (required); new `ScopedAppSecretResponse` schema; `AppSecretListResponse` widens with `secrets_by_scope` (inline `additionalProperties`); `AccountAppSecretResponse` schema gains `scope`; `EnvScopeInvalid` Problem response description mentions "applies to both env and secrets surfaces". |
| `pkg/apid/openapi.yaml` | `make spec-sync` mirror. |

## Consequences

### Positive

- **Staging vs prod from one image, sealed at rest.** A customer can
  PUT `DATABASE_URL=postgres://prod-host/db` in `prod` and
  `DATABASE_URL=postgres://stg-host/db` in `staging`, redeploy without
  a rebuild, and vmmd's per-scope demux gives each scope its own
  credential at wake.
- **Redeploy preserves scoped secrets.** Same wire contract as ADR-089
  §11 — secret rows survive any number of redeploys because they're
  per-(app, scope), not per-deployment.
- **Strict per-scope credential hygiene.** No silent default overlay —
  a rotation gap in one scope cannot be masked by another scope's row
  (D4 rationale).
- **Audit signal is unambiguous.** A `secret.set` audit row with
  `data.scope=prod` is filterable in the customer audit log without a
  join; a row with `data.scope=default` is the legacy global view.
- **Backwards-compatible wire.** `?scope=` absent == `?scope=default`
  == the existing flat-list wire == the existing CLI / dashboard /
  third-party integrations. Every old client keeps working.
- **Error codes are uniform with env.** `env_scope_invalid` /
  `env_scope_reserved` are reused (D7) — same RFC 7807 envelope
  surface across the env and secrets surfaces.

### Negative

- **Wire shape widens.** `AppSecretListResponse` gains a new
  discriminated-union arm (`secrets_by_scope`); SDK consumers that
  decode into a strict struct without `omitempty` handling must
  upgrade. The Python + Node SDKs are auto-regenerated; the Go SDK
  was hand-rolled in C4 (hand-extracted posture, per the
  `pr-327-public-go-sdk-surface` memory).
- **CLI gains a `--scope` flag on every secret verb.** Operators who
  memorized the old `gregale secrets set` shape need to know the
  flag is optional (omitted == `default`). The flag's
  `--scope <name>|__all__` synopsis is rendered in `usage:` strings
  so `gregale secrets set --help` surfaces it.
- **Pre-existing CLI/SDK gaps are not closed by this ADR.** The
  inventory surfaced three that pre-date ADR-092:
    1. `cmd/gregale env pull|push` (`commands5.go:179-345`) currently
       reuses the sealed-secret HTTP API rather than the dedicated env
       endpoints. Same posture as pre-ADR-090.
    2. The `sdk/go/scopes.go` does not re-export `ScopeSecretsRead`
       / `ScopeSecretsWrite`. Already on main; out of scope here.
    3. The `gregale secrets list-all` (account-wide enumeration)
       accepts but ignores `--scope`. Out of scope per D8.

  These gaps are tracked as **out of scope (deferred)** below; closing
  them is the natural follow-up.

### Out of scope (deferred)

- **`SecretScopesMax` cap** — parallel to `EnvScopesMax` (D6).
  ADR-091 (Phase 3) is the natural follow-up if dashboards ever need
  it.
- **`secret.X` overlay semantics** — explicitly rejected (D4).
- **Update-time `scope` change on a sealed secret** — DELETE + re-PUT
  (no API surface needed).
- **CLI `secrets list-all --scope <name>`** — accepted but ignored
  for cross-cutting ops (D8).
- **Dashboard "scope secrets" UI surface** — exposed on the API but
  the dashboard is sequenced behind this PR.

### Compatibility

- `app_secrets` table widens (additive column + PK change). PR-A
  shipped the column + PK with `default` backfill via the column
  DEFAULT.
- `AppSecretResponse` widens with `Scope` — always populated (default
  scope echoed as `"default"`).
- `AppSecretListResponse` widens with `SecretsByScope` (omitempty).
- `PutAppSecretRequest` is unchanged — scope is in the query string,
  not the body. Body remains `{value}`.
- Audit payload widens: existing consumers see an extra `scope` key
  in the `data` map; consumers that don't read the field are
  unaffected.

## Rejected alternatives

- **Per-scope sealed secrets without the env PR-B cluster.** Rejected:
  we needed `?scope=` semantics, the `ValidateScope()` helper, and the
  discriminated-union wire shape from ADR-090. Sequencing the secrets
  surface before the env surface would have meant duplicating all
  three — the cleanest path was PR-A (env data) → PR-A (env wire) →
  PR-A (secrets data) → PR-B (secrets wire).
- **Make `scope` required and reject the no-scope path.** Rejected:
  breaks every existing dashboard / CLI / third-party integration
  that calls `PUT /v1/apps/{slug}/secrets/KEY` without `?scope=`. The
  implicit `default` mapping is the simpler migration story.
- **Layered-overlay semantics for secrets (same as env).** Rejected:
  credentials are not runtime config. A silent default overlay could
  mask a missing rotation in a non-default scope (D4 rationale).
- **`secret_scope_invalid` / `secret_scope_reserved` minted codes.**
  Rejected: doubles the RFC 7807 vocabulary without customer benefit.
  D7 reuses `env_scope_*`.
- **Per-scope host key derivation.** Rejected: per-scope key material
  in PG triples the secret-store footprint and creates a
  unseal-error class (rotating the host key means re-sealing every
  scope's envelopes, not one). The single host key per host is
  retained; `kid` is the per-row identity epoch (already in main
  from ADR-089 PR-B).
- **Always-nested wire on `/v1/apps/{slug}/secrets` even for the
  single-scope path.** Rejected: the existing flat `Secrets
  []AppSecretResponse` shape is the most SDK-friendly and the 95%
  case. `secrets_by_scope` is only populated when explicitly
  requested via `?scope=__all__`.

## Acceptance

- `make test` — unit tests pass: scope-shape validator, scope-aware
  quota count, `secrets_by_scope` map marshal, audit payload widening.
- `make spec-check` — `api/openapi.yaml` and `pkg/apid/openapi.yaml`
  in sync; `?scope=` parameter + `secrets_by_scope` field + new error
  responses all present; AST parity (`TestSpecCompliance/Schemas`)
  green.
- `cmd/e2e/secrets_scope_e2e_test.go::TestSecretsScopeSurfacePg` —
  full PG-backed: 11 wire-surface assertions covering per-scope GET,
  sentinel rejection, invalid shape rejection, nested map shape,
  cross-scope quota, plaintext-leak invariant per scope.
- `make sdk-gen` — Node + Python SDKs regenerate cleanly with the
  scope-aware sibling methods on all four secrets operations; no
  generator errors.
- `make sdk-check` — Go SDK has the WithScope siblings (hand-rolled
  in C4); helper-without-spec-route warnings are non-fatal.

## References

- `pkg/api/secrets.go` — the v1 DTOs this ADR extends with `Scope` +
  `ScopedAppSecretResponse` + `SecretByScope` + discriminated-union
  `SecretsByScope` field.
- `pkg/api/env.go` — the ADR-090 DTO mirror (the wire shape this ADR
  follows).
- `pkg/state/store.go` — the v1 state methods (PR-A widened the
  PK + added `…InScope` siblings).
- `cmd/apid/handlers_env.go::scopeFromQuery` — the v1 helper reused
  verbatim on the secrets handlers (the `?scope=` validation seam).
- `pkg/api/env_scope.go::ValidateScope` — the v1 shape validator
  reused for secrets (same wire semantics).
- `pkg/api/env_scope.go::EnvScopeAllSentinel` — the v1 reserved
  sentinel reused on the GET path.
- `cmd/apid/spec_compliance_test.go` — the AST parity test that
  rejected a named `SecretByScope:` schema entry (because the Go DTO
  is a type alias, not a struct); D3 inlines the additionalProperties
  shape instead, mirroring env's `EnvByScope`.
- `pkg/sched/engine.go::loadSealedEnvFor` — the v1 strict-per-scope
  resolution seam (PR-A); this ADR documents its contract (D4) so
  future readers don't mistake the no-overlay posture for a bug.
- `pkg/fcvm/manager.go::SealedEnvEntry.Scope` — the v1 vmmd per-scope
  mount demux (PR-A).
- `migrations/00217_app_secrets_scope.sql` — PR-A shipped this; this
  ADR documents its contract.
- `docs/adr/089-sealed-secrets.md` — the foundation ADR whose sealed-
  envelope shape this ADR widens with `Scope`.
- `docs/adr/090-named-envs.md` — the parallel env ADR; this ADR
  mirrors its `?scope=` semantics, error-code reuse (D7), audit
  payload widening (D5), and quota posture (D6).
- `docs/adr/091-deployments-scope.md` — the wake-time scope-selection
  ADR that closes the loop schedd pre-positioned for (out of scope
  for ADR-092 but a planned future dependency).
- `docs/adr/035-auth-audit-events.md` — the audit-kind taxonomy this
  ADR's payload widening follows.
- Roadmap: 2026-08-10 `secrets-envs-roadmap-decisions-2026-08-10.md` —
  this ADR is Phase 4 of that three-phase plan (Phase 1 = sealed
  secrets in main, Phase 2 = ADR-090 env, Phase 3 = ADR-091
  deployments, Phase 4 = this ADR).
