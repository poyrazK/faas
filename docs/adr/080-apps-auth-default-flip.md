# ADR-080 · Flip `apps.require_authn` global default to true (grand-father existing customers)

- **Status:** accepted
- **Date:** 2026-08-06
- **Issue:** #695
- **Supersedes:** none
- **Closes:** spec §17 G15 (open gap)

## Context

Issue #560 shipped `apps.require_authn bool NOT NULL DEFAULT false` as a per-app opt-in (CLOSED 2026-08-05). Issue #477 / ADR-079 later shipped `apps.public_auth_mode text NOT NULL DEFAULT 'open'` with the closed enum `{open, bearer, basic}`. Together, every `{slug}.gregale.dev` route is **public-by-default** today — a customer has to PATCH both fields to gate ingress.

Cloud Run's analogue is the inverse: services are IAM-authenticated by default; `--allow-unauthenticated` opens them publicly. Issue #695 closes **spec §17 G15** ("the global default is unchanged to avoid breaking existing customers") by flipping the global default to authenticated. A one-time grandfather migration preserves every existing customer's behaviour in place — no anonymous request breaks for any customer.

### Why now

- **Security posture:** every `{slug}.gregale.dev` is publicly reachable. A customer who deploys an internal API, B2B service, or anything with PII has to remember to PATCH the flag — and most won't, until something bad happens.
- **Sales-conversion friction:** "is this secure by default?" is on every enterprise evaluation. "Yes, opt-out" closes deals; "opt-in" doesn't.
- **G15 is explicit** in spec §17. The rationale for deferring in #560 was correct at the time; the follow-up is the global flip.

## Decision

### 1. Per-plan defaults table

`Limits` gains two new fields, populated per plan:

| Plan  | `RequireAuthnDefault` | `PublicAuthModeDefault` | Rationale |
| ----- | -------------------- | ----------------------- | --------- |
| Free  | `false`               | `"open"`                | Free doesn't unlock the token gate today (no `RequireAuthn`); leave public-by-default. |
| Hobby | `true`                | `"open"`                | Hobby unlocks the gate but not `bearer` (`PublicAuthBearerAllowed = false`). Defaulting to bearer without a usable scope would strand the customer. |
| Pro   | `true`                | `"bearer"`              | Both gates allowed; secure-by-default. |
| Scale | `true`                | `"bearer"`              | Same as Pro. |

New accessors (`pkg/api/limits.go`, sibling of `Plan.RequireAuthnAllowed`):

- `func (p Plan) RequireAuthnDefault() bool`
- `func (p Plan) PublicAuthModeDefault() string`

Both fail-closed on unknown plans (mirroring the existing `RequireAuthnAllowed` shape — return the zero value, same as `Allowed` does).

**Why per-plan and not a single global literal:** Hobby unlocks `require_authn` but not `bearer`. A single `(true, "bearer")` literal would leave Hobby stranded — the customer creates an app, gets a 401 from the gateway, has no path to PATCH it. Per-plan defaults make the asymmetry explicit and auditable; every plan's defaults are a literal in `pkg/api/limits.go`.

### 2. Single companion column for the grandfather marker

`apps.auth_default_flipped_at timestamptz NULL`. The migration backfills every existing row (`UPDATE apps SET auth_default_flipped_at = now() WHERE auth_default_flipped_at IS NULL`) and is replay-safe.

Chosen over a per-row audit emission because:

- A Scale customer with hundreds of pre-flip apps would generate hundreds of audit rows for a single transition event.
- The transition is platform-wide, not per-app — the audit log needs ONE record of the global cut-over.
- The companion column gives per-app granularity (the dashboard banner query + `faas apps list` annotation can read it) without the audit row-storm.

### 3. One batch audit row for the global cut-over

`apps.auth_default_global_flipped` audit row emitted by the migration itself, payload:

