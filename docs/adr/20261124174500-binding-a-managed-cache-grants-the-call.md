# ADR-20261124174500: Binding a managed cache grants the call

- **Status:** accepted. Owner decision, 2026-11-24: "Bind grants the call".
- **Date:** 2026-11-24
- **Amends:** [ADR-20261030131528](20261030131528-project-bindings-name-project-services.md)
  (a project workload reaches a service outside its project only under the
  `account` policy) and [ADR-20261116192126](20261116192126-managed-redis-valkey-cache.md)
  (bindings write only the connection URL).
- **Decision:** `gregale redis bind CACHE --app APP` and Compose cache
  provisioning record a grant: APP may call CACHE. The service authorizer
  admits a caller that holds a grant to a target that is still a managed
  cache, under the `declared` and `project` policies, where it would
  otherwise refuse the call as undeclared. Nothing else changes.
  - **Storage.** `managed_valkey_cache_grants (cache_app_id, app_id,
    account_id, created_at)`, primary key on the pair, both ends
    `ON DELETE CASCADE` to `apps`. apid is the only writer.
  - **Reads.** `pkg/servicebinding` asks an optional
    `state.ManagedValkeyCacheGrantReader`. PgStore, MemStore and the
    gateway's pinned service policy snapshot implement it; the snapshot
    records each grant read in its input revision, so a pinned call sees a
    grant change like any other policy change. A store without grants grants
    nothing.
  - **A grant counts only while its target is a cache.** The read joins
    `managed_valkey_caches`, so `redis delete` (which removes the marker)
    ends every grant at once.
  - **Revocation.** `gregale redis unbind CACHE --app APP`
    (`DELETE /v1/redis/{name}/bindings/{app}`) deletes the grant and audits
    `redis.unbound`. It leaves the app's connection variable, which
    `gregale env unset` removes. Deleting either app also removes it.
  - **Scope.** The grant is per app id. A PR preview or scenario-test copy of
    the app holds none: previews and test runs keep their own rules.
    Target-side `x-gregale-allow-callers` still applies.
- **Why:** The 2026-11-24 voting-app agent eval: a Compose project's `vote`
  and `worker` services got `REDIS_URL` for a provisioned cache, and every
  connection was refused. A cache is an app outside every project, and
  ADR-20261030131528 refuses a project caller any target outside its project
  unless the caller uses `account`. About 40 of the agent's 85 commands went
  to this. Only `x-gregale-service-policy: account` worked, which opens the
  whole account to the caller. The binding is already the customer's
  explicit statement that this app uses this cache; it should carry the
  permission too.
- **Consequences:**
  - Project and declared-policy apps reach the caches bound to them, with no
    policy change. `account` callers are unchanged.
  - Apps bound before this release hold no grant. Running `gregale redis
    bind` again records it (the URL it writes is the same). Compose
    provisioning does not rebind a key that is already set, so a project
    applied before this release needs that one bind per consumer too.
  - The CLI's offline `service explain` reads configuration, not grants, and
    still reports a bound cache as undeclared for a project caller; the
    server explanation runs the gateway's authorizer and reports it allowed.
  - Release order: data (migration) before apid and gatewayd-internal. An
    N-1 gateway ignores grants (calls stay refused, as today); an N-1 apid
    writes none.
- **Rejected alternatives:**
  - **Write a service binding on the caller.** Project members' bindings are
    reconcile-owned and name only project services (ADR-20261030131528);
    a cache binding there would be dropped on the next apply or reopen the
    stale-binding hole that ADR closed.
  - **Make caches reachable by every app in the account.** Simple, but it
    drops the explicit per-app edge that the declared policy promises.
  - **Make the Compose cache a project member.** Caches are shared by name
    across projects and outlive a project's apply; membership would tie
    their lifecycle to one project.
