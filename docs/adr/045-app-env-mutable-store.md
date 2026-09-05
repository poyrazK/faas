# ADR-045 — Customer env vars as a mutable per-app plaintext store (issue #395)

Status: Accepted, 2026-07-28. Owner: @poyrazK. Related: issue #395
("API: Env vars are manifest-only — changing one variable requires a
full redeploy"). Slot 045 is the next free number after ADR-044.

## Context

`AppManifest.env` exists in `pkg/api/appmanifest.go:25-40` and the
data shape is wired end-to-end (image-config InjectManifest in
`pkg/imaged/handler.go:793-825`, builder-merge in
`pkg/rootfs/build.go:403-417`), but the runtime reader — apid's
`SetAppManifest` at `pkg/state/queries.sql:92-93` — has zero callers.
Env that the guest actually sees comes from two other channels today:
OCI `ImageConfig` baked into drive1 at deploy, and sealed secrets
re-staged on every wake at `pkg/fcvm/manager.go:499-537
stageSecretsEnv`. The console's Env Vars page is therefore read-only —
flipping `LOG_LEVEL=debug` requires a rebuild + redeploy, the
snapshot is invalidated, and the customer eats a cold-wake on the
next request.

Customers store non-sensitive config as sealed secrets today because
secrets are mutable and env vars are not. That wastes the per-app
secret quota (`SecretCountMax` ≤ 100 across plans) and blurs the
audit trail: a `secret.set` audit kind stops meaning "a credential
changed" because the row holds `LOG_LEVEL=debug` half the time.

Issue #395 reports this as a console gap; the surface is small enough
that fixing it with a third env channel — a mutable, plaintext,
per-app store mirroring `app_secrets`'s CRUD shape — closes the gap
without touching the build pipeline.

## Decision

**1. New `app_envs` table mirroring `app_secrets` minus ciphertext.**
Plaintext TEXT values; same `(app_id, key)` primary key shape; same
`^[A-Z][A-Z0-9_]*$` SQL CHECK constraint (so a future apid bug that
skips `ValidateEnvKey` cannot crash the DB on insert). Migration
00061 ships the table + `account_id`, `app_id` indexes. Migration
00063 widens `api_keys_scopes_vocab_chk` to admit `env:read` /
`env:write` scope strings (the DB CHECK is closed-vocabulary, so
adding new scopes to the in-Go constant alone is insufficient — the
DB will reject `INSERT … WITH … 'env:write'` until 00063 lands).
**Both migrations ship in the same PR** so the rollout window
between them doesn't surface a broken `POST /v1/keys`.

**2. Three HTTP routes mirror `/v1/apps/{slug}/secrets` minus the
seal step.**
```
GET    /v1/apps/{slug}/env          → listEnv
PUT    /v1/apps/{slug}/env/{key}    → setEnv
DELETE /v1/apps/{slug}/env/{key}    → deleteEnv
```
The wire shape (`PutAppEnvRequest{Value}`, `AppEnvResponse{Key,
created_at, updated_at}`, `AppEnvListResponse{Env, quota_max,
count}`) mirrors the secret DTOs field-for-field so the SDK can
reuse the same parsing branch. **The plaintext VALUE never appears
in the GET response** — only metadata does, mirroring the sealed-
secret response shape (a customer gets the value at process start
inside the guest via `/etc/faas/env.json`; the management API never
echoes it).

**3. New scopes `env:read` and `env:write`.** Closed vocabulary at
`pkg/api/apikey.go::validScopes` mirrors the widened DB CHECK.
Surface-set `ScopesEnvWriteSurface = {admin, env:write}` grants
PUT/DELETE; GET reuses the existing `ScopesReadSurface` (admin +
`apps:read`). **env:write is NOT MFA-gated** because env vars are
explicitly non-sensitive runtime config — see the rejected
alternatives for the rationale. The audit-kind signal `env.set` /
`env.deleted` is distinct from `secret.set` / `secret.deleted` so
the secret-quota bypass argument is closed: a customer cannot grant
`secrets:write` through an env-var surface without losing the
audit-kind signal that says "config change" vs "credential change".

**4. Per-plan quota `EnvVarsMax` + `EnvValueMaxBytes`.** Encoded in
`pkg/api/limits.go` as plan-level fields:
| Plan | EnvVarsMax | EnvValueMaxBytes |
|---|---|---|
| Free | 8 | 4 KiB |
| Hobby | 32 | 8 KiB |
| Pro | 64 | 16 KiB |
| Scale | 256 | 32 KiB |
The byte cap is enforced BEFORE the row hits PG (defense in depth —
`PutAppEnvRequest.Validate` checks against `limits.EnvValueMaxBytes`,
mirroring the `PutAppSecretRequest` seal path's pre-write check).

**5. Semantics: applies on the next cold wake.**
There is no in-place env update of a running instance. When an env row is
changed, apid marks every restorable snapshot for every deployment of the app
stale. This covers traffic splits and rollback windows, so the next wake
cannot restore a process image carrying the old environment.
The wake then cold-boots and stages the new file. Wire contract: schedd's `Engine.Wake` loads
`ListAppEnv` at line 516 (and the prime path at line 897) and
threads the rows through the existing `AppSpec.APIEnv` field into
the `vmmdpb.AppSpec.api_env` proto field (#9). vmmd's
`Manager.Wake` reads `req.APIEnvEntries`, marshals the merged map
to JSON, and calls `StageAPIEnv` which writes `/etc/faas/env.json`
on drive1 (mirroring `stageSecretsEnv`'s `/etc/faas/secrets.env`
write). guest-init's `BuildEnvWithSecrets` merges `env.json` into
the process env with precedence `OS environ < manifest env < api
env < secrets` (the 4-layer fixture is pinned by
`guest/init/app_test.go::TestBuildEnv_FourLayerPrecedence`).

**6. Plaintext by contract (acceptance #5).** Values are stored
as-is, never sealed, never re-encrypted. The rationale is that
there is no secret-vs-non-secret classifier that doesn't itself
need maintenance, and per the issue body, non-sensitive defaults
belong here, anything sensitive is told to use sealed secrets.
The audit posture mirrors the secrets handler: `slog.Info("env
set", "key", key, "value_bytes", logsanitize.RedactValue(value))`
never logs the plaintext. The audit emit (`s.audit.Emit("env.set",
…{"app_id", "name"})`) never carries the plaintext either.

**7. Redeploy preserves API env (acceptance #6).** API env is per-app,
not per-deployment. The deployment-create handler does not call
`DeleteAppEnv`; the env rows survive any number of redeploys.
Pinned by `cmd/apid/handlers_env_test.go::TestEnv_RedeployPreservesEnv`.

## Consequences

- `pkg/api/apikey.go::validScopes` adds two entries; the `//go:embed
  pkg/apid/openapi.yaml` mirror tracks via `make spec-sync`.
- `pkg/api/limits.go` gains `EnvVarsMax` + `EnvValueMaxBytes`
  per plan; the limits test pins the four plan values (Free 8/4K,
  Hobby 32/8K, Pro 64/16K, Scale 256/32K).
- Migration 00061 + 00063 ship in one PR. The cross-PR slot gate
  (PR #377 / ADR-041) carries the new numbers; `make
  check-migration-slots` must pass.
- **Migration 00063 DOWN is not safe for env-scoped keys.** The
  Down block restores the v2 6-string vocab; a row with
  `env:read` / `env:write` scopes that landed under the new
  constraint would fail this DOWN constraint on apply. Same
  hazard as 00046 (the parent migration), carried forward by
  intent — any rollback of 00063 requires operators to either
  (a) refuse to run while env-scoped rows exist in api_keys,
  or (b) revoke env-scoped keys BEFORE the Down applies. The
  CD pipeline (`cd-deploy`) does not auto-Down across the
  migration boundary, so the live hazard is "operator runs
  goose down by hand during an incident" — surface this in the
  deploy runbook.
- `cmd/apid/spec_compliance_test.go::TestSpecCompliance` exercises
  the new routes + DTOs + error codes; the spec compliance gate
  validates the OpenAPI ↔ Go code parity on every PR.
- Audit kinds `env.set` / `env.deleted` are documented at
  `api/openapi.yaml::AuditEventResponse.kind` description (line
  ~3166). Dashboards render them as a separate quota / change
  signal.
- Console Env Vars page (faas-frontend, separate repo) becomes
  writable in a follow-up PR; the current PR ships the API surface
  only.
- guest-init binary grows by one syscall (`open` on
  `/etc/faas/env.json`); the missing-file path is the same no-op
  the secrets reader already uses (`isNotExist → return nil, nil`).

## Rejected alternatives

- **Column on `apps.manifest` jsonb.** The runtime reader
  (`SetAppManifest`) has zero callers today — wakes don't actually
  consume `manifest.env` from PG; they only consume the
  drive1-baked OCI ImageConfig. Wiring a wake-time read seam is
  larger than the new table + 3 routes + 1 staging block. Plus
  manifest is shared with entrypoint/port/etc., semantically a
  deploy contract, not a runtime one.
- **Force-park on PUT.** That's the pain the issue complains
  about. A PUT that costs a cold-boot is barely better than the
  current "rebuild + redeploy + cold-boot" path.
- **Live update of running instance.** Adds a new `vmmd.SetEnv`
  RPC and a vsock listener to a deliberately minimal wake path. Out
  of scope for the tier-2 public-launch feature slice — the next-
  wake contract is explicit in the issue.
- **Centralize with secrets under sealing.** Issue #395 explicitly
  rejects this: it doubles the audit kind to be ambiguous and
  burns the secret quota for non-secret values. The whole point
  of the env surface is to let the secret audit mean "credential
  change".
- **MFA on env:write (parity with secrets:write).** Rejected. MFA
  is gated on `secrets:write` because credential write is the
  highest-blast-radius action — losing a credential lets an
  attacker exfiltrate customer data. Flipping `LOG_LEVEL=debug`
  to `LOG_LEVEL=info` doesn't carry the same risk; gating it
  behind MFA adds friction (every `flask debug` env tweak needs a
  TOTP code) without a security win. The audit signal still
  surfaces the change to the customer's audit timeline; a
  compromised admin key is the loss-bearing case and that scenario
  is gated upstream by `admin` scope's own MFA requirement.