```json
{
  "migrated_count": 4321,
  "from_require_authn_default": false,
  "to_require_authn_default": true,
  "from_public_auth_mode_default": "open",
  "to_public_auth_mode_default": "bearer",
  "plan_overrides": {
    "free": {"require_authn": false, "public_auth_mode": "open"},
    "hobby": {"require_authn": true, "public_auth_mode": "open"},
    "pro": {"require_authn": true, "public_auth_mode": "bearer"},
    "scale": {"require_authn": true, "public_auth_mode": "bearer"}
  }
}
```

Per-app PATCH-true/PATCH-false flips continue to emit the existing `app.authn_required` / `app.authn_disabled` / `app.public_auth_changed` rows — no change.

### 4. Zero dual-write window

Unlike ADR-061 (orgs/memberships), there is no Nullable-phase and no Prometheus cut-over counter. The reason:

- The columns (`require_authn`, `public_auth_mode`) are NOT changing shape — they exist already with their old defaults.
- The only thing changing is which Go code path stamps the default at create-time.
- A pre-flip row reads identically under both old and new Go code (the Go zero-value fallback is the schema default, which the migration explicitly backfills).
- The grand-father mechanism is "set the column on every existing row at migration time" — no runtime ambiguity.

A separate `pg_dump` / restore test in the migration's `_test.go` confirms no row's effective state changes after the migration: every pre-flip row had `require_authn=false` before AND continues to read `require_authn=false` after.

### 5. Plan-tier gating stays as today

The `RequireAuthn`, `PublicAuthBearerAllowed`, `PublicAuthBasicAllowed` gates in `pkg/api/limits.go` are NOT touched. A customer on a plan without `RequireAuthnAllowed` who has an app defaulted to `true` can immediately PATCH-false (the gate is on PATCH-true, not on the default). This is intentional: opt-out is universal, opt-in is plan-gated.

The Hobby tier gets `RequireAuthnDefault = true` but `RequireAuthnAllowed = false`. A Hobby customer cannot *escalate* back to true once they PATCH-false (the gate blocks PATCH-true). This is the load-bearing reason Hobby unlocks the gate as a default but not as an opt-in — it's the right tradeoff for a Hobby-tier customer (no anonymous token = expected; "I want to add a token gate" = upgrade).

### 6. Wire surface unchanged

