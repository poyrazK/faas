# ADR-20261109180000: Restore budget for bound apps, session Postgres bindings and binding-gap fixes

## Status
Accepted. Amends the lifecycle hook caps of spec §4.3 (`after_restore`
`timeout_ms` ≤ 2000) for apps with a managed binding, the managed Postgres
credential modes of ADR-462 and ADR-1190, and the plan secret quota of
`pkg/api/limits.go` (`MaxSecretsPerApp`). Consistent with ADR-005 (cold boot
must always work), ADR-351 (checkpoint drain) and ADR-1141 (workload identity).

## Context

A read-only audit of bindings on 2026-11-09 (production reads only) found:

1. **Restore misses its deadline on bound apps.** Ten production wakes of
   apps bound to a managed Postgres database fell back to cold boot with
   `after_restore` ack=13 (deadline). The shipped Node and Python starters ran
   a readiness transaction inside `after_restore`. The managed databases are
   Neon with scale-to-zero, about 36 ms RTT from compute, and a suspended
   endpoint takes longer than the 2 s hook cap to answer. Every such wake
   became a cold boot, so the snapshot bought nothing.
2. **No serving credential keeps session state.** `read_write` URLs go
   through a transaction pooler. `docs/postgres.md` pointed customers who need
   LISTEN/NOTIFY, session advisory locks, `SET` or SQL `PREPARE` at the
   `migration` binding, which only release tasks receive.
3. **Managed binding values used the customer's secret quota.** An object
   storage compute binding writes six managed secrets. On Free (10 secrets)
   two bucket bindings plus one Postgres binding exhausted the quota, and a
   binding create failed with a quota error that named no customer secret.
4. **Binding gaps.**
   - Capabilities advertised `queue-pull-leases` and `outbound-integrations`
     while workload identity was off in production. The vmmd relay closed
     connections silently when no signer was configured.
   - `gregale bindings check` compared the server's `generated_at` with the
     client clock, so a skewed laptop reported `runtime_unknown`.
   - Pull-queue consumers and Data API apps could never pass the gate.
   - Deleting an app left its queue bindings live.
   - Rotating an object storage binding on an app with resident instances
     and no live deployment finalized without refreshing those instances.

## Decision

1. **Restore stays off the network.** The shipped starters, and the guidance
   in `docs/postgres-pool-runtime.md`, rebuild the pool lazily in
   `after_restore` and run no SQL there. The first query connects and reports
   `database_unavailable` on its own. A failed secret reload or drain still
   fails the hook, so the platform cold-boots as before (ADR-005).
2. **Bound apps may take 4 s.** `after_restore.timeout_ms` may be up to
   `AfterRestoreHookBoundMaxTimeoutMS` (4000) when the app has a managed
   Postgres or object storage binding that is not being deleted. Other apps,
   and every `before_checkpoint`, keep the 2000 ms cap. apid enforces this
   wherever the hook can be set: app create and update, and source-ref and
   source-deployment lifecycles. Without such a binding the request gets a
   422 naming the limit. The host's 5 s resume deadline still bounds the whole
   resume.
3. **New `session` credential mode.** A `session` binding has `read_write`
   privileges under its own role and marker, and always uses the direct
   endpoint. There is no pooler fallback: without a direct endpoint, issuing
   fails with `ErrUnsupported`. Serving instances receive it like
   `read_write`. A migration widens both access CHECKs (added under new names
   NOT VALID, then validated) and the runtime-freshness function. Neon and
   Firecracker advertise it, and the CLI, manifest, OpenAPI and SDKs accept
   it. Connection budgets count it as an ordinary binding.
4. **Managed binding values do not count against `MaxSecretsPerApp`.** Rows
   owned by a managed Postgres binding or an object storage credential are
   excluded from the count. The quota limits what customers store.
5. **Binding gaps.**
   - `queue-pull-leases` and `outbound-integrations` are available only when
     apid has a workload verifier.
   - vmmd answers relay requests without a signer with
     `503 outbound_relay_unavailable` (RFC 7807) and a throttled warning.
   - bindingcheck evaluates at `max(client clock, inventory generated_at)`.
     It skips pull-queue consumers with a warning (`pull_consumer_external`).
     It skips Data API-only apps (`data_api_runtime`). `--require-bindings`
     turns "no bindings" into a blocker.
   - App soft delete retires the app's queue bindings.
   - Object storage rotation refreshes resident instances even without a
     live deployment.
   - A secret-key collision returns `object_storage_binding_secret_conflict`
     and suggests `--prefix`.

## Consequences

- Wakes of bound apps restore from snapshot instead of cold-booting. The cost
  moves to the first request, which waits for the database as any first
  connection would. Readiness probes (`/healthz`) still check the schema.
- Apps that copied the old starter keep validating in `after_restore`. They
  can raise the hook to 4 s, or adopt the lazy pattern.
- `session` bindings hold one server connection per pool slot per instance.
  The docs say to keep those pools small. Provider connection budgets still
  apply.
- Downgrading past the migration fails while any `session` binding exists,
  as the earlier access-mode migrations do.
- Free accounts can bind more buckets. Managed secret rows remain bounded by
  the binding limits themselves.
