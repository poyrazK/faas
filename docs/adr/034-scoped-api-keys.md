# ADR-034 · Scoped API keys

- **Status:** accepted (rev2, 2026-07-25)
- **Date:** 2026-07-25
- **Evolution:** 034 (accepted 2026-07-25, coarse admin|read|write)
  → rev2 (2026-07-25, fine-grained vocabulary, IAM-1 closes issue #185).
  The ADR was revised in place because the prefix was already the
  canonical reference inside apid. (Subsequent renumbering: ADR-036
  is the per-instance metrics cardinality rollups ADR — PR #205,
  issue #170 / PR-A. ADR-037 is the reactive scale-up trigger ADR
  — issue #169 / #172.)

## Decision (rev2)

Every API key carries an explicit `scopes text[]` set. The apid auth
middleware checks the requested scope on every authenticated route.
The closed vocabulary is:

| Scope          | Allows                                                              |
|----------------|---------------------------------------------------------------------|
| `admin`        | Every action including billing, account deletion, key management.   |
| `apps:read`    | GETs across `/v1/apps`, `/v1/deployments`, `/v1/keys`, `/v1/audit-events`, `/v1/invocations`, `/v1/delayed-tasks/{id}`, `/v1/account`, `/v1/account/export`, `/v1/crons`, `/v1/domains`, `/v1/apps/{slug}/secrets` (list only). |
| `deploy:write` | POST/PATCH/DELETE on `/v1/apps`, `/v1/domains`, `/v1/crons`, `/v1/invocations/queues/*`, `/v1/delayed-tasks`, `/v1/account/restore`, `/v1/apps/{slug}/invoke`, `/v1/apps/{slug}/deployments`, `/v1/apps/{slug}/wake`, `/v1/apps/{slug}/park`, `/v1/apps/{slug}/rollback`, `/v1/apps/{slug}/rename`. |
| `delayed_tasks:read` | List delayed tasks for an app and get one delayed task without account-wide app read access. |
| `delayed_tasks:write` | Create and cancel delayed tasks without general deployment authority; also admits delayed-task reads. |
| `secrets:read` | Reserved for IAM-5 (per-secret GET). Today every secret read is admin-only. |
| `secrets:write`| PUT/DELETE on `/v1/apps/{slug}/secrets/{key}`.                      |
| `usage:read`   | GET `/v1/usage`, `/v1/usage/summary`.                               |

`admin` implicitly satisfies every other scope check — the
`principalHasScope` helper grants any-of. Session-cookie auth (Key ==
nil) is implicitly admin: humans at the dashboard always have full
access.

Per-route scope sets live in `pkg/api/apikey.go` as named constants
(`ScopesAdminOnly`, `ScopesReadSurface`, `ScopesDeployWriteSurface`,
`ScopesSecretsWriteSurface`, `ScopesUsageReadSurface`) used by the
`requireScope` middleware. This collapses 38 `requireScope` call
sites into 4 named shapes.

Migration `00044` (slot 00043 was taken by the go124-alpine runtime
migration that landed after the rev1 plan was approved) atomically:

1. Backfills every legacy row. `admin` stays `admin`; `read` expands
   to `{apps:read, usage:read, secrets:read}`; `write` expands to
   `{deploy:write, secrets:write}`; a row carrying both `read` and
   `write` collapses to the union via `array_agg(distinct ...)`.
2. Emits one `key.scopes_changed` audit row per affected key
   (actor=`migration`, not `apid`, so the cut-over is visible in the
   audit log timeline).
3. Adds the DB CHECK constraint `api_keys_scopes_vocab_chk` that
   closes the enum to the new six values (`<@` is "subset-of",
   `cardinality > 0` rejects a row that lost its scopes).

Down drops the CHECK constraint only. The backfill is not reversed
because the row no longer carries the source-of-truth
read/write/admin markers.

## Why (rev2)

The rev1 ADR shipped a coarse vocabulary (`admin | read | write`) on
day one. Production data showed two real problems that the coarse
language couldn't address:

- **`read` was the broadest possible read.** A customer with a CI
  key on `read` could `GET /v1/apps/{slug}/secrets` and see every
  secret VALUE (the response carries names + last-rotated-at +
  per-secret policy) and `GET /v1/account/export` to download the
  full GDPR bundle.
- **`write` couldn't be combined with anything.** A legitimate CI
  "deploy + verify" workflow needed `read` for the post-deploy logs
  GET and `write` for the deploy PUT — but the coarse vocabulary had
  no way to express "deploy AND read". The customer resorted to
  minting a full admin key for CI.

rev2 closes both gaps. Issue #185 captures the customer-side ask.

## Consequences

- `api_keys` retains the `scopes text[] NOT NULL DEFAULT '{admin}'`
  column (migration 00036 in rev1) and gains the CHECK constraint
  in migration 00044. The default preserves the legacy full-access
  contract for any INSERT that omits the column.
- `state.APIKey` carries `Scopes []string`. `Store.CreateAPIKey`
  takes an explicit `scopes` argument. `Store.AuthenticateKey` is
  the canonical account+key lookup, returning both in one call.
- `apid` middleware composes as `auth → requireScope → handler`.
  The principal is stashed in `r.Context()` so the route bodies are
  untouched. The route table in `cmd/apid/server.go` picks the
  named scope shape (`ScopesReadSurface`, `ScopesDeployWriteSurface`,
  etc.) per route — the per-method `MethodDefaultScope` helper that
  rev1 introduced is gone (every route now names its shape
  explicitly so a future hardening pass can grep for "this route
  can be admin-gated" without an indirection).
- `TouchKeyLastUsed` is wired on every successful bearer auth via a
  detached-context goroutine. Observability, not auth — a slow PG
  never blocks the user's request.
- `POST /v1/keys` validates requested scopes via
  `api.NormalizeCreateKeyScopes` (the single funnel for all mint
  paths: POST /v1/keys and the CLI device-code exchange). Unknown
  scope → `400 code:validation`. Omitting `scopes` defaults to
  `["admin"]` so existing SDK/CLI callers keep full access.
- `DELETE /v1/keys/{id}` uses the new `Store.DeleteAPIKeyReturning`
  (the IAM-4 audit-friendly shape) so the `key.deleted` event
  carries the dismissed scopes. The post-IAM-4 row answer to "what
  did this key allow before it died?" is `GET
  /v1/audit-events?kind_prefix=key.deleted`.
- Migration 00044 emits one `key.scopes_changed` audit row per
  affected key with payload `{key_id, from: [old], to: [new]}`.
  Operators see the cut-over via `GET /v1/audit-events?kind_prefix=key.`.
- The existing `adminAllows` email-allowlist gate on `/v1/compute-nodes`
  is preserved. Scope is layered on top: a non-admin key never reaches
  the admin email check.
- Session-cookie auth (dashboard) is implicitly admin. A human at
  the keyboard gets full access; an API key holder is the only
  principal that can be scoped down.
- OpenAPI spec, SDK `CreateKey`, and DTOs gain an enumerated `scopes
  []string` field with six valid values. `make spec-check` enforces
  parity (rev2 closes the vacuum `oas3-valid-schema-example` warnings
  rev1 left in place by adding `example:` to APIKeyResponse and
  APIKeyExportResponse).
- The dashboard renders the new Scopes column on
  `/dashboard/account`, surface as a comma-separated list of `code`-
  styled chips. Empty/nil renders as `—` so legacy accounts that
  pre-date the migration's CHECK constraint don't see a blank column.
- The 401 → 403 path is now distinguishable: `code: unauthorized`
  means "log in", `code: insufficient_scope` means "your key does
  not have permission for this endpoint".

## Rejected alternatives

1. **Keep the coarse `read | write` vocabulary.** Rejected at rev2:
   `read` is the broadest possible read; `write` doesn't compose
   with any read; the only way to model "deploy + verify" was to
   hand out a full admin key, which defeats the whole point of the
   scope primitive.
2. **Fine-grained scopes from day one.** Rejected at rev1 (the
   six-scope × forty-route matrix was too much to nail down without
   production feedback). Adopted at rev2 after production data
   pinned the two real gaps.
3. **Per-resource scopes** ("this key can only deploy `app-foo`").
   Rejected: requires a resource-binding primitive (RBAC, ABAC, or
   signed JWTs) that the platform does not yet have. Defer to a
   later IAM phase (IAM-5 / IAM-6).
4. **RBAC roles** (`admin`, `developer`, `viewer`). Rejected: keys
   are minted by an account owner for their own use; the
   multi-user-account model is not on the v1 roadmap. Scopes on a
   key are simpler and cover the same threat model.
5. **Multiple distinct API-key types** (`admin_key`, `deploy_key`).
   Rejected: a `type` column is a single-value categorical where
   scopes are a flexible set. A `type` field would need to be
   extended for every new permission split; scopes compose.
6. **Bootstrap every key as admin and let customers opt down** vs.
   the inverse (no scopes → 403). The "default admin" semantics
   preserve the pre-1.0 contract; customers who want least-privilege
   opt in explicitly. A deny-by-default migration would break
   production traffic for customers whose SDK passes `{"label":
   "ci-deploy"}` without scopes.
7. **Per-key IP allowlists, key expiry, automated rotation**.
   Separate IAM-3 phase. Permitting them now would inflate the
   surface; the scope primitive is the load-bearing one.
8. **Using the existing `Label` column instead of a new column**.
   Rejected: `Label` is a free-form description rendered in the
   dashboard ("ci-deploy", "laptop"). Coupling authorization to a
   string the customer types at mint time is a typo-driven
   privilege escalation waiting to happen.

- **Provenance + rotation lineage columns (`created_ip`,
  `created_ua`, `parent_key_id`)** — deployed in
  `migrations/00144_api_keys_provenance.sql` (IAM hardening
  mega-PR, logical change 2). All three columns are nullable +
  advisory (no NOT NULL, no CHECK). The provenance columns let
  a SOC 2 auditor answer "who minted this key from which IP+UA"
  without joining through Loki; `parent_key_id` is the explicit
  FK to the predecessor key (distinct from the rotation-internal
  `rotated_from_id` column. For rotations both point at the same
  predecessor). The `key.created` / `api_key.created` /
  `key.rotated` / `api_key.rotated` audit rows now carry the same
  `created_ip` / `created_ua` / `parent_key_id` payload so the
  audit row and the DB row stay in lockstep. The audit payload
  values are run through `pkg/logsanitize.Field` so the audit
  trail is storage-rotation-safe (matching the precedent set by
  `pkg/authcode.HashEmail`, ADR-035 §"Failed-login emission").
