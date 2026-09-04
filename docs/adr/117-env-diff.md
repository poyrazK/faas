# ADR-117 — env-diff matrix endpoint + value_hash discriminator

Status: **proposed** (PR-C / cluster: ADR-092 PR-A + PR-B + PR-C)
Author: PR-C working group
Date: 2026-08-19
Cross-refs: ADR-089 (sealed secrets), ADR-090 (named envs), ADR-091
(deployments + scope), ADR-092 (scoped secrets — the parent cluster).

## Context

PR-A (#849, data plumbing) and PR-B (#970, `?scope=__all__` wire
surface) shipped the scope-aware sealed-secret shape to main. The
flat-list response carries `{key, scope, kid, created_at, updated_at}`
plus per-scope `secrets_by_scope` arms for the nested view. The
**presence** question is now answerable — a customer can ask "is
DATABASE_URL set in prod vs staging?" — but the **value-equality**
question is still open: a customer looking at `STRIPE_KEY=sk_test_xxx`
in `dev` and `STRIPE_KEY=sk_test_xxx` in `staging` cannot tell from
the kid + updated_at alone whether the plaintexts are actually
identical (kid is identity-shaped, not value-shaped).

The user's blocker (feature 8 from their product list) is:

> "Environment comparison — a simple three-column view: Variable |
> Preview | Staging | Production — `DATABASE_URL ✓ ✓ ✓`,
> `STRIPE_KEY Test Test Live`, `LOG_LEVEL debug info info`,
> `SENTRY_DSN missing ✓ ✓`. Never reveal secret values by default —
> only whether they differ."

This ADR closes that feature.

## Decisions

### D1. value_hash HMAC field on the sealed envelope

Per-row HMAC-SHA256 of the **plaintext** (NOT the ciphertext — see
the §11 caveat below), keyed by a per-host 32-byte key in
`/etc/faas/secrets/host.hmac.key` (mode `0o400`). Truncated to 16
hex chars (64 bits). Collision probability at `SecretCountMax <= 100`
is ≈ 5.4 × 10^-16 — negligible for customer-facing use.

**The hash is computed over the plaintext, BEFORE `SealOne`.** age
X25519 + ChaCha20-Poly1305 is probabilistically non-deterministic
(fresh ephemeral X25519 + fresh nonce per Seal), so identical
plaintexts produce byte-different ciphertexts. A ciphertext-derived
hash would diverge for every row and the discriminator would be
useless.

Distinct from `kid` (identity-shaped, not value-shaped) and from the
plaintext itself (sealed). The hash travels on the wire but the
HMAC key does not.

### D2. Trust boundary

`host.hmac.key` lives alongside `host.age.pub` and follows the same
`0o400` + startup-or-503 posture. Never logged. Never crosses the
apid → schedd → vmmd boundary; the `value_hash` travels but the
HMAC key does not. The diff endpoint reads `value_hash` from the
Store layer directly; the HMAC key is only needed at write time
(PUT/rotate/rekey).

Bootstrap coordination: `apid` refusing to start is correct posture,
but the bootstrap step that creates the file must land before the
apid rollout. Operator step (documented in the PR description, NOT
a git-tracked file):

```bash
openssl rand -out /etc/faas/secrets/host.hmac.key 32
chmod 0400 /etc/faas/secrets/host.hmac.key
chown root:root /etc/faas/secrets/host.hmac.key
```

This is sequenced ahead of the apid rollout by the operator's
release-bundle install (per ADR-113 PR-A memory
`pr-933-adr-113-pr-a-opened-2026-08-16`).

### D3. GET /v1/apps/{slug}/env-diff endpoint

Reads the full app secret + env surface, unions keys, renders a
matrix where each cell carries `{present, value_hash}` (and `value`
for env vars only — secrets never reveal). No `?scope=` filter in v1
(the matrix is the whole point).

Response shape (mirrors `pkg/api/env_diff.go`):

