# ADR-120 — App API keys / consumer identity (issue #975 item #5)

Status:      accepted (PR #5-A merges the data model + read path; PR #5-B the apid CRUD + audit log; PR #5-C the gatewayd-internal middleware + consumer_id propagation)
Date:        2026-08-20
Supersedes:  none
Related:     ADR-090 (api_keys), ADR-101 (oidc_exchanged_tokens),
            ADR-104 (per-consumer throttle keying), ADR-079 (per-app public_auth),
            ADR-092 (api_keys org_bound), ADR-085 (SDK regen via OpenAPI),
            ADR-117 (deploy stage progress — companion cluster)

## Context

Issue #975's 12-item capability audit identifies a missing identity
surface between Gregale's two existing credentials:

- `api_keys` — account-scoped, long-lived, minted by apid for the
  account operator (CLI / dashboard / SDK). Spec §4.2, ADR-090.
- `oidc_exchanged_tokens` — short-lived bearer exchanged from an
  external IdP, account-scoped. ADR-101.

Neither fits "the *application's* customer's key". The application
operator needs to mint a credential that an end-customer of the
deployed function presents to call the function, and that credential
must be:

1. **scoped to a single (account_id, app_id) pair** — a leaked key
   only affects one app;
2. **revocable independent of the app's deployment** — pulling the
   app's deployment doesn't kill the customer's session, and
   killing a single customer doesn't affect anyone else;
3. **tagged with a stable `consumer_id`** — downstream telemetry
   (#6 consumer analytics, #7 route metrics cold-start, #8 queryable
   request logs) needs a label that survives key rotation;
4. **optional by default** — apps that don't need end-customer
   auth must stay on the public path — with a per-app opt-in to
   `required`.

Items #6 (consumer analytics), #7 (route metrics cold-start
reserved label), and #8 (queryable request logs) all plug into
this identity. The audit explicitly frames it as the **Identity
gate** for the rollout: shipping the rest of the identity-aware
telemetry without this primitive forces a stopgap label that
every customer later has to migrate off.

## Decision

Five sub-decisions land together because each constrains the
others.

### D1. Table shape: a NEW table `consumer_keys` distinct from `api_keys`

A new table; we deliberately do NOT extend `api_keys`. The
`api_keys` row carries account-scoped operator metadata (status,
scopes like `deploy:write`, rotation grace window) that doesn't
fit a customer-of-an-app flow.

Columns:

```
id              uuid PRIMARY KEY DEFAULT gen_random_uuid()
account_id      uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE
app_id          uuid NOT NULL REFERENCES apps(id)     ON DELETE CASCADE
name            text NOT NULL
prefix          text NOT NULL
hashed_secret   bytea NOT NULL
scopes          text[] NOT NULL DEFAULT '{}'
expires_at      timestamptz NULL
last_used_at    timestamptz NULL
revoked_at      timestamptz NULL
created_at      timestamptz NOT NULL DEFAULT now()
updated_at      timestamptz NOT NULL DEFAULT now()
```

Indexes:

- `UNIQUE (account_id, app_id, name)` — the user-visible identity
  for revocation / list.
- `(app_id, prefix)` — gateway-side lookup for every inbound
  request; composite narrowing to ~one row before the hash compare.
- `(account_id)` — list endpoints.

`ON DELETE CASCADE` from both `accounts` and `apps` closes the
GDPR hard-delete path. When an account is hard-deleted, every
`consumer_keys` row goes with it; when an app is hard-deleted,
every key scoped to that app goes with it. Mirrored in PR #5-A's
`memstore_gdpr_test.go` and the new `gdpr_cascade_test.go`.

### D2. Wire format `ck_<prefix>_<secret>`

Two-segment plaintext:

- `ck_` — literal prefix; greppable for incident-response scan
  tooling without leaking the secret.
- `<prefix>` — 8 hex characters (16 bits of randomness per key,
  ~65k possible prefixes; sufficient to namespace keys inside one
  app). Stored as the `prefix` column.
- `<secret>` — 64 hex characters (32 bytes of `crypto/rand`, ~256
  bits of entropy). Twice `api_keys`' 24 bytes because
  consumer_keys are exposed to the public internet — every
  customer of the app sees one — while `api_keys` only ever
  circulate inside the account operator's CI fleet.

The prefix makes a leaked-key scan pattern-discoverable
(`ck_*`) without exposing the secret. The `(app_id, prefix)`
composite index then narrows the hash compare to ~one row.

### D3. Hash policy: SHA-256 of the FULL plaintext

