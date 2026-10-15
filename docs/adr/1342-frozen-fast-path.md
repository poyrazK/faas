# ADR-1342: Frozen fast path — a thaw is a ledger check, the vmmd call and one guarded update

- Status: Proposed. Built behind schedd's `frozen_fast_path` switch, off by
  default; it only matters once ADR-1151's `frozen_tier` is on.
- Date: 2026-10-15
- Amends: ADR-1151 decision 2, "Thaw path"
- Builds on: ADR-1151 (pause tiers), the scaling roadmap's wake rule (zero
  control-plane round trips per request, at most one per wake)

## Context

A frozen VM resumes in about 4 ms inside vmmd (ADR-906). Before vmmd is
called, schedd spends far longer re-deciding what it decided when it froze
the VM. A thaw on real Postgres (`TestPostgresThawStatements`, after the
read deduplication, pipelining and in-transaction bookkeeping of the
roadmap's wake steps 2 to 4) runs 24 statements in about 18 sequential round
trips:

| step | statements |
|---|---|
| wake phase 1 | app, enrollment, production release, live deployment, instance list |
| admission | account, cutover barrier, production release, live deployment, scaling state |
| thaw reads | instance list, node, config stamp, runtime values, secret intent, config proof |
| publication | begin, three locks, runtime values again, publish, proof and bookkeeping, commit |

Production measured about 80 ms of schedd time before the 4 ms thaw, on a
control plane that answers every one of those statements. Almost all of it
re-reads rows that did not change while the VM was frozen.

## Decision (proposed)

### 1. Record a thaw ticket when the VM freezes

When `FreezeIdle` freezes a VM, schedd keeps a ticket for it in memory: the
instance (id, node, wake id, deployment, network lease), the app as resolved
for its deployment, the account's plan and limits, the deployment, the
environment admission key, the node's admission limits and region, the
config fence captured in the instance's proof, the frozen charge, and the
guard inputs below. The freeze runs in the reaper, off the request path, so
the extra reads cost no request any latency.

A ticket is issued only when the thaw it allows is the one the ordinary path
would choose:

- the deployment is in the production routing lane (`production`,
  `default` or unscoped);
- either the app's active production release pins this deployment, or no
  release pins the app and this deployment is the app's only live deployment
  in that lane with traffic;
- the app has no organization, since application standards are evaluated in
  Go (ADR-595);
- the VM is a normal-mode instance without ephemeral secrets.

Any other frozen VM thaws through ADR-1151's path, unchanged. Tickets live
only in schedd's memory: a schedd restart loses them and every thaw takes
the ordinary path until the VMs freeze again.

### 2. Thaw from the ticket before any read

Wake phase 1, under the app lock and before its first read, takes the fast
path when the wake names no deployment, targets production, carries no
coordinated wake selection, the ledger still holds the ticketed VM's frozen
reservation, and the ledger holds no live reservation for the app (phase 1
would return that running instance instead). Then:

1. Capacity: the serving-capacity check and the ledger re-admission, from the
   ticket, in memory.
2. vmmd `ThawInstance`, as today (healthcheck and `after_restore` included).
3. One statement, `ThawFrozenInstance`: the FROZEN → RUNNING compare-and-set
   with every guard in its `WHERE` clause, and the transition event and the
   `instance_changed` notification in the same statement (wake step 4).

### 3. The guards

The statement rechecks, at the moment it commits, everything the ordinary
path would have read:

| ordinary path reads | guard in `ThawFrozenInstance` |
|---|---|
| instance row | `state = 'frozen'`, same wake id, node and deployment |
| config proof | proof row's config fingerprint equals the ticket's |
| app: active, cutover barrier | `apps.status = 'active'`, `managed_postgres_admission_cutover_id IS NULL`, RAM, CPU, concurrency and type as in the ticket, no organization; row locked `FOR SHARE` |
| account active, plan | status `active` or `past_due`, no abuse hold, same plan |
| live deployment, release | still live with traffic, not security-quarantined, still the selection of section 1; row locked `FOR SHARE` |
| runtime values and secret intent (config fingerprint) | the app's runtime-config stamp rows (`app_runtime_config_changes` and the scope rows for `default` and the deployment's scope) hold the values recorded at freeze |

A refused statement changes nothing. schedd then freezes the VM again through
vmmd, restores its frozen ledger charge, drops the ticket and continues with
the ordinary wake path, which may still thaw the VM through ADR-1151's path.
A VM that cannot be frozen again is parked.

### 4. Config freshness

The fast path does not recompute the config fingerprint; it compares the
change stamps instead. Every runtime-config writer stamps a change, and
snapshot validity already depends on those stamps (a stale snapshot would
keep a credential the customer replaced). Two backstops cover a write that
the stamps miss or a stamp that commits while the thaw statement runs: the
proof's fingerprint guard, and runtime-config restart, which destroys stale
frozen VMs and retires stale running ones, as it does for any VM that was
running when the edit landed. This is the one place the fast path is weaker
than ADR-1151's thaw, which recomputed the fingerprint: a thaw that races a
config edit can serve until runtime-config restart replaces it, as a running
VM does today.

## Alternatives considered

- Keep recomputing the fingerprint. It needs the runtime values, secret
  intent and stamp reads (one pipelined round trip) before the update: two
  round trips instead of one, for a guarantee running VMs do not get.
- Compare row versions (`xmin`) of the app, account and deployment instead
  of named columns. Rejected: the deployment row also changes for scale
  stamps and backoff bookkeeping, so tickets would expire for no reason.
- A `thawing` state claimed before vmmd is called, so a refusal needs no
  second freeze. Rejected: a migration and new state-machine edges to save a
  0.4 ms pause in a rare case.
- Wait for the schedd owner cache (roadmap step 6), which brings every wake
  to one write. The ticket is that cache's narrowest case and can be built
  now; the owner cache would replace it.

## Consequences

- A ticketed thaw makes one control-plane round trip. The roadmap's
  `wake.thaw_breakdown` event shows it: `wake_reads_ms` and `thaw_reads_ms`
  fall to the in-memory work.
- The guards duplicate the ordinary path's selection rules in SQL. A Postgres
  contract test checks every guard against the ordinary path's choice, so a
  change to either side fails the test.
- Turning `frozen_fast_path` off drops back to ADR-1151's thaw at once;
  tickets are memory only, so no data needs cleaning up.
- `schedd_frozen_thaw_total{outcome}` gains `fast` and `fast_refused`.

## Rollout

1. This ADR.
2. Store: `ThawFrozenInstance` and the freeze-time guard inputs, with
   conformance tests for MemStore and Postgres, one test per guard.
3. schedd: tickets at freeze, the phase 1 fast path, refusal handling,
   metrics, behind `frozen_fast_path`.
4. Turn it on with `frozen_tier` in the same canary, comparing
   `wake.thaw_breakdown` and thaw errors against the ordinary path.
