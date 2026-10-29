# ADR: The traffic binding guard locks and checks only the hostnames a write changes

Date: 2026-10-27
Status: Accepted (owner, 2026-10-27)
Amends: [ADR-570](570-traffic-enforcement-guarantees.md) (effective request policy: the global-route session lock and the global route analysis; the binding-transition follow-ups)
Related: [ADR 20261027085556](20261027085556-public-routing-admits-from-refreshed-view.md) (CP Postgres off the request path), Hunt 14 H14-78 (#5612)

## Context

ADR-570 refuses any write that would push a hostname's runtime policy past its
bounds (50,020 rules, 64 MiB of projection, the automaton ceilings) or grow a
legacy overload. A write that changes which app or account a hostname belongs
to is a binding write, and the binding guard (`beginTrafficBinding`,
`pkg/state/traffic_tenant_binding.go`) checks it. Binding writers include
deployment create and status changes, app create, restore and visibility,
aliases, custom domains, tenant surfaces and hostnames, environments, GitOps,
project releases, preview sets, rollbacks and clones: about 45 call sites
through `beginAccountAppTrafficMutation`, `beginAppTrafficMutation`,
`beginDeploymentTrafficMutation` and `beginTrafficTenantBinding`. Custom-domain
removal and route-rule writes take the same lock.

Every binding write does this:

1. It reads every binding claim in the fleet as one JSON document: all custom
   domains, tenant hostnames, aliases and primary app URLs. It reads it three
   times: before locking, after locking and at commit.
2. It adds every account with an enabled route rule to its owner set
   (`ReadTrafficGlobalRouteAccounts`), whether or not those routes can match
   the hostnames being changed.
3. It takes one exclusive advisory lock, `gregale.traffic.global-routes.v1`,
   plus the account lock of every owner.
4. It reads the route-only analysis of the whole fleet twice, and each owner's
   full host analysis twice.

What that costs:

- **Cost grows with the fleet, not the write.** In production, creating a
  deployment of an app with no edge rules crossed the 2 s analysis bound twice
  in 48 hours (H14-78: 2002 and 2003 ms, an account with about 100 apps,
  during a control-plane rollout). ADR-570 says the refusal is correct. The
  work behind it is the problem.
- **One binding write at a time, fleet-wide.** Deploys from unrelated accounts
  queue on the global lock and retry every 25 ms.
- **Fleet size becomes a hard limit.** Past 100,000 claims or route-owning
  accounts, or 64 MiB of claim metadata (`TrafficPolicyMaxAnalysisInputs`,
  `TrafficPolicyMaxAnalysisMetadataBytes`), every guarded write fails,
  deploys included. Those ceilings exist to bound one hostname's runtime
  projection. Applying them to the whole fleet makes growth an outage.
- **No timing by phase.** We can't tell which of the four steps dominates.

The guard must also protect against cross-account effects. They come from only
two places:

- **A binding transition changes who owns a hostname.** The old and new
  owners' rules change at that hostname.
- **Route rules match unclaimed hostnames.** A route's `match_host` pattern
  (`*`, `*.example.com`, `promo-*.<apps domain>`) applies to unclaimed,
  substitutable hostnames, whichever account owns the route (ADR-570's global
  route discovery). A hostname that becomes unclaimed gains every route whose
  pattern matches it. A hostname that becomes claimed loses them. Immutable
  deployment, alias and named-environment URLs are never substituted, so
  routes never apply to them.

Neither effect reaches a hostname whose claim did not change. A binding write
only has to check the hostnames it changes, against the rules that can match
them.

## Decision

1. **Measure first.** apid records
   `traffic_policy_analysis_seconds{writer,phase}` with phases `discover`,
   `lock_wait`, `before`, `validate`, and the same phases as span attributes.
   This lands before the rest, so the change is judged on production numbers.
   Refusals keep their existing 422 and codes.

2. **A write declares the hostnames it may change, and locks only those.**
   - **Scope:**
     - An app writer covers the app's bindings before the write (primary URL,
       aliases, custom domains, environment and revision URLs) plus any
       hostname the write names.
     - An account writer covers the account's bindings.
     - A tenant writer covers the requested hostnames and its surface's
       hostnames.

     Owner-scoped, indexed queries read the scope, not the fleet document.
   - **Commit check:** at commit, the write compares the scope's claims before
     and after. If a claim outside the locked scope changed, the write rolls
     back and retries with the larger scope, as the owner-set retry does
     today.
   - **Locks:** hierarchical advisory locks keyed by DNS name. They are taken
     on the pinned direct connection before the repeatable-read transaction
     starts, the same ordering rule as today.
     - A hostname takes an exclusive lock on its name and shared locks on each
       ancestor name.
     - A wildcard domain `*.s`, or a route pattern whose literal suffix is
       `s`, takes an exclusive lock on `s` and shared locks on its ancestors.
     - A route pattern with no literal suffix (`*`) takes the root
       exclusively.

     Keys are acquired root first, then by name, with try-lock and
     release-all-and-retry as today. Two writes conflict only when one's name
     equals the other's or is its ancestor. A hash collision can only
     over-serialize.
   - **The root is today's `gregale.traffic.global-routes.v1` key.** New
     writers take it shared. Root-scope route writes take it exclusively.
     Release N-1 takes it exclusively for every write, so N-1 and N writers
     still exclude each other while both releases run. Once N-1 is gone, N
     writers run concurrently.
   - **Account locks:** the writer's account, and every account that owns a
     claim overlapping a changed hostname, before or after. Route-owning
     accounts no longer join just for having routes: their route writes take
     the name locks above, and those already conflict.

