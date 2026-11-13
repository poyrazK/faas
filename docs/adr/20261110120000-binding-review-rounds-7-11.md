# ADR-20261110120000: Session qualification, binding caps and binding review fixes

## Status
Accepted. Amends ADR-20261109180000 (session bindings, managed values outside
the secret quota, binding capability gates), the managed Postgres
qualification contract (ADR-462, ADR-650, ADR-1190), the environment
workload qualification of ADR-20261019072523 and the plan limits of
`pkg/api/limits.go`. Consistent with ADR-005, ADR-1141 and ADR-20261028103115
(production provisioning is an operator pin, not an artifact).

## Context

Five read-only review rounds after ADR-20261109180000 merged (2026-11-10,
main 8e174a4d33) found these problems in bindings:

1. **`session` shipped without qualification.** Neon and Firecracker
   advertised `session` unconditionally. Staging's artifact check compares the
   advertised modes with the qualified report, so every pre-session artifact
   failed `credential_capabilities_mismatch` and closed staging provisioning.
   A report could not hold more than five modes, and no probe proved session
   behaviour. Production, which follows an operator pin, offered it unproven.
2. **First deploy refused a declared database.** The source deploy checked
   the `after_restore` budget before `gregale.yaml` `databases:` were bound,
   so a first deploy with a declared database and a 4 s hook returned 422.
3. **Environment qualification could not pass for several modes.** The
   receipt CHECK and `environment_binding_postgres_deadline` accepted only
   `read_write`, `read_only` and `migration`, so an app with a `session`,
   `data_api` or `auth` binding never qualified.
4. **`session` logins had no connection limit.** They hold server connections
   directly, and one app at Scale concurrency could exhaust a compute.
5. **`gregale postgres connect --access`** did not accept `session`.
6. **Clone quota off by one.** Environment clones refused an app exactly at
   its customer-secret cap (`>=`), or counted the managed rows they publish,
   while a direct bind refuses only an app already over it.
7. **Pull leases advertised as unavailable.** The workload-identity gate
   added in ADR-20261109180000 also hid `queue-pull-leases`, but leases accept
   API keys with `queues:consume`, which work without workload identity.
8. **Rotations stuck pending.** A rotation started while instances were
   resident but no deployment was live queued a refresh that schedd failed
   with "no resident live deployment" on every delivery; the rotation stayed
   pending until a deployment went live or the customer retried.
9. **PostgreSQL bindings uncapped.** With managed values out of the secret
   quota, nothing bounded the bindings one app holds, and each one issues a
   provider role.
10. **Binding env keys looser than the guest.** Keys starting with `_` and
    platform identity keys were accepted, then dropped by guest-init; `PORT`,
    `GREGALE_*` and `FAAS_*` would shadow platform settings.
11. **`gregale.yaml` database entries could not change.** Changing the
    database or access of an entry made every later deploy fail with a bare
    409 `managed_postgres_conflict`.
12. **Release-pinned apps accepted bindings that could never deliver.** The
    release-secret trigger refuses app-secret rows in an environment whose
    active release pins secrets, so a Postgres binding retried hourly and never
    became ready, and a bucket binding failed with a generic 409.

## Decision

1. **`session` is a rollout capability.** A backend advertises it only with
   `session_enabled: true`, like `data_api_enabled` and `auth_enabled`; the
   default is off, so existing staging artifacts keep verifying and
   production does not offer it until an operator enables it. Qualification
   adds `session_credentials_probe`: the material names a direct endpoint, a
   session advisory lock outlives the statement that took it, a LISTEN
   receives a NOTIFY, re-issue returns the same password and revocation ends
   the login. A report may list every known mode (six).
2. **The deploy-time budget check counts declared bindings.** A
   `gregale.yaml` that declares a database or bucket for the app satisfies the
   bound-app hook budget, since the deploy binds it.
3. **Qualification covers serving modes.** `session` receipts are checked
   with the `read_write` ACL probe (its privilege class) and gate the deadline
   like `read_write`. `data_api` and `auth` logins carry their own qualified
   contracts and no longer block a graph's deadline.
4. **`session` roles carry `CONNECTION LIMIT 42`**
   (`api.SessionBindingConnectionLimit`: 2 connections × Scale concurrency
   20 + 1 rollout). Session bindings are for session features; bulk query
   traffic belongs on a pooled `read_write` binding. The limit is set by the
   provider, not stored as `connection_limit`, so the capacity-mirror
   trigger's rules are unchanged.
5. `gregale postgres connect --access` accepts `session`.
6. **Clone quota checks match a direct bind:** refuse only an app already
   over `SecretCountMax`; managed values never count.
7. Pull leases are advertised by their platform flag alone; only the
   outbound relay keeps the workload-identity gate.
8. **schedd completes or defers instead of failing.** With no live
   deployment and nothing resident, a runtime refresh is complete and pending
   rotations for its wake finish. With resident instances of a deployment
   that is not live yet, the refresh is deferred and retried.
9. **`PostgresBindingsPerAppMax`** (Free 8, Hobby 25, Pro 50, Scale 100, the
   bound `SecretCountMax` used to impose) caps live bindings per app,
   enforced in the reservation transaction and in clone publication, and
   refused with 403 `managed_postgres_binding_limit` carrying limit and
   observed values.
10. **New binding env keys** must start with a letter and may not be a
    platform-provided key (`api.PlatformProvidedEnvKey`). Existing bindings
    are unaffected.
11. **A changed `gregale.yaml` entry gets 409 `managed_postgres_binding_changed`**
    naming the bound database and access and the requested ones. A deploy
    still never replaces a binding, because that would revoke the credential
    the serving release uses; removing an entry still leaves its binding.
12. **Binding creation checks release pinning up front** and refuses with
    409 `project_release_secrets_pinned`, as customer secret writes already
    do.

## Consequences

- An operator enabling `session_enabled` must re-run qualification; the new
  report carries `session_credentials` evidence. Until then `session` is not
  offered and existing `session` bindings keep their credentials but cannot
  be re-issued.
- An app at Scale with pools larger than two connections per instance on a
  `session` binding sees "too many connections for role" under load instead
  of exhausting the database.
- Apps that already hold more bindings than the new cap keep them; they
  cannot add another until under it.
- Not changed: removing a `gregale.yaml` database entry still leaves the
  binding in place (data and credentials are never deleted implicitly).