```json
{
  "app_slug": "demo-app",
  "scopes": ["default", "prod", "staging"],
  "rows": [
    {"key": "DATABASE_URL", "kind": "secret", "cells": {
      "prod": {"present": true, "value_hash": "abcdef0123456789"},
      "staging": {"present": true, "value_hash": "deadbeefcafebabe"}
    }},
    {"key": "LOG_LEVEL", "kind": "env", "cells": {
      "default": {"present": true, "value": "info"},
      "prod": {"present": true, "value": "debug"},
      "staging": {"present": true, "value": "info"}
    }},
    {"key": "STRIPE_KEY", "kind": "secret", "cells": {
      "prod": {"present": true, "value_hash": "1234567890abcdef"}
    }}
  ],
  "generated_at": "2026-08-19T13:00:00Z"
}
```

Rows sorted ASC by key; scopes sorted ASC. A scope with no row
emits `{present: false}` for the missing scopes in that row — the
matrix is rectangular at the response level.

### D4. Backward compatibility

`value_hash` is `omitempty` on the wire (AppSecretResponse,
ScopedAppSecretResponse, AccountAppSecretResponse). Pre-PR-C rows
have `value_hash = NULL` per the migration column shape. The
JSON wire emits `"value_hash": ""` only when the row has an empty
plaintext (which would be a handler bug — the handler never accepts
empty plaintext). Dashboards reading existing rows see no field;
new rows see 16 hex chars.

**No backfill sweep.** The gap closes lazily on rotate. A sweep at
PR-C merge time would require unsealing every ciphertext, which is
a hot operation on Scale-tier accounts (cf. the rekey-pass lazy
backfill at §D6).

### D5. Wire surface — ValueHash field on existing DTOs

`AppSecretResponse.ValueHash`, `ScopedAppSecretResponse.ValueHash`,
`AccountAppSecretResponse.ValueHash`. `omitempty` is load-bearing:
pre-PR-C clients see no field; new clients see 16 hex. The Python
SDK validator pattern must be non-strict (cf. `pkg/api/env.go`
precedent for `^[a-z_]+$` where empty is valid).

### D6. Rekey lazy backfill

`pkg/rekey/rekey.go` switches to
`UpsertAppSecretWithKidAndValueHashInScope`. The rekey worker
unseals every row, gets the plaintext, re-seals. To stamp
`value_hash` it computes `secretbox.ValueFingerprint(plaintext,
hostHMACKey())` BEFORE the re-Seal — same call site as the PUT
path. The rekey pass is the only place we reliably backfill
`value_hash` for pre-PR-C rows.

### D7. Quota, audit, dashboard

- **Quota**: no change. The matrix renders the existing secret +
  env surfaces; `SecretCountMax` + `EnvCountMax` are unchanged.
- **Audit**: no `value_hash_short` field on the audit emit. A
  16-bit HMAC prefix is observable across dashboards and was
  flagged as a cross-customer correlation vector. The audit
  payload stays `{app_id, name, scope}` — the diff endpoint is
  the only observation surface, and it is authorized-scoped by
  `loadApp`.
- **Dashboard**: the JSON is the input the customer-facing
  dashboard reads to render the comparison table. The dashboard
  itself is sequenced behind this PR (per ADR-092 D8's deferred
  "Dashboard 'scope secrets' UI surface").

### D8. gregale env diff CLI

`gregale env diff --app <slug>` renders the matrix as a text table:

```
KEY                KIND    default  prod  staging
DATABASE_URL       secret  -        ≠     ≠
LOG_LEVEL          env     info     debug info
STRIPE_KEY         secret  -        ==    -
```

Cells:
- `-` = missing
- `==` = `value_hash` matches (secrets) or value equals (env).
- `≠` = `value_hash` differs (secrets) or value differs (env).
- For env vars, the cell shows the literal value (env is public).
- For secrets, the cell never shows a value — only `-` / `==` / `≠`.

`--json` flag (reuses `jsonOutput` global) for shell pipelines.

### D9. Migration slot + reserve fence

`migrations/00296_app_secret_value_hash.sql`:

```sql
alter table app_secrets
  add column value_hash text;
alter table app_secrets
  add constraint app_secrets_value_hash_shape
    check (value_hash is null or length(value_hash) <= 16);
```

NULLABLE (NOT NULL DEFAULT ''). NULL = "pre-PR-C row, never
re-stamped". The empty-string vs NULL distinction matches the
`omitempty` wire shape. Forward-only Down mirrors 00217 + 00229 +
00206 precedent.