Same scheme as `api_keys` (see `pkg/api/apikey.go::HashAPIKey`).
We deliberately do NOT move to argon2id for consumer_keys v1:

- The lookup key is `(app_id, prefix, hashed_secret)`.
- `prefix` narrows to ~2^16 candidates per app.
- `hashed_secret` narrows to ~1 candidate per (app_id, prefix).
- A brute force requires ~2^16 hash evaluations per app to
  enumerate its prefixes × 2^256 to invert one SHA-256 → 2^272
  work per attempt. SHA-256 is the correct cost asymmetry.

argon2id is reserved for v2 if cardinality grows past the
Scale-plan cap (1000 keys per app × ~50 routes × ~10 throttle
rules = 500k worst-case buckets; ADR-104's `__other__` collapse
keeps the effective map bounded at `ThrottleMaxKeysPerRule`).

### D4. Scope vocab: closed set, validated at the apid write boundary

The v1 set:

```
read    — GET-only (any route the app exposes)
write   — all methods
admin   — DELETE/rotate keys + manage (rare; super-user pattern)
```

Closed-set regex `^[a-z_]+$`. The apid write path is the
canonical gate (handlers validate → CHECK in the migration is
defense-in-depth). read-vs-write semantics are enforced by
`gatewayd-internal`'s middleware against the resolved HTTP
method; the edge-rules engine continues to own the route-level
filter (no double-bookkeeping between scope and edge-rule).

### D5. Opt-in posture: per-app, default `optional`

A new column `apps.consumer_auth_mode TEXT NOT NULL DEFAULT 'optional'
CHECK (consumer_auth_mode IN ('optional','required'))`. PR #5-B
adds it via `ALTER TABLE apps ADD COLUMN IF NOT EXISTS`. PATCH
`/v1/apps/{slug}` accepts the value.

The middleware semantics (precise matrix lives in `auth_consumer.go`):

| `consumer_auth_mode` | Authorization header | Behaviour |
|---|---|---|
| `optional` | absent | pass-through (no consumer_id stamped) |
| `optional` | present + valid | stamp consumer_id, proceed |
| `optional` | present + invalid format | 401 `consumer_key_invalid` (we never silently ignore — a stale revoked key would otherwise linger) |
| `optional` | present + revoked / expired | 401 `consumer_key_inactive` |
| `optional` | present + scope mismatch | 403 `consumer_scope_missing` |
| `required` | absent | 401 `consumer_key_required` |
| `required` | present + valid | stamp consumer_id, proceed |
| `required` | present + invalid format | 401 `consumer_key_invalid` |
| `required` | present + revoked / expired | 401 `consumer_key_inactive` |
| `required` | present + scope mismatch | 403 `consumer_scope_missing` |

IDOR at the gateway: a header presented for a key in a *different*
account's app → 401 `consumer_key_invalid` (NOT 403 — leaking the
existence of the foreign key would be a side-channel).

## Consequences

+ A new identity primitive that the audit's #6/#7/#8 can pin to:
  `consumer_id` is stamped into `r.Context()` and exposed via
  `pkg/auth/middleware/context.go::ConsumerFromContext(r)`. This
  is the load-bearing getter the downstream clusters read.
+ Revocation is O(1): the predicate is `revoked_at IS NULL`; no
  cascade.
+ GDPR hard-delete is CASCADE through `accounts → apps →
  consumer_keys` (verified in PR #5-A's tests; mirrored in
  `memstore_gdpr_test.go`).
+ `apps.consumer_auth_mode='optional'` is a no-op for the 99% of
  apps that don't need end-customer auth — no behaviour change
  for the existing public surface.
+ The plaintext is on the wire response exactly **once** (the
  POST response), never on a GET. The DB stores the SHA-256; we
  never log the plaintext; the response omits it on every
  subsequent fetch.

- Per-app cardinality: a Scale plan's 1000 keys × N routes × M
  rules × ADR-104's `__other__` collapse is a real budget.
  Documented in PR #5-C's tests; cap enforced by
  `Limits.ConsumerKeysPerApp` at apid write time (422
  `consumer_key_quota_exceeded`), not at the gateway (write-time
  = customer-visible error, not silent collapse).
- `last_used_at` touch is fire-and-forget with a 60s per-key
  debounce to avoid write amplification. Means `last_used_at` is
  **best-effort observability**, not a billing signal
  (caller-facing billing uses meterd's separate counter — explicit
  non-goal).

## Alternatives considered (and rejected)

1. **Per-account-only key (no `app_id`)** — rejected: a leaked
   key would affect every app the account owns. Issue #975
   explicitly cites per-app scoping as the reason for a new
   identity.
