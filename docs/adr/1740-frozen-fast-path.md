# ADR-1740: Frozen fast path — a thaw is a ledger check, the vmmd call beside one read, and one guarded update

- Status: Proposed. Built behind schedd's `frozen_fast_path` switch, off by
  default; it only matters once ADR-1151's `frozen_tier` is on. Amended by
  ADR-1741 (2026-10-16): the guarded thaw checks the app's runtime
  generation, which every change in section 3's table moves in the writer's
  own transaction, so the column guards, the stamps and the runtime-values
  read are gone. A ticket reads the generation first; `ThawFrozenInstance`
  locks only the generation row and keeps the proof and enrollment-expiry
  checks. The ticket also keeps the workload-retirement gate's verdict,
  which the generation covers, so a ticketed thaw is that one statement.
  Amended 2026-10-21 (see "Amendment: coordinated wakes"): a coordinated
  wake whose selection is the ticket's deployment and environment takes
  the fast path.
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
- the application-standards rule (ADR-595) admits the app, evaluated in Go
  as wake admission does, and the VM's admission captured no managed
  standards (those publish only with a receipt). Every app has an
  organization in production (a personal one at least), so the guard keeps
  the rule's answer valid by requiring the enrollment row unchanged rather
  than restating the rule in SQL;
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
2. vmmd `ThawInstance`, as today (healthcheck and `after_restore` included),
   and at the same time one read of the runtime values (values, secret intent
   and config stamp, sent together). Their config fingerprint must equal the
   ticket's, as ADR-1151's thaw requires.
3. One statement, `ThawFrozenInstance`: the FROZEN → RUNNING compare-and-set
   with every guard in its `WHERE` clause, and the transition event and the
   `instance_changed` notification in the same statement (wake step 4).

The read runs while vmmd resumes the VM, so a thaw waits for one round trip
beyond the vmmd call instead of about eighteen.

### 3. The guards

The statement rechecks, at the moment it commits, everything the ordinary
path would have read:

| ordinary path reads | guard in `ThawFrozenInstance` |
|---|---|
| instance row | `state = 'frozen'`, same wake id, node and deployment |
| config proof | proof row's config fingerprint equals the ticket's |
| app: active, cutover barrier | `apps.status = 'active'`, `managed_postgres_admission_cutover_id IS NULL`, RAM, CPU, concurrency, type and organization as in the ticket; row locked `FOR SHARE` |
| application-standards enrollment | the enrollment row's version (`xmin`) as read at freeze, and no expired exception; the database's publication guard trigger on `instances` still runs |
| account active, plan | status `active` or `past_due`, no abuse hold, same plan |
| live deployment, release | still live with traffic, not security-quarantined, still the selection of section 1; row locked `FOR SHARE` |
| runtime values and secret intent (config fingerprint) | read beside the vmmd call and compared in Go; then the app's runtime-config stamp rows (`app_runtime_config_changes` and the scope rows for `default` and the deployment's scope) must still hold the values recorded at freeze |

A fingerprint that differs, or a refused statement (which changes nothing),
sends the VM back the same way: schedd freezes it again through
vmmd, restores its frozen ledger charge, drops the ticket and continues with
the ordinary wake path, which may still thaw the VM through ADR-1151's path.
A VM that cannot be frozen again is parked.

### 4. Config freshness

The stamps alone cannot prove the config is unchanged. apid writes an
environment value and then stamps the change in a separate call before it
acknowledges the request (`handlers_env.go`), and some flows invalidate
snapshots at the existing stamp without writing a new one. A store-level
write that is not followed by a stamp leaves the stamps as they were. So the
fast path keeps ADR-1151's fingerprint check: it reads the runtime values
beside the vmmd call and compares their fingerprint with the ticket's. The
stamps in the guarded update cover only the time between that read and the
update; ADR-1151's thaw closes the same gap by re-reading the values inside
its publication transaction. A config edit that lands in those milliseconds
and stamps after the update is the one case the fast path serves with the
old config; runtime-config restart replaces such a VM as it does any VM
that was running when the edit landed.