**Original slot plan** was 00291 with 00288-00290 reserves;
rebasing onto `origin/main` (post PR-B / issue #879 cluster) revealed
00288-00295 are all consumed. Bumped to **00296** (free on main).
The three reserves were dropped — the slot fence pattern (memory
`cross-pr-slot-fence-reservation-fence-pattern`) protects an
unclaimed slot, but consumed slots need no fence.

## File inventory

- `docs/adr/117-env-diff.md` (NEW) — this ADR.
- `migrations/00296_app_secret_value_hash.sql` (NEW) — schema.
- `migrations/00296_app_secret_value_hash_test.go` (NEW) — pins.
- `pkg/secretbox/value_hash.go` (NEW) — `ValueFingerprint` helper.
- `pkg/secretbox/value_hash_test.go` (NEW) — known-answer + edge tests.
- `cmd/apid/host_keys_hmac.go` (NEW) — `/etc/faas/secrets/host.hmac.key` loader.
- `cmd/apid/host_keys_hmac_test.go` (NEW) — perm-check + length-check tests.
- `pkg/state/types.go` — `AppSecret.ValueHash` + `AccountAppSecret.ValueHash`.
- `pkg/state/store.go` — `UpsertAppSecretWithKidAndValueHashInScope`.
- `pkg/state/pgstore.go` + `pkg/state/memstore.go` — mirrors.
- `pkg/state/{pgstore,memstore}_value_hash_test.go` (NEW) — 9 assertions.
- `pkg/api/secrets.go` — `ValueHash` on the three response DTOs.
- `pkg/api/env_diff.go` (NEW) — `EnvDiffResponse` / `EnvDiffRow` / `EnvDiffCell` / `EnvDiffKind`.
- `cmd/apid/handlers_secrets.go` — `sealAndPersist` computes `ValueFingerprint` BEFORE `SealOne`.
- `cmd/apid/handlers_secrets_rotate.go` — same change.
- `cmd/apid/handlers_env_diff.go` (NEW) — `envDiff` handler + `buildEnvDiffResponse` helper.
- `cmd/apid/server.go` — register `GET /v1/apps/{slug}/env-diff`.
- `cmd/gregale/commands_env_diff.go` (NEW) — text table renderer.
- `cmd/gregale/cli_meta.go` + `cmd/gregale/commands3.go` — dispatch entry.
- `pkg/rekey/rekey.go` — switch to `UpsertAppSecretWithKidAndValueHashInScope`.
- `api/openapi.yaml` — new route + schemas + EnvDiffKind enum.
- `pkg/apid/openapi.yaml` — `make spec-sync` mirror.
- `sdk/{go,node,python}/...` — `make sdk-gen`.
- `cmd/e2e/env_diff_e2e_test.go` (NEW) — 9 wire-surface assertions.

## Consequences

### Positive

- Customer-facing "is this secret equal across environments"
  question is now answerable without ever unsealing.
- Dashboard team has a single JSON endpoint to read.
- CLI has a human-readable matrix table.
- Pre-PR-C rows degrade gracefully (no field, lazy backfill on rotate).

### Negative

- New on-disk secret (`/etc/faas/secrets/host.hmac.key`) requires
  bootstrap coordination ahead of the apid rollout. Same posture as
  `host.age.pub`.
- Lazy backfill leaves pre-PR-C rows in an "unknown equality"
  state until rotated. Acceptable for v1 (rotations are common;
  backfill-at-merge would unseal every ciphertext on a Scale-tier
  account).
- New migration slot 00296 vs original plan 00291 — fence
  collisions caught the drift before PR-B merged.

### Neutral

- Audit payload unchanged. Wire surface widens by one optional
  field per DTO. CLI gains one subcommand. Dashboard renders
  externally.

## Rejected alternatives

1. **kid+updated_at heuristic.** Insufficient signal: two secrets
   with the same kid (same host identity) and same updated_at
   could still differ in plaintext. The heuristic confuses
   "identity match" with "value match".

2. **salt+4-char prefix blob on the wire.** §11 review overhead,
   value material leak (the prefix is 4 hex of HMAC-SHA256 — not
   the secret itself, but a stable derivative). The full 16-hex
   truncated hash is the right tradeoff: non-reversible under the
   host key, sufficient discriminator at SecretCountMax scales.

3. **No per-host HMAC key.** Everyone-with-tenant-shared-key would
   trivially collide (same key + same plaintext = same hash across
   every box, allowing cross-customer correlation). The per-host
   key is the §11-grade boundary.

4. **Backfill sweep at PR-C merge time.** Would unseal every
   ciphertext on a Scale-tier account. The rekey lazy backfill
   handles pre-PR-C rows safely on rotation.

5. **Real-time SSE subscription on the diff.** Pull-model is
   sufficient for the dashboard (per ADR-091 wire surface). SSE
   would be a separate ADR.

## Open questions

1. **HMAC key rotation has no migration path.** If
   `/etc/faas/secrets/host.hmac.key` is re-read at startup, apid
   crashes mid-flight (the `value_hash` for in-flight rows will
   mismatch). Mitigation: load the key once at startup, never
   re-read. The HMAC key is *per-host*, not *per-account* —
   rotation is a host-level event, not a row-level event.

2. **Cross-PR slot precheck.** Future ADR clusters that touch
   `app_secrets` (or any `migrations/` slot) MUST run
   `git fetch origin 'refs/pull/*/head:refs/remotes/origin/pr/*'`
   before opening the PR. The `git ls-tree origin/main` check is
   necessary but not sufficient (memory
   `cross-pr-slot-precheck-pr-867-collision-2026-08-13`).

3. **§11 review surface.** The new HMAC key is a new secret on
   disk. Same posture as `host.age.pub` (`0o400` enforcement,
   503 on missing, never logged). The §11 review packet is the
   diff of `cmd/apid/main.go` (loader) +
   `pkg/secretbox/value_hash.go` (helper) + this ADR.

4. **Wire shape width.** `AppSecretResponse` gains `value_hash`
   (16 hex, `omitempty`). Pre-PR-C clients see no field. New
   clients see 16 hex. The Python SDK validator pattern is
   `^[a-f0-9]{16}$` — confirm the codegen produces a non-strict
   validator (per `pkg/api/env.go` precedent).

## Acceptance

- [ ] `go build ./...`
- [ ] `make lint`
- [ ] `make test` (includes new `pkg/secretbox/value_hash_test.go`,
      `pkg/state/{pgstore,memstore}_value_hash_test.go`,
      `cmd/apid/host_keys_hmac_test.go`,
      `migrations/00296_app_secret_value_hash_test.go`).
- [ ] `make migrate-test` (00296 applies cleanly on the
      migrator's seed DB).
- [ ] `make spec-check` (yaml mirror in sync).
- [ ] `make sdk-gen` (Go/Node/Python SDKs regenerate cleanly).
- [ ] `make e2e` (cmd/e2e/env_diff_e2e_test runs across 4 shards).
- [ ] `make scan` (no new Go vulnerability class) plus the exact OCI image
      scans enforced by `.github/workflows/images.yml`.
- [ ] `make load` (GET /env-diff p99 < 50ms at Hobby scale).
- [ ] `/code-review medium` clean.

## References

- `docs/adr/089-sealed-secrets.md` — sealed envelope shape.
- `docs/adr/090-named-envs.md` — env `?scope=` seam.
- `docs/adr/091-deployments-scope.md` — wake-time scope selection.
- `docs/adr/092-scoped-secrets.md` — the parent cluster
  (PR-A #849, PR-B #970).
- `pkg/secretbox/kid.go` — `IdentityFingerprint` (kid) sibling.
- `pkg/secretbox/seal.go` — `SealOne` call site.
- `pkg/state/store.go` — `UpsertAppSecretWithKidInScope` (PR-A).
- `pkg/api/env.go` — env discriminated-union shape (precedent).
- `pkg/api/secrets.go` — DTO widening.
- `cmd/apid/handlers_env.go::loadApp` — account-aware guard.
- `cmd/apid/host_keys.go` — `/etc/faas/secrets/host.age.pub` loader.
- `migrations/00217_app_secrets_scope.sql` — PR-A migration.
- `cmd/e2e/secrets_scope_e2e_test.go` — PR-B e2e harness.
- Roadmap: 2026-08-10 `secrets-envs-roadmap-decisions-2026-08-10.md`.
