# ADR: Scoped binding writes read only their scope, and change capture proves it

Date: 2026-10-29
Status: Proposed
Amends: [ADR-20261027163626](20261027163626-traffic-binding-guard-per-host.md) (decisions 2 and 3: how a scoped write reads claims and reservations, and how its commit check sees an undeclared change)
Related: [ADR-570](570-traffic-enforcement-guarantees.md)

## Context

ADR-20261027163626 scoped the binding guard's locks and its route candidates.
Every scoped binding write still reads the whole fleet twice over:

- **Claims:** `ReadTrafficBindingClaims` reads every custom domain, tenant
  hostname and alias, three times per write (discover, before, at commit).
- **Reservations:** the global view (`ReadTrafficHostAnalysis` with no
  account) reads the scoped route candidates. It also reads every app's
  primary URL, every custom domain and every tenant hostname as reservations.
  It does so twice per write (before, at commit), sorted.

The rollout benchmark (`TestPgTrafficGuardFleetScale`, #5902) seeds 10,000
apps, 2,000 verified domains and 1,000 route-owning accounts:

| Deploy create | p50 | p99 | Refused |
|---|---|---|---|
| Fleet-wide guard | 2.08 s | 2.31 s | 20 of 20, on the 2 s bound |
| Scoped guard | 242 ms | 434 ms | 0 of 200 |

A Postgres statement profile of the scoped run splits the time:
- **About 60% is the global view.** It runs about 2.4 times per deploy and
  is dominated by the fleet-wide reservations.
- **About 40% is the claims read.** It runs 3 times per deploy.
- **Go CPU is negligible.**

The profile ran at load average 42 on a shared machine, so only the
proportions are meaningful. The target is deploy create p99 under 250 ms.
The rest of the gap is these fleet-wide reads.

They are fleet-wide for one reason: the commit check. A scoped write must
refuse a change to a hostname it did not declare (`binding_scope`). Today it
finds such changes by diffing every claim and reservation in the fleet before
and after the write. A read limited to the scope cannot see a change outside
it.

## Decision

1. **Change capture records what a write touched.** Row triggers on the
   claim and reservation sources append to a transaction-local set:
   - `custom_domains`: the domain and its old domain, on insert, update or
     delete;
   - `tenant_hostnames`: the hostname and its old hostname;
   - `deployment_aliases`: the alias's app;
   - `apps`, only on updates of `slug`, `status`, `visibility`,
     `account_id` or `deleted_at`: the app, with its old and new slug;
   - `tenant_surfaces`, on status, app or account changes, and
     `platform_tenants`, on status changes: the surface or tenant.

   The triggers record identities only. At commit the scoped guard expands
   them to hostnames: an app's primary URL, domains, tenant hostnames and
   aliases; a surface's or tenant's hostnames. The triggers run for every
   writer, including the previous release's and those outside apid, and cost
   one row append per changed row. They don't fire on the hot `apps` columns.

2. **The commit check uses the capture, not a fleet diff.** A scoped write
   expands its captured set to hostnames, before and after. It refuses
   (`binding_scope`) when one lies outside its declared scope, under the same
   rule as today: a newly added exact hostname needs no declaration. The
   fleet-wide guard keeps today's diff and also reports captured misses, so
   the audit (`traffic_binding_scope_misses_total`) compares both while the
   switch is off.

3. **Scoped reads.** A scoped write reads only the claims and reservations
   that can affect its scope:
   - **by name:** those of its declared hosts and their ancestor wildcard
     domains;
   - **by suffix:** those inside its declared subtrees;
   - those of its account and of the overlapping owners it locks.

   New indexes on reversed labels serve the suffix lookups. When the scope's
   route candidates include an empty-scope route (`*`), the write keeps
   today's fleet-wide reservations read, because such a route can match every
   unclaimed hostname.

4. **No change, no global check.** D is the set of captured hostnames whose
   claim or reservation differs between before and after (decision 3 of
   ADR-20261027163626). When D is empty, which is the case for deployment
   create and most status changes, the global route check is skipped.
   Overlapping owners' owned checks still run. The global before view is read
   at begin only while D could be non-empty, which today means always. A
   later step can read it lazily from an exported snapshot.

5. **Unchanged:**
   - locks, verdicts, ceilings, error codes, the 2 s phase bound and the
     fleet-wide path;
   - the equivalence tests, which keep running every binding-writer suite
     under both guards.

## Rollout

1. Migration: the capture triggers and the reversed-label indexes, an
   expand-only change. The previous release fires the triggers and ignores
   them.
2. The audit compares the captured set with the fleet diff while the switch
   is off. A disagreement is a capture gap and blocks step 3.
3. Behind `FAAS_TRAFFIC_SCOPED_BINDING_GUARD`, scoped writes switch to the
   capture-based check, the scoped reads and the D-empty skip. The benchmark
   gates it: deploy create p99 under 250 ms at the benchmark's fleet size.

## Consequences

- A scoped binding write's reads stop growing with the fleet, except when an
  empty-scope route is among its candidates.
- Writes to the claim and reservation sources pay a trigger per changed row.
  `apps` triggers fire only on rare columns.
- A capture gap would let an undeclared change through unrefused.
  - The audit of step 2 checks the capture against the fleet diff before the
    switch relies on it.
  - A test lists every claim and reservation source in the analysis queries
    and requires a capture trigger on each.

## Alternatives considered

- **Keep fleet-wide reads and cache them.** A cached fleet document in apid
  would need invalidation from every claim writer, including other daemons,
  and the commit check would still have to see the latest state.
- **Materialize per-hostname claims.** A table keyed by hostname, maintained
  by triggers, would make every read indexed. It costs more to write and
  duplicates the claim sources. Capture plus indexed reads reaches the target
  with less.
- **Drop the commit check once the audit reads zero.** The check is what
  makes a writer bug fail closed. Without it, an undeclared change would
  skip the analysis it needed.