## Alternatives considered

- Trust the stamps alone and skip the values read: one round trip instead of
  two. Rejected after the conformance test showed a value written without a
  stamp passes the guard; the read costs no latency because it runs beside
  vmmd.
- Compare row versions (`xmin`) of the app, account and deployment instead
  of named columns. Rejected: the deployment row also changes for scale
  stamps and backoff bookkeeping, so tickets would expire for no reason. The
  enrollment row is the exception: it changes only when standards change,
  and its rule is too large to restate in SQL.
- A `thawing` state claimed before vmmd is called, so a refusal needs no
  second freeze. Rejected: a migration and new state-machine edges to save a
  0.4 ms pause in a rare case.
- Wait for the schedd owner cache (roadmap step 6), which brings every wake
  to one write. The ticket is that cache's narrowest case and can be built
  now; the owner cache would replace it.

## Consequences

- A ticketed thaw makes two control-plane round trips, one of them hidden
  behind the vmmd call, instead of about eighteen in a row. The roadmap's
  `wake.thaw_breakdown` event shows it: `wake_reads_ms` and `thaw_reads_ms`
  fall to the in-memory work. The schedd owner cache (roadmap step 6) can
  remove the read by keeping the runtime values in memory.
- Measured on real Postgres (`TestPostgresTicketedThawStatements`): a
  ticketed thaw runs 4 statements in 2 sequential round trips, against 24
  statements in about 19 for ADR-1151's thaw. With ADR-1741's generation
  guard it runs one statement, `ThawFrozenInstance`.
- The guards restate the ordinary path's selection rule in SQL. Conformance
  tests on MemStore and Postgres refuse a thaw after each change the guards
  cover, and check that a deployment the basis selects is the one
  `ResolveProductionDeployment` returns, so a change to either side fails.
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

## Amendment: coordinated wakes (2026-10-21)

Section 2 kept the fast path away from any wake carrying a coordinated wake
selection. Every gateway wake carries one: the gateway calls schedd's
`EnsureWake`, which resolves the production deployment, its app settings,
account and scaling owner from fresh reads before it enters the wake
coordinator (ADR-098, ADR-590). With the frozen tier on in production-us
(rc.285, 2026-10-21), every thaw took ADR-1151's path:
`schedd_frozen_thaw_total{outcome="fast"}` and `fast_refused` stayed 0, and
`publish_ms` (the ordinary publication) was 63 ms of a 97 ms schedd thaw.

A selection names a deployment the same way the ticket's basis does: for a
production wake with no explicit deployment it comes from
`ResolveProductionDeployment`, which section 3's conformance test ties to
`FrozenThawBasis`. So:

- A coordinated wake takes the fast path when its selection is the
  ticket's app, deployment and admission environment
  (`runtimeEnvironmentAdmissionKey` of the selected scaling owner). Any
  other selection leaves the ticket in place and the ordinary path decides.
- Such a wake also defers the workload-retirement gate, as an
  uncoordinated production wake does; a step that decides from fresh reads
  runs it first.
- The guarded statement is unchanged: the selection being fresher than the
  ticket only means a change after the freeze has already moved the
  generation, and `ThawFrozenInstance` refuses it.

The fast path was dark without anyone seeing why, so it now reports itself:

- `schedd_frozen_fast_path_skipped_total{reason}` counts wakes of an app
  with a frozen VM that did not take the fast path: `deployment` (the wake
  named one), `scope`, `selection`, `no_ticket`, `capacity`.
- `schedd_frozen_thaw_tickets_total{outcome}` counts tickets at freeze:
  `issued`, `error` (a read failed), or why the VM thaws through the
  ordinary path (`mode`, `not_selected`, `operator_cold_boot`,
  `app_inactive`, `managed_standards`, `account`, `shape`, `config`).

