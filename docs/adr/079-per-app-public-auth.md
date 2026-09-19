# ADR-079 · Per-app public-URL auth (open|bearer|basic)

- **Status:** accepted
- **Superseded (in part, PR-E):** prose referred to the monolithic
  `cmd/gatewayd/` daemon split by ADR-070 into `gatewayd-public` (TLS-only
  edge) and `gatewayd-internal` (routing + wake + proxy). Body is preserved
  verbatim; readers should substitute "gatewayd-internal" for the
  routing/wake/proxy path and "gatewayd-public" for the certmagic/TLS path.
  `cmd/gatewayd/<file>.go` citations in this body are stale; see PR-E for
  the new file locations.
- **Date:** 2026-08-06
- **Issue:** #477
- **Supersedes:** none

## Context

Issue #477 requests a Cloud Run–style opt-in to gate the
per-app public hostname behind `Authorization: Bearer
<token>` (`bearer`) or `Authorization: Basic …`
(`basic`). Today every customer app is reachable
anonymously, with the closest precedent (issue #560) being
the **all-or-nothing** `require_authn` boolean that flips
the same gate for every request. Issue #477 lands in the
intermediate zone between `require_authn=true` and the
default anonymous surface, with a third state `open` that
is explicit-naming for the pre-#477 default so the wire
shape is a closed enum and a future "ip-allowlist" or
"mTLS" mode slots in without an extra migration.

Customer enumeration (UX spec §6.5 + the customer support
themes from the Issue #560 closure retro):

- `open` — the pre-#477 default. No header consulted;
  every request passes (subject to the existing
  require_authn / egress guard rails).
- `bearer` — `Authorization: Bearer <fp_live_…>` with
  `apps:read` scope on the app's owning account. Cross-
  account tokens receive `403 insufficient_scope`.
  Re-uses the require_authn chain so `Gatewayd.RequireAuthnAllowed`
  already in production remains the canonical authenticator.
- `basic` — `Authorization: Basic <user:pass>` with
  credentials sealed under the `APP_BASIC_AUTH`
  secretbox namespace at PATCH time. Gatewayd-internal
  unseals at boot (unseal-at-boot strategy mirrors
  webhook secrets per ADR-076).

Today the answer to "I want my app public but not to the
open Internet" requires a corporate-egress-allowlist
configuration (issue #185) that doesn't apply on the
data-plane side. Issue #477 closes that gap.

## Decision

### 1. Closed-enum mode column

`apps.public_auth_mode text NOT NULL DEFAULT 'open'` with a
CHECK constraint matching the closed set
`{open, bearer, basic}`. Chosen over a `jsonb` blob
because:

- sqlc + store layer enumerate this once (the public auth
  mode is read on every request — sqlc column types beat
  every-shape-bytes).
- The wire shape is naturally an enum string; a jsonb
  requires operator UX to learn the closed set.
- Adding a future mode (mTLS, IP allowlist) is a
  migration that touches the CHECK AND extends
  `pkg/api.AppPublicAuthMode*` constants — surfaced in
  the same change so the closed set stays in lockstep.

### 2. Reuse the `apps:read` scope for `bearer`

A bearer key unlock means "I have `apps:read` on the
account". Chosen over minting a new `apps:public_url`
scope because:

- `apps:read` already exists (issue #185 / ADR-034) —
  no scope migration.
- The use case maps cleanly: a CI key with `apps:read`
  for read-only ops also reads the public URL
  anonymously when the lock is open; the cross-account
  authz check ensures the key belongs to the app's
  owning account.
- The misuse surface ("the CI key can hit the
  bearer-locked app") is operationally loud: the
  dashboard surfaces it on first call and the
  issue-#560 retro carry-over doc has the warning copy.

### 3. Per-key-hash cache invalidation on `key_changed`

The gateway cache invalidates by `key_id` hash on
`pg_notify` channel `db.NotifyKeyChanged`. Chosen over
full-flush on any key change because:

- Full-flush would wipe the cache every time **any**
  key in any account rotates — a 6-customer Hobby box
  rotates keys hourly; full-flush would pin every
  cache lookup to the auth path.
- Route cache's per-entity pattern (`InvalidateApp(id)`)
  is the established precedent for surgical
  invalidation; the per-key-hash extension point already
  exists at `cmd/gatewayd-internal/backend.go:123` —
  this ADR just adds the `InvalidateAPIKey(keyIDHash)`
  method that uses it.

A simpler cache busting (no cache at all) was rejected:
60s TTL + pg_notify invalidation gives at-most-60s worst
case + event-driven eviction, both well under the
bearer window-of-revoke.

### 4. Seal-at-PATCH / unseal-at-boot for `basic`

The basic-auth credentials are sealed by the apid PATCH
path under the `APP_BASIC_AUTH` secretbox namespace and
unsealed at gatewayd-internal boot via the host-age
identity. Chosen over:

- **Per-request unseal** — would key-rotate transparently
  but adds a 2-3 ms cost to every request and reads the
  secretbox decryption key from a sealed-envelope
  bootstrap step that mirrors webhook unseal.
- **Env-var-at-PATCH** (the issue body's
  `username_env/password_env` shape) — would require
  re-key every credential rotation through the app
  env-var surface, threading the credentials through
  drive1 overlay mount. ADR-076 established
  seal-at-PATCH as the canonical pattern (webhook
  secrets already work that way).

### 5. Plan-tier gating

| Plan  | open | bearer | basic |
| ----- | ---- | ------ | ----- |
| Free  | ✓    | 402    | 402   |
| Hobby | ✓    | ✓      | 402   |
| Pro   | ✓    | ✓      | ✓     |
| Scale | ✓    | ✓      | ✓     |

The plan tier splits the cost recovery: bearer requires
the same `apps:read` scope as the require_authn gate
(Hobby+), basic adds the secretbox unseal cost which
is Pro+. Both gates return `402 plan_*_not_allowed`
(matching the streaming / warm-snapshot / cron /
webhook family — `StatusPaymentRequired` is the
codebase convention for tier-locked features).

### 6. Re-redaction invariant

Audit rows + structured logs MUST NOT carry the
plaintext `basic_user`, `basic_pass`, or the sealed
blob. The redaction posture is enforced at three
levels:

1. The `app.public_auth_changed` audit row carries
   mode strings + `has_basic_creds: bool` only.
2. The `app.updated` row's `old/new` maps carry the
   `public_auth` mode string only — never the blob.
3. The test
   `cmd/apid/handlers_public_auth_test.go::TestPublicAuthPatch_AuditEmitsWithRedaction`
   does a direct substring check against known
   plaintext values (`alice`, `secret`, `hunter2`) so a
   future contributor adding structured logging that
   doubles the audit row trips the test.

The invariant's load-bearing nature means a fuzz test
gating log redaction is in the follow-up queue (issue
queue Q3) — the substring check is the seed.

## Consequences

- The dashboard UI is a follow-up. The CLI is the v1
  surface (issue #477 / C4) — the dashboard renders
  `app.public_auth.mode` + a "rotate" button that
  surfaces a modal with `basic_user` + `basic_pass`
  inputs (scope decision: dashboard UI is out of scope
  for issue #477 per the issue body).
- mTLS is explicitly deferred. A future mode='mtls'
  bumps the CHECK + the closed-enum constants AND
  requires the gatewayd chain to consult the client
  cert chain — out of scope here.
- The cache invalidation depends on
  `cmd/gatewayd-internal` subscribing to
  `db.NotifyKeyChanged`. The first gatewayd consumer of
  this channel (issue #477 is the first) makes
  cross-channel-subscription ordering a load-bearing
  invariant: the gatewayd reconnect loop must repaint
  the cache on the inner-cancel boundary so an
  out-of-order notify doesn't drop.
- secretbox key rotation requires a gatewayd-internal
  restart. Acceptable because webhook secret rotation
  also requires it (ADR-076); the operator workflow is
  the same.
- The `apps:read` scope misuse surface is mitigated by
  dashboard warning copy at first call. A future
  issue adds an SDK UI warning on
  `apps:read`-bearer-locked-app access.
- Re-redaction invariant requires the substring test
  to keep up with new field additions. A future
  reviewer adding any new field on the audit row MUST
  extend the substring list with a fresh plaintext
  fixture so the invariant stays pinned.
- **Invalid-mode fail-closed posture.** The gateway validates
  `public_auth_mode` before either customer-auth gate and before
  wake admission. Empty or unrecognised values return `503` with
  the stable code `public_auth_config_invalid`; the invalid value
  is omitted from the response, audit row, metric labels, and
  structured log fields. A bounded
  `gateway_public_auth_config_errors_total{reason}` counter and
  `instances.public_auth_config_invalid` audit row give operators
  visibility without turning database drift into an anonymous
  access path. Valid `open`, `bearer`, `basic`, `ip_allowlist`, and
  `internal_only` modes retain their existing behavior.

## Verification

### Code paths

- `migrations/00153_apps_public_auth.sql` + `_test.go`
  pin the schema (replay safety, down-up, CHECK
  rejection of unknown values).
- `pkg/api/public_auth.go` pins the constants
  (`AppPublicAuthMode*` + bounded byte caps).
- `pkg/api/dto.go` pins the DTO shape +
  `PublicAuthBlock.Validate()`.
- `cmd/apid/handlers_ext.go::validateUpdateApp` pins
  closed-enum validation + plan gate ordering
  (422 supersedes 402 so a Free customer PATCHing
  `mode='weird'` sees the shape error, not the plan
  error).
- `cmd/apid/handlers_ext.go::updateApp` pins the
  secretbox seal step + audit emission + Set-bit
  convention (a partial-PATCH like RAM-only never
  touches the public_auth columns).
- `pkg/gateway/handler.go::enforcePublicAuth` pins
  the runtime gate (bearer re-uses require_authn;
  basic uses secretbox unseal + constant-time
  compare; open passes through).
- `pkg/gateway/public_auth_cache.go` pins the 60s
  TTL + per-key-hash invalidation.
- `cmd/gatewayd-internal/public_auth_unsealer.go`
  pins the unseal-at-boot strategy with namespace
  verification (a namespace drift surfaces as a
  fail-closed decryption error at gatewayd boot).
- `pkg/state/memstore.go::UpdateApp` +
  `pkg/state/pgstore.go::UpdateApp` pin the Set-bit
  semantics + the clear-on-mode-flip invariant
  (a future pgstore migration touching
  public_auth_basic can't quietly disagree with the
  memstore).
- `cmd/apid/handlers_public_auth_test.go` pins the
  plan gates, the basic-creds round-trip, the stale-
  secret invariant on open flip, the audit emission
  with redaction.
- `cmd/e2e/public_auth_e2e_test.go` pins the state
  shape across both backends.

### CI gates

- `make test` covers unit tests (api, gateway, state).
- `make migration-test` covers migration slot 00151.
- `make e2e-build && make e2e` covers the e2e
  shape tests.
- `make spec-sync && make sdk-gen` cover OpenAPI
  parity + SDK regen.
- `make lint` covers golangci-lint (gofmt, goconst,
  errorlint).

### Manual operator workflow

1. `gregale app <slug> --public-auth=bearer` on a
   Hobby+ app — see 200.
2. `curl <host>` without `Authorization` → 401 with
   `WWW-Authenticate: Bearer realm="apps"`.
3. `curl -H "Authorization: Bearer fp_live_…"
   <host>` → 200.
4. Revoke the API key in the dashboard → next
   request → 401 (within the cache TTL).