`POST /v1/apps` already accepts `require_authn *bool` (issue #560) and `PATCH /v1/apps/{slug}` already accepts `require_authn` + `public_auth` (issues #560, #477 / ADR-079). Customers opt out with:

- CLI: `gregale app <slug> --no-require-authn --public-auth=open` (existing flags).
- API: `PATCH /v1/apps/{slug}` with `{"require_authn": false, "public_auth": {"mode": "open"}}`.
- Dashboard: per-app "Make public" button (out of scope here; follow-up).

The only NEW wire field is the read-side `apps.auth_default_flipped_at *time.Time` on `AppResponse` — purely informational, no PATCH-side surface, no mutation path. Customers don't need to think about it; it powers the dashboard banner and the CLI annotation.

### 7. Dashboard banner — new `ActionRequiredSurface`

`Page.ActionRequiredSurface string` (sibling of the existing `FlashSurface` at `pkg/dashboard/dashboard.go:565`). Rendered on `account.html` beneath the `FlashSurface` block, conditionally on `ActionRequiredSurface != ""`. Per-account query:

```sql
SELECT count(*) FROM apps
WHERE account_id = $1
  AND auth_default_flipped_at IS NOT NULL
  AND created_at < $2  -- the migration's stamp time
```

Populates the banner with: *"On YYYY-MM-DD the default for newly-created apps changed to require authentication. Your existing N apps were not affected. New apps now require `Authorization: Bearer <token>` by default; run `gregale app <slug> --no-require-authn --public-auth=open` to opt out."*

The banner is **one-time per affected customer** — once every pre-flip app has been either explicitly kept-true or PATCHed-false by the customer, the count drops to zero and the banner stops rendering. No dismissal cookie / no dismiss button: count-zero is the natural off-switch.

### 8. CLI annotation — `faas apps list` adds an AUTH column

```
SLUG                      STATUS      URL                                            AUTH
hello                     running     https://hello.gregale.dev                  AUTH: required · since 2026-08-06
internal-api              running     https://internal-api.gregale.dev           AUTH: required + basic · since 2026-08-06
public-blog               running     https://public-blog.gregale.dev            AUTH: open
new-deploy                running     https://new-deploy.gregale.dev             AUTH: bearer
```

The `since YYYY-MM-DD` suffix renders only when `auth_default_flipped_at != nil` (pre-flip apps that have been grandfathered).

### 9. Rollback path

The migration's `Down` section drops `auth_default_flipped_at`. The Go-side defaults are reverted to `false` / `"open"`. The `apps.auth_default_global_flipped` audit row remains in `events` (audit log is append-only per spec §11; rollback does not erase history).

There is no dual-write phase to roll back; no partial-state window to close. The rollback is one migration + one revert PR.

## Consequences

- **Free plan remains public-by-default.** A future ADR can revisit Free if security asks for it (see Open items below).
- **Hobby gets the token gate as a default but not as an opt-in** (because `RequireAuthnAllowed = false` today). If Hobby ever unlocks `RequireAuthnAllowed`, the default literal moves to `"bearer"` in the same PR.
- **Per-plan defaults are encoded as struct fields, not accessors that derive from the existing Allow booleans.** This means a future plan-tier restructuring (e.g. "Hobby Basic" with bearer) is a one-line edit in the limits table, not a logic change.
- **The dashboard banner uses the same yellow flash class as `FlashSurface`** for visual consistency. A future contributor adding a second surface must use the same class.
- **The companion column `auth_default_flipped_at` is nullable and read-only.** No PATCH path writes it; only the migration backfill sets it. A future contributor adding a PATCH path must refuse the field with `422 unprocessable_entity`.
- **The CLI annotation is a 5th column on `faas apps list`** — the table widens from 3 to 4 columns. The `gregale apps list` test at `commands.go:253-274` must update its column-width format string.
- **`enforceRequireAuthn` runs before `enforcePublicAuth` on the gateway hot path** (`pkg/gateway/handler.go:1354, 1370`). A Hobby app with `require_authn=true, public_auth_mode=open` will 401 on the require-authn gate before the public-auth gate ever runs — which is the intended behavior (require-authn is the broader gate, public-auth is the layered mode).
- **E2E fixtures that hit a fresh Hobby app without a token** (`cmd/e2e/deploy_wake_metal_test.go`, `cmd/e2e/streaming_metal_test.go`) need updating to either `--no-require-authn` at deploy time OR to send a Bearer token. This is the only test surface that breaks; it must be coordinated with the e2e author in the PR thread.
- **The migration's idempotency relies on `WHERE auth_default_flipped_at IS NULL`.** A future contributor adding an `ON CONFLICT` clause must NOT bypass this predicate — a re-applied migration that re-stamps the column would change the dashboard banner's "since" date and the CLI annotation's suffix.

## Verification

### Migration tests
- `make migration-test` — apply `00156` against an empty pg instance and verify the schema + replay safety + audit row emission.
- `make migration-test REPLAY=1` — re-apply; verify `auth_default_flipped_at` is NOT re-stamped (idempotent — predicate is `WHERE auth_default_flipped_at IS NULL`).
- Down-migration probe — `00156` down drops the column cleanly. Forward-only contraction per ADR-041 / migration `00099` precedent.

### Unit tests (CI pins — most important to get right)
- **`migrations/00143_apps_require_authn_test.go`** — historical assertion (`defaultVal == false`) stays. The post-flip default pin moves to `00156`.
- **`migrations/00153_apps_public_auth_test.go`** — historical assertion (`defaultMode == "open"`) stays. The post-flip default pin moves to `00156`.
- **`migrations/00156_apps_auth_default_flip_test.go`** — pins the post-flip default for both columns on a fresh DB.
- **`pkg/state/pgstore_test.go`** — `TestPg_CreateAppIfUnderQuota_WritesRequireAuthnDefault` + `TestPg_CreateAppIfUnderQuota_WritesPublicAuthModeDefault` mirror the existing `TestPg_CreateAppIfUnderQuota_WritesStreamingEnabled`.
- **`tests/state/create_app_default_test.go`** — issue #695 AC #5 tripwire. Fails the merge if the default reverts to `false` / `"open"`.
- **`cmd/apid/handlers_ext_test.go::TestGetApp_SurfacesRequireAuthn`** — updated per-plan expectations (Hobby/Pro/Scale → `true`, Free → `false`).
- **`cmd/apid/handlers_public_auth_test.go`** — `mustSeedApp` stamps the new default; the expected `PublicAuthMode` matches the per-plan truth table.

### End-to-end
- `cmd/e2e/public_auth_e2e_test.go` — extend with the grandfather probe (pre-flip row has `auth_default_flipped_at != nil` after the migration test applies).
- `cmd/e2e/apps_list_default_test.go` (NEW) — CLI annotation probe (table-driven across the four plans).
- `cmd/e2e/deploy_wake_metal_test.go` + `cmd/e2e/streaming_metal_test.go` — fixtures updated to either `--no-require-authn` at deploy time or a Bearer token.

### Operator workflow
1. `psql -c "select count(*), auth_default_flipped_at from apps group by 2"` — every row has `auth_default_flipped_at != null` after the migration runs.
2. `select * from events where kind = 'apps.auth_default_global_flipped'` — exactly one row, with `data->>'migrated_count'` matching the pre-migration app count.
3. Log into the dashboard on an account with at least one pre-flip app — the yellow banner renders.
4. `gregale app list` — every row shows the AUTH column with the `since YYYY-MM-DD` suffix on grandfathered apps.
5. `gregale app <slug> --no-require-authn --public-auth=open` on a Pro/Scale app — see `AUTH: open` on the next `gregale app list`.

## Open / deferred items

1. **Free-plan treatment.** Free stays `require_authn=false` default. The plan can be re-evaluated in a follow-up if security asks for it.
2. **Hobby public_auth_mode default of `"open"`.** Hobby unlocks the token gate but not `bearer`. A future PR could open Hobby's `PublicAuthBearerAllowed` so the default can become `"bearer"`, but that's a pricing/plan-tables decision that belongs in a separate ADR.
3. **Per-row audit emission.** Deferred. Batch row only. Re-evaluate if security asks for per-app audit trail.
4. **E2E test surface.** `cmd/e2e/deploy_wake_metal_test.go` and `cmd/e2e/streaming_metal_test.go` need token-gated or opt-out Hobby fixtures. Coordinate with the e2e test author in the PR thread.
5. **Dashboard "Make public" button.** Out of scope here. CLI + API opt-out paths are sufficient for v1; the dashboard button is a UX follow-up.

## Notes for the implementer

- The migration slot is **00156**. Slot 155 was held by PR #697 (`feat/issue-554-liveness-followup`, owned `00155_deployments_parked_reason.sql`); this branch renumbered from 00155 to 00156 to avoid a goose "duplicate version 155" collision when both PRs land, and added a no-op `00155_reserve_slot.sql` fence (per ADR-041 / `migrations/00100_reserve_slot.sql` precedent). The fence must be `git rm`-ed once PR #697 merges.
- The ADR number **080** is free (the three `079-*.md` files share the same number — see ADR-079 / ADR-079-per-app-public-auth / ADR-079-liveness-probe-restart-wedged-vm for the precedent of overlapping numbers; the repo's file naming is forgiving on this point).
- The `Limits.RequireAuthnDefault` field on Free is the only field with `false`. A test that iterates plans asserting "at least one default is true" is the right shape; iterating and asserting "all defaults are true" would over-constrain.