3. **Only the hostnames whose claim changes are analysed.**
   - D is the set of hostnames, and wildcard languages, whose owner, target,
     eligibility or environment differs between before and after.
   - **D is empty** for deployment create, status changes that don't revive an
     alias, and most writes without rules. Only the writer account's owned
     before/after check runs, as for ordinary non-binding writes today.
   - **D has only hostnames routes never match** (revision, alias and
     environment URLs). Only the overlapping owners' owned checks run. There
     is no route analysis.
   - **Otherwise**, the existing before/after automaton runs over candidate
     inputs only:
     - owned rules of the overlapping owners;
     - route rules whose pattern can match D.

     A new indexed column, `edge_rules.match_host_scope`, finds the route
     candidates. It holds the lower-cased literal suffix described above, or
     `''` for `*`, and is matched against the names in D and their ancestors.
     `''` routes are always candidates.
   - **Unchanged:** semantics, ceilings, legacy-overload repair, error codes
     and the 2 s phase bound. The ceilings now apply to one write's
     candidates, which is what they bound at runtime.

4. **Route-rule writes are scoped the same way.** A route write locks its
   pattern's scope as in decision 2. It analyses the candidate routes and
   claims inside that scope, not the whole fleet's route graph. A `*` route
   still covers the whole fleet, because it really does apply everywhere.

5. **Equivalence is proven against the current guard.** MemStore applies the
   same scoping under its mutex. A property test builds random fleets (claims,
   wildcard domains, route patterns including `*`, legacy overloads) and
   random writes. The scoped verdict must equal today's global verdict, in
   both MemStore and PgStore. The global path stays in the test suite as the
   oracle. A second test lists every binding writer and checks that it
   declares a scope.

6. **Rollout.**
   1. Phase metrics (decision 1).
   2. An expand migration adds and backfills `match_host_scope`, with an
      index for enabled routes. A trigger fills it on every write, including
      the previous release's. `@app` rules get NULL, because they cover only
      their own app's hosts. Nothing reads it yet.
   3. The scoped guard ships behind apid's `FAAS_TRAFFIC_SCOPED_BINDING_GUARD`,
      off by default, with the equivalence tests and a Postgres benchmark at
      10,000 apps and 1,000 route-owning accounts. Target: deploy create p99
      under 250 ms. The claim lookup indexes, by name and by suffix, ship
      with the queries that read them. The first cut covers app and tenant
      binding writers in apid. Custom-domain publication and removal, route
      writes and other daemons keep the fleet-wide guard until they get
      their own scoped paths.
   4. The switch is turned on in staging, then in production after a release
      with no refusal or `lock_wait` regression. It is removed one release
      after that, together with the global path, except as the test oracle.

## Consequences

- Deployment create and other binding writes without rules stop depending on
  fleet size. Deploys from unrelated accounts stop serializing.
- Fleet growth no longer fails every guarded write. A hostname's own
  projection still can, as ADR-570 intends.
- A `*` route write still serializes with every binding write and analyses
  every route in the fleet.
- A write takes more lock keys: name depth times hostnames, usually under 20.
- Every route write maintains the new `edge_rules` column. The change is in
  `pkg/state`, so every daemon rebuilds.
- **Risk:** a writer whose declared scope misses a hostname it changes. The
  commit check catches it and retries, and the writer-list test makes a new
  writer declare a scope.

## Alternatives considered

- **Raise the 2 s bound.** It hides the cost. The fleet-wide serialization and
  the fleet-size ceilings stay.
- **Drop the global check and rely on the runtime per-host bound.** The
  overload would surface as a 503 on someone's hostname instead of a refused
  write. ADR-570 requires the write-time refusal.
- **Verify after commit, asynchronously.** It needs a repair path for an
  overload that has already been committed. Rejected for the same reason.
- **Materialize per-hostname aggregates.** Correct and fast, but it adds a
  large table that every rule writer and binding writer must maintain. Worth
  revisiting if candidate analysis is still too slow after phase 3.
