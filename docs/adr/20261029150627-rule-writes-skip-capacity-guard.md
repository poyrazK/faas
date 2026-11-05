# ADR: Traffic rule writes no longer take the fleet capacity guard

Date: 2026-10-29
Status: Accepted (owner, 2026-10-29)
Amends: [ADR-570](570-traffic-enforcement-guarantees.md) (the policy mutation guard's `service_capacity_policy` table lock)
Related: [ADR-20261027163626](20261027163626-traffic-binding-guard-per-host.md) (scoped binding and route-rule guard), [ADR-422](422-bare-metal-service-recovery-capacity.md) (service capacity protection)

## Context

Every guarded traffic policy write begins with
`LOCK TABLE service_capacity_policy IN EXCLUSIVE MODE NOWAIT`
(`LockTrafficCapacitySnapshot`, `pkg/state/traffic_policy_mutation.go`) and
holds it until it commits. The lock exists for a real case. Some policy writes
also insert or update rows that fire the fleet capacity triggers (ADR-422):
- `apps`: `ram_mb`, `cpu_millicores`, `manifest`, `status`, `account_id`;
- `deployments`: `status`, `scope`, `sidecars`, `app_id`;
- `instances`, `accounts.plan`, `compute_nodes` and the migration tables.

A capacity trigger locks the policy singleton row (`FOR UPDATE`, or
`FOR SHARE` while protection is off) and takes a capacity snapshot. A write
that waited for that row lock inside its repeatable-read snapshot could miss
the reservation of the transaction it waited for. The table lock rules that
out: no capacity statement is in flight when the snapshot starts, and none
can start before the write commits.

Every policy write takes it, including those that never write a
capacity-triggered table:
- edge rule create, update and rollback;
- route policy apply;
- CORS preset create and update;
- project environment edge policies.

For those writes the lock buys nothing and costs a lot:

- **It blocks capacity statements fleet-wide.** `EXCLUSIVE` conflicts with the
  `ROW SHARE` lock every capacity trigger takes. While any rule write is open,
  every instance state change waits, and so does every app, deployment and
  plan write. Instance state changes are schedd's wakes, parks and
  placements. A rule write stays open through two analysis phases of up to
  2 s each.
- **It stalls binding writes.** Binding writes, deployment create included,
  take `ROW SHARE NOWAIT` first and retry every 25 ms until the rule write
  commits.
- **It serializes rule writes fleet-wide.** Two rule writes of unrelated
  accounts cannot run together. The scoped route locks of ADR-20261027163626
  cannot help while this lock does.
- **It starves under load.** The rule write's own `NOWAIT` fails while any
  capacity statement is open anywhere, and each instance transition keeps
  one open. On a busy fleet a rule write spends its time in `lock_wait`.

The impact on wakes is inferred from the lock modes and trigger definitions.
It has not been measured in production.

A rule write does not need the capacity guard to be correct:
- **Writes.** It inserts or updates no capacity-triggered table. No trigger on
  `edge_rules`, `cors_presets`, `route_policy_receipts` or
  `project_environment_edge_policies` writes one. Its `apps` and `accounts`
  row locks are `SELECT ... FOR UPDATE`, which fire no statement trigger.
- **Snapshot.** Its analysis reads rules, claims and app and deployment
  bindings. ADR-570 protects that read with the binding guard on every
  binding writer. Binding writers lock every overlapping owner's account, and
  for routes the route-name or global route locks, so they exclude the rule
  write. The capacity guard never did this job. It blocks only statements on
  capacity columns, and bindings also change through columns it does not
  cover, such as slug, visibility, domains and aliases. Removing it from rule
  writes therefore removes no protection the analysis relies on.

## Decision

1. **Rule-only policy writes skip the capacity guard.** A policy write
   declares whether it may fire a capacity trigger. These writes do not, and
   begin without `LockTrafficCapacitySnapshot`:
   - edge rule create, quota create and update;
   - edge rule set rollback;
   - route policy apply;
   - CORS preset create and update;
   - project environment edge policy put.

   Every other policy write keeps the guard unchanged: app create and
   admission, project environment create, project plan apply, environment
   clone and clone operations. A new policy writer keeps it unless it is
   added to the rule-only list.

2. **A rule-only write that fires a capacity trigger fails closed.** Before
   commit, a rule-only write checks `pg_locks` for its own backend. If it
   holds any lock on `service_capacity_policy`, it rolls back with an error
   and commits nothing. That happens only if one of its statements fired a
   capacity trigger. So a later trigger or code change that reaches a
   capacity table fails visibly, instead of racing a capacity reservation.

3. **Nothing else changes:**
   - account, route-name and global route session locks;
   - the account row lock;
   - analyses, ceilings, verdicts and error codes;
   - the binding writers' `ROW SHARE NOWAIT` (`LockTrafficCapacityWriter`).

4. **Rollout.** This ships without a switch:
   - The change only removes a lock from writes that have no use for it.
   - Mixed releases are safe. A previous-release rule write still takes the
     `EXCLUSIVE` guard. A new-release rule write holds no capacity lock, so
     neither excludes anything the other relies on.
   - The commit check bounds the risk to refused rule writes. It can't
     produce a wrong capacity decision.

## Tests

- Each rule-only writer commits while another transaction holds a capacity
  trigger's `ROW SHARE` lock. Today it retries until that transaction ends.
- A capacity statement completes while a rule write is open mid-transaction.
- App create still waits for an in-flight capacity statement.
- A rule-only write whose transaction takes a lock on
  `service_capacity_policy` is refused at commit and leaves no trace.
- Two scoped route writes on disjoint scopes, from different accounts, now
  begin together. Overlapping scopes still serialize.

## Consequences

- Rule writes stop blocking wakes, parks, deploys and other capacity
  statements across the fleet.
- Rule writes of different accounts run concurrently. Route writes still
  serialize when their scopes overlap, or with the fleet-wide guard.
- `lock_wait` for the `route_rule` and `policy` writers stops depending on
  fleet-wide instance churn.
- App, plan and clone policy writes keep the guard, so their capacity checks
  are unchanged.
- **Risk:** a rule-only writer that starts writing a capacity-triggered table
  is refused at commit until it moves back to the guarded list.

## Alternatives considered

- **Take the guard only for the statements that fire triggers.** Taking it
  after the snapshot starts is the bug the guard exists to prevent.
- **Use `ROW SHARE` for rule writes too.** That would still conflict with the
  `EXCLUSIVE` guard of app and plan writes, and protect nothing.
- **Put the guard behind a switch.** The commit check already turns any
  mistake into a refused write. A switch would add a configuration to keep
  and remove for no extra safety.