2. **Reuse `api_keys`** — rejected: `api_keys` is
   account-scoped and admin-level (the operator's credential).
   Mixing end-customer traffic into the same table forces every
   consumer key to carry the same scope surface as a CI rotation
   key (over-permissive) and breaks ADR-090's rotation semantics
   (rotation = revoke-grace-mint doesn't fit a customer-of-an-app
   flow).
3. **JWT-only** — rejected: simpler (no DB lookup per request)
   but no revocation primitive short of a JWT-key-rotation cycle
   (the issuer would have to rotate the signing key to kill one
   customer). The audit calls out revocation as a hard requirement.
4. **OIDC-only** — rejected: forces every app customer to
   federate against an IdP, which is overkill for a hobby app that
   wants 10 keys for its first 10 paying users. OIDC lives in
   ADR-101 for the operator-side surface; `consumer_keys` is the
   symmetric customer-side primitive.

## Downstream unblock (the audit's #6/#7/#8)

- **Item #6 (consumer analytics):** `consumer_id` is a stable
  label on the request log table, plottable as a top-N.
- **Item #7 (route metrics cold-start):** `consumer_id` is a free
  reserved label in the route-metrics series; ADR-093's per-route
  50-label cap is independent (`consumer_id` has its own cap via
  `ConsumerKeysPerApp`).
- **Item #8 (queryable request logs):** `consumer_id` is a column
  on the request log projection; the replay engine's
  header-denylist references it.

All three land as separate cluster clusters once PR #5-C is on
main and `pkg/auth/middleware/context.go::ConsumerFromContext(r)`
is the canonical getter.

## Migration plan

Three sequential PRs (the cors_presets PR-A → PR-B cadence
extended with a PR-C for the heavier surface):

- **PR #5-A** — data model + Store interface. Slot **00305** on
  main. ~700 LOC; ships the table, the `ConsumerKey` struct, six
  Store methods (Create / GetByID / ListForApp / Revoke /
  TouchLastUsed / LookupByAppAndPrefix), pgstore + memstore
  impls, replay-safe migration test, GDPR mirror test, ADR-120
  doc.
- **PR #5-B** — apid CRUD + audit log + OpenAPI + SDK regen +
  `apps.consumer_auth_mode` column. Slot **00305**. ~1100 LOC;
  ships 4 handlers, 4 new RFC 7807 problem codes, OpenAPI
  resource, regenerated Go/Node/Python SDKs, audit-log row on
  every create/revoke.
- **PR #5-C** — `gatewayd-internal` middleware + `ConsumerFromContext`
  accessor. **No migration slot** (pure Go; data model + read
  path + apid surface from PR-A/B are sufficient). ~600 LOC;
  ships the middleware matrix above, the 60s `last_used_at`
  debouncer, and the e2e test.

Slot precheck runs at PR-open time via
`scripts/ci/check_migration_slots.sh`. If PR #5-A would collide
with another PR's real migration (currently PR #984 reserves
00305 for `deployments_annotation`), PR #5-A renumbers to
**00305** and PR #5-B renumbers to **00310** — fallback is one
precheck run away.

## Security & GDPR notes

- Plaintext never logged. The middleware stamps `consumer_id`
  (the row's UUID, not the key) into `r.Context()`.
- `last_used_at` is the only column that gets touched after
  creation; the wire path uses the in-memory debouncer so a
  hot-loop customer can't generate write amplification.
- GDPR: hard-deleting an account cascades `consumer_keys` rows.
  Verified in PR #5-A's `memstore_gdpr_test.go` mirror and the
  pgstore `gdpr_cascade_test.go`.
- The hash is SHA-256 of the FULL plaintext (`ck_<prefix>_<secret>`),
  not of the secret alone — so the prefix is part of the input
  and a secret reused across two different prefixes produces two
  different hashes. The (app_id, prefix) index then narrows the
  hash compare to ~one row.

## Amendment 2026-11-12 (Hunt 34 H34-36, H34-37)

- `read` covers the safe methods `GET`, `HEAD` and `OPTIONS`, not `GET`
  alone. `HEAD` is `GET` without a body and edge rules already treat it so;
  refusing it broke link checkers and `curl -I` with a read key.
- In `required` mode a CORS preflight (`OPTIONS` with `Origin` and
  `Access-Control-Request-Method`) and no `Authorization` header passes
  without a consumer. Browsers never attach credentials to a preflight, so
  the gate refused every cross-origin call before the app's CORS rule could
  answer. The preflight stamps no consumer and the request that follows is
  checked as before.
