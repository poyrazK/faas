# ADR 20261023214253 · Operators need MFA, and a session to read customer data

- **Status:** proposed (2026-10-24)
- **Date:** 2026-10-24
- **Related:** ADR-029 (operator allowlist), ADR-046 (MFA gate), ADR-077
  (step-up), ADR-091 (operator observability backend), ADR-982 (apid
  packages)

## Context

A platform operator is any account whose email is in `FAAS_ADMIN_EMAILS`.
Provider mutations already go through `requireAdminMutationPolicy`: MFA, the
allowlist, a five-minute TOTP step-up that refuses bearer keys, and a
same-origin check. Provider reads do not.

- **MFA is opt-in for operators.** Login stamps a session `mfa_pending` only
  when the account has an authenticator or `mfa_required` is set. An operator
  with neither gets a full session from a password.
- **Bearer keys skip MFA.** `RequireMFA` passes every API key. An operator's
  admin key, such as the `FAAS_ADMIN_TOKEN` that `gregalectl obs` sends,
  reads every `/v1/admin/obs/*` route: tenant 360, the audit log, traces, the
  event stream. Only `GET /v1/admin/status/incidents` requires a session.
- **A password mints a bearer key.** `POST /v1/auth/login` returns a new
  admin-scope key for any account whose password matches, enrolled or not.

So a leaked operator password, or one leaked operator key, reads every
tenant's data with no second factor.

## Decision

1. **MFA is mandatory for operators.** Login stamps `mfa_pending` on every
   session for an operator account (password, magic link, GitHub, Google),
   as if `mfa_required` were set. An operator without an authenticator is
   confined to enrollment until they confirm one. The rule is computed from
   the allowlist at login; nothing is written to `accounts`.
2. **Operator sessions must belong to an enrolled operator.** Both read
   policies below check the session's account on each request and refuse an
   operator with no authenticator with `403 mfa_required`. Login already
   prevents this case, but a session issued before release N, or by a
   release N-1 replica during the rolling deploy, would otherwise stay valid
   for up to 7 days. Mutations keep `requireAdminMutationPolicy`: its
   step-up already proves a TOTP code from the last five minutes.
3. **A password does not mint an operator key.** `POST /v1/auth/login` and
   `POST /v1/auth/signup` refuse an operator account with `403 mfa_required`
   after the password matches. Operators mint keys with `POST /v1/keys` from
   an MFA-completed session, which requires a step-up.
4. **Two read policies.** Every `GET /v1/admin/obs/*` route uses one of two
   helpers in `pkg/apid/apidserver/auth_facade.go`. Both require MFA on a
   session, the admin scope and the allowlist.
   - `requireOperatorSessionRead` refuses bearer keys
     (`403 forbidden`, "operator session required"). It covers every read
     that returns customer data: account or app identifiers, emails, events,
     audit rows, traces or client IPs.
   - `requireOperatorRead` accepts an operator's bearer key. It covers fleet
     and pipeline telemetry that names no tenant. Probes and scripts keep
     using a key for these.

| Session only (customer data) | Bearer-readable (fleet telemetry) |
|---|---|
| `obs/overview` (top rate-limited account ids) | `obs/capacity` |
| `obs/tenants`, `obs/tenants/{id}`, `obs/tenants/{id}/360`, `obs/tenants/{id}/activity` | `obs/nodes` |
| `obs/apps/{id}` | `obs/nodes/{name}/heartbeats` |
| `obs/nodes/{name}/detail` (apps on the node) | `obs/nodes/wake-latency` |
| `obs/nodes/events` (app, deployment and instance changes) | `obs/builder-heartbeats` |
| `obs/anomalies` (per-account usage) | `obs/health` |
| `obs/rate-limits` (account ids, client IPs) | `obs/notification-health`, `obs/rule-health`, `obs/rule-coverage`, `obs/recording-coverage`, `obs/input-health` |
| `obs/audit-log/search`, `obs/events`, `obs/traces/{trace_id}` | `obs/collection-history` |
| `obs/incidents`, `obs/incidents/{kind}/{id}/evidence`, `obs/incidents/{kind}/{id}/context` | `obs/incidents/platform_alert/summary`, `obs/incidents/platform_alert/response-times` |
| `obs/incidents/platform_alert/recurrence` (series carry account and app ids) | `obs/incidents/platform_alert/{id}/triage-history` (no actor ids) |

   The same session-only policy covers the other provider reads that return
   tenant records and whose only clients already use the operator session:
   `/v1/admin/egress-flows`,
   `/v1/admin/managed-postgres/{usage,accounting}/{account_id}`,
   `/v1/admin/ops/jobs/runs[/{id}]`, `/v1/admin/ops/deployments[/{id}]`,
   `/v1/admin/accounts/{id}/flags` and `/v1/audit-log/all` (which had no MFA
   gate). Other `/v1/admin/*` reads (config, flags, billing catalog, data
   repairs, GitHub recovery queues, operator intents, rekey progress) keep
   their chains. A follow-up moves them onto `requireOperatorRead`.
5. **A route's tier is pinned by a test.** A table test in
   `operator_read_policy_test.go` lists every route above and asserts which
   tier it is in. A new provider read picks a helper at the route table.

### Migration across releases N-1 and N

Points 1 to 3 need no migration: automation uses keys, not sessions, and a
key minted before release N keeps working where keys are allowed.

Point 4 changes what an existing `FAAS_ADMIN_TOKEN` reaches. The release that
enforces it must not break operator tooling on a flag day, and apid N runs
beside apid N-1 during a deploy.

- **gregalectl.** The `gregalectl obs` commands prefer the operator session
  (`gregalectl auth login`, or `FAAS_OPERATOR_SESSION` /
  `FAAS_OPERATOR_SESSION_FILE`) and fall back to `--admin-token`. That change
  ships separately. An older gregalectl still works against apid N for the
  bearer-readable routes.
- **Grace switch.** apid reads `FAAS_OPERATOR_BEARER_READS`. Unset or `deny`
  (the default) enforces the session-only tier. `deprecated` lets an
  operator key through a session-only read and marks the response with
  `Deprecation: true` and a `Warning: 299` header. Each such read writes an
  `auth.operator_bearer_read_deprecated` audit row with the route pattern and
  key id, so an operator can find what still uses a key. Any other value
  logs a warning and enforces. The switch never relaxes MFA, the allowlist,
  `GET /v1/admin/status/incidents`, or points 1 to 3.
- **Sequence.**
  1. Release N ships with the switch unset. If automation outside the repo
     still reads customer data with a key, the operator sets
     `FAAS_OPERATOR_BEARER_READS=deprecated` on apid before the upgrade and
     moves that automation to a session or to a bearer-readable route.
  2. Once the audit rows stop, the operator removes the switch.
  3. Release N+1 removes the `deprecated` mode.
- **Operators enroll first.** Enrollment from a password-only session is
  trust on first use. Every operator enrolls an authenticator before release
  N reaches production. `gregalectl auth login` already requires one, so
  operators who use it are enrolled.

## Consequences

- A leaked operator password reaches only MFA enrollment. A leaked operator
  key reads fleet telemetry but no tenant's data. A key cannot be minted from
  the password.
- Scripts that read customer data must hold an operator session, which
  expires after 7 days and needs a TOTP code to renew.
  `deploy/scripts/adr093-hobby-audit.sh` reads `obs/tenants` with a key; it
  needs the grace switch or a session until it is changed.
- An operator who disables MFA loses operator reads until they enroll again.
- Operator mutations are unchanged.
- In-repo bearer clients of the session-only routes are the gregalectl obs
  commands and the ADR-093 script. The Operations console uses a session.
