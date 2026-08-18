# Sub-issue #05 — Application API keys with scopes and expiration

Parent: [README.md](README.md)

## Problem

Today Gregale has exactly one kind of bearer credential: the **platform
API key** bound to a Gregale account, with scopes like `apps:read` that
authorize calls to `/v1/apps`. Schema:

- `schema.sql:506-526` — `api_keys` table, account-scoped, `expires_at`,
  per-key grants.
- `pkg/api/apikey.go:104-138` — scope catalog.

ADR-079 (`docs/adr/079-per-app-public-auth.md:71-87`) acknowledges that
per-app public auth currently *reuses* the owner's `apps:read` platform
key. That is a deliberate v1 shortcut, not a target shape.

What is missing is the **consumer** credential — a key the customer
issues to one of their own end-users (or service accounts), scoped to a
single app, with application-defined scopes and an expiry the customer
controls.

Without this:

- Per-consumer rate limits (sub-issue #04 of the audit, item 4 in the
  table — but the per-route one — and the throttle-keying in ADR-104)
  fall back to identity-less buckets.
- Consumer-level analytics (sub-issue #06) have nothing to key by.
- Safe replay (sub-issue #09) cannot redact keys it doesn't know about.

This is the **load-bearing sub-issue** of the cluster. The mega issue's
rollout order makes it the gate.

## Proposal

New `consumer_keys` table:

```sql
CREATE TABLE consumer_keys (
  id              UUID NOT NULL DEFAULT gen_random_uuid() PRIMARY KEY,
  account_id      UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  app_id          UUID NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
  name            TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 64),
  prefix          TEXT NOT NULL CHECK (prefix ~ '^ck_[A-Za-z0-9]{6}$'),
  hashed_secret   BYTEA NOT NULL,
  scopes          TEXT[] NOT NULL CHECK (cardinality(scopes) <= 32),
  expires_at      TIMESTAMPTZ NULL,
  last_used_at    TIMESTAMPTZ NULL,
  revoked_at      TIMESTAMPTZ NULL,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (account_id, app_id, name)
);
CREATE INDEX ON consumer_keys (app_id) WHERE revoked_at IS NULL;
```

`prefix` is the human-shareable portion of the key (e.g. `ck_a1b2c3_`).
The full secret is shown to the operator **once** at creation; only the
prefix and the SHA-256 hash are stored. Mirrors the G2 lean for env
secrets (sealed at rest, never logged).

### Wire shape on the public edge

The customer distributes keys like `ck_a1b2c3_<32-byte-secret>` to their
consumers. Consumers send `Authorization: Bearer ck_a1b2c3_<secret>` on
public requests to the app.

### Gateway enforcement

New middleware in `gatewayd-internal`:

1. On request arrival, look for `Authorization: Bearer ck_<prefix>_<secret>`.
2. Resolve `(prefix, hashed_secret)` → `consumer_keys` row scoped by the
   resolved `app_id` (the request already resolved to an app via hostname
   lookup; see `pkg/gateway/forwardproxy.go`).
3. Check `revoked_at IS NULL AND (expires_at IS NULL OR expires_at > now())`.
4. Check scopes against any rule that gates the matched path (e.g. a
   future `kind=consumer_scope` rule — not in scope here).
5. Stamp the request context with `consumer_id` so downstream features
   (rate limit keying, request log column, replay redaction) can use it.

### Failure modes

| Condition | Response |
|---|---|
| No `Authorization` header | 401 `consumer_key_required` (only on paths with `consumer_auth=required` rule, else pass-through) |
| Prefix not found | 401 `consumer_key_invalid` |
| Secret hash mismatch | 401 `consumer_key_invalid` |
| Revoked or expired | 401 `consumer_key_inactive` |
| Scope missing | 403 `consumer_scope_missing` |

These are RFC 7807 codes added to the error catalog in
`pkg/api/error_codes.go`.

### Backward compat

- Existing platform `api_keys` unchanged.
- ADR-079 stays: the platform key continues to authorize `/v1/apps` etc.
- New apps default to `consumer_auth=optional`; operators opt in per app
  with a single `PATCH /v1/apps/{slug} { "consumer_auth": "required" }`.

### Limits (pkg/api/limits.go)

- `consumer_keys_per_app` = 100 (Free/Hobby/Pro), 1000 (Scale).
- `consumer_keys_per_account` = 250 (Free/Hobby), 2500 (Pro), 25000 (Scale).
- `consumer_key_secret_bytes` = 32 (constant; reject shorter at creation).

## Acceptance

1. Operator issues a consumer key for an app, sees the secret once.
2. Consumer presents the key; app's edge rule with `consumer_auth=required`
   accepts and 200s.
3. Wrong secret → 401 `consumer_key_invalid` (not 500, no info leak).
4. Expired or revoked key → 401 `consumer_key_inactive`.
5. Revoking a key causes the next request to fail; the throttle bucket
   tied to that key drains within 60 s.
6. Migration is numbered in the next available slot; cross-PR fence check
   runs before commit.

## Dependencies

None — but everything in the identity row of the rollout (#06, #07's
consumer label, #08's `consumer_id` column, #09's redaction) depends on
this.

## Audit provenance

- `schema.sql:506-526` — `api_keys` table is account-scoped only.
- `pkg/api/apikey.go:104-138` — scope catalog is platform control-plane.
- `docs/adr/079-per-app-public-auth.md:71-87` — explicit "reuse owner
  account's apps:read platform key" shortcut.
