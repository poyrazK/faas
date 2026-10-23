# ADR-20261022203523 · Per-instance request concurrency

- **Status:** proposed
- **Date:** 2026-10-22
- **Amends:** spec §4.9.1 (per-VM concurrency model, issue #559)
- **Related:** [ADR-059](059-customer-scaling-policy.md) (per-app scaling policy),
  [ADR-194](194-multi-signal-scaling-targets.md) (scaling targets),
  [ADR-199](199-rollouts-on-every-plan.md) (rollout concurrency grant),
  [ADR-978](978-route-priority-under-load.md) (warm-queue priority),
  [ADR-1342](1342-function-settings-and-concurrent-workers.md) (function
  worker concurrency)
- **Finding:** Hunt 12 H12-4 (same as Hunt 11 H11-12)

## Context

An app on production-us did not scale out under load. The `hello-python`
template runs `gunicorn --workers 2`, so one instance serves two requests at
a time. The app allowed three instances (`max_concurrency 3`) on the Scale
plan. Eight concurrent requests that each take 3 s finished after 3 to 12 s,
all on one instance. Two more instances were allowed and never started.

Every component that decides how many instances an app needs divides by the
same number, the plan's `concurrency_per_vm` (Free 4, Hobby 5, Pro 25,
Scale 80):

| Component | Code | Uses the plan bound to |
|---|---|---|
| gatewayd-internal per-VM gate | `effectiveVMConcurrencyLimit` | admit up to 80 requests to one VM before trying a sibling or queueing |
| gatewayd-internal burst admission | `desiredBurstInstances` | ask schedd for `ceil(in-flight / 80)` instances |
| schedd wake coordinator | `WakeFanout.PerVM` | start another cold wake only past 80 waiters per instance |
| schedd aggressive reaper | `inflightDemandReplicas` | keep `ceil(in-flight / 80)` instances during scale-in |

With eight requests in flight every one of them computes one instance. The
gateway therefore sends all eight requests to the instance that is already
full, where they wait in gunicorn's backlog.

Spec §4.9.1 already says that the handler process's concurrency is the
customer's responsibility. It gives the customer no way to tell the platform
what that concurrency is. The existing settings do not cover it:

- `autoscale_target_rps` is a rate. Eight 3 s requests are 2.7 requests per
  second, so a rate target sees almost no load.
- `scaling.targets: concurrent_requests` (ADR-194) is a schedd trigger that
  samples in-flight counts. It can add an instance after the fact, but the
  gateway keeps admitting up to 80 requests to the full instance. A burst is
  already inside instance 1 before a sibling is routable.
- `max_queue_depth` and `max_queue_wait_ms` size the waiting room, not its
  door.
- `function_concurrency` (ADR-1342) multiplexes requests over a function
  runner's interpreter workers inside one VM. It does not change how many
  requests the gateway sends to that VM.

## Options

### A. An explicit per-app `per_instance_concurrency` (recommended)

The app declares how many requests one instance serves at once. It is set in
the scaling policy: `scaling.per_instance_concurrency` in `gregale.yaml`,
`gregale app <slug> scale --per-instance-concurrency N`, or
`scaling_policy.per_instance_concurrency` on `PATCH /v1/apps/{slug}`. All four
rows of the table above divide by it instead of by the plan bound.

- Exact: the customer knows their server's concurrency, and the platform
  enforces it without guessing.
- Small: the setting goes in the `apps.scaling_policy` jsonb like the other
  queue settings, so it needs no migration. One resolver serves all three
  daemons.
- The default (unset) is today's behaviour, byte for byte.
- Cost: the customer has to set it. The template that showed the bug can
  declare it.

### B. Infer it from the detected server

The builder reads the start command and sets the value: `gunicorn --workers`
or `-w`, `WEB_CONCURRENCY`, `uvicorn --workers`, puma `-w`/`-t`.

- No configuration for the common case.
- Fragile. gunicorn also reads `gunicorn.conf.py` and `GUNICORN_CMD_ARGS`.
  `--threads` and the gevent, eventlet and uvicorn worker classes break the
  "one request per worker" rule. Dockerfile `CMD`, shell entrypoints and
  wrapper scripts hide the command.
- A guess that is too low makes a capable app queue at the gateway and scale
  out for no reason. That costs the customer instance-seconds they did not
  choose, so a wrong inference silently changes their bill.
- It needs a new builder-to-deployment metadata path and rules for which wins
  when the inferred value and a declared value disagree.

### C. A soft target only

Reuse `scaling.targets: concurrent_requests` as the gateway's burst signal and
as a routing preference, and keep the per-VM gate at the plan bound.

- No new field.
- It does not fix the burst. With the gate at 80, all eight requests reach
  instance 1 before a sibling is routable (≥ 350 ms), so they still queue in
  the guest.
- It changes the meaning of every existing `concurrent_requests` target, which
  today is a sustained schedd signal and not an edge limit.

### D. A and B together

Inference supplies the default and a declared value wins. This is B's
fragility plus A's work. It is deferred; see *Not decided*.

## Decision

Option A.

1. **Setting.** `per_instance_concurrency` is a field of the app's scaling
   policy (`apps.scaling_policy` jsonb, ADR-059). It is set through
   `scaling.per_instance_concurrency` in the manifest, `gregale app <slug>
   scale --per-instance-concurrency N` (also `gregale app <slug>
   --per-instance-concurrency N`), or `scaling_policy.per_instance_concurrency`
   on `PATCH /v1/apps/{slug}`. `0` or omitted means the plan's
   `concurrency_per_vm`, which is exactly today's behaviour.
2. **Bounds.** Valid values are `0` to the plan's `concurrency_per_vm`
   (`Limits.ConcurrencyPerVMBound`; no new quota). apid rejects a larger value
   with a 422 that names the plan limit and the requested value. After a plan
   downgrade a stored value larger than the new bound is clamped when it is
   read, never refused at request time. `Limits.PerInstanceConcurrency` in
   `pkg/api/limits.go` is the only resolver, and gatewayd-internal, schedd and
   apid all call it.
3. **Gateway gate.** Each gatewayd-internal process admits at most the
   effective value to an instance; see *Several gateways* for what that means
   across nodes. Request N+1 goes to a sibling with a free slot. When every
   routable instance is full, the request waits in the warm queue (ADR-978
   priority order, `max_queue_depth` or the plan default), and a waiter takes
   whichever slot frees first, on any instance. The
   `vm.inflight_threshold_reached` audit row stays reserved for the plan
   bound. A full instance is normal for an app that declares a lower limit,
   and one events row per queued request would put request-rate inserts on
   the control-plane Postgres. Its waits are counted by the existing
   queue-wait metric.
4. **Gateway scale-out.** Burst admission asks schedd for
   `ceil(in-flight / per_instance_concurrency)` instances, capped at the app's
   instance ceiling. The third concurrent request to a two-slot app asks for a
   second instance at once, not after the 81st.
5. **Warm-queue wait.** Requests over the limit used to wait in the guest's own
   backlog. The gateway must not turn them into 503s after the 1 to 2 s
   saturation default. An app that sets `per_instance_concurrency` and leaves
   `max_queue_wait_ms` at 0 therefore waits up to
   `PerInstanceConcurrencyQueueDefaultWait`. That is defined as the gateway's
   request hold, `WakeQueueTTLSeconds` (30 s; spec's queue cap 512 / 30 s), and
   a test keeps it there. The waiter count is unchanged: the plan's warm-queue
   depth (Free 8, Hobby 32, Pro 128, Scale 512), all within the 512 cap unless
   the customer raises `max_queue_depth` as they already can.
   - **The request budget starts after the wait.** The gateway stamps the
     budget when the request reaches an instance, after `acquireVMTarget`.
     Before, time in the guest's backlog counted against the budget. Now a
     request can wait 30 s and then still use its whole budget: 3 s for a
     function, 30 s for an app by default, or its `request_timeout_s`. The
     worst-case end-to-end time is the wait plus the budget. Customers who need
     a tighter bound set `max_queue_wait_ms`.
   - An explicit `max_queue_wait_ms` wins.
   - `concurrency_overflow: drop` still rejects at once.
6. **schedd.** The wake coordinator's fan-out (`WakeFanout.PerVM`) and the
   aggressive reaper's in-flight floor (`inflightDemandReplicas`) use the
   effective value. A cold burst fans out at `ceil(waiters / N)`. Scale-in
   never parks an instance that the in-flight requests need at N per instance.
   The `concurrent_requests` trigger (`pkg/sched/targets`) treats a declared
   `per_instance_concurrency` as an implied `concurrent_requests` target of N,
   unless the app declares that target itself. It reads vmmd's per-instance
   in-flight count, which covers every gateway. Workers imply nothing.
7. **Visibility.** `effective_limits.concurrency_per_instance` on
   `GET /v1/apps/{slug}` reports the effective value, and `gregale app <slug>`
   prints it. `concurrency_per_vm` stays the plan bound.
8. **Dark launch.** Per the owner's rule for new customer features, apid accepts
   a non-zero value only for accounts listed in
   `FAAS_PER_INSTANCE_CONCURRENCY_ACCOUNTS` (comma-separated UUIDs, or `*`).
   It is unset by default, which admits no account. For any other account
   every non-zero value, negative ones included, gets the same
   `422 unknown field(s)` an N-1 apid returns. Keeping a stored value or
   clearing it to 0 is always allowed, so delisting an account never breaks
   its other policy writes. The gateway and schedd apply whatever is stored,
   and apid is the only writer, so the gate is complete at write time.
   Until launch the customer surface stays hidden:
   - `--per-instance-concurrency` is a hidden flag. It parses, but it is not in
     the CLI metadata, so help, completions, man pages and
     `docs/cli-reference.md` do not name it.
   - The customer docs section is `docs/per-instance-concurrency.md`. That page
     is not listed in `docs/customer-pages.json`, so it is not published.
   - The field stays in `api/openapi.yaml` and the generated SDK models,
     because the spec's schema-parity check requires every DTO field. An
     unlisted caller who sends it gets the unknown-field answer.

   Launch steps, all in one change:
   1. Set the gate to `*`.
   2. Move the docs section into `docs/scaling-policy.md`.
   3. Add the flag to `cmd/gregale/cli_meta.go` and the usage lines, and
      regenerate the CLI reference.
   4. Add the template line (Decision 9).
9. **Template, at launch.** Once the gate is `*`, `hello-python` declares
   `per_instance_concurrency: 2` next to its `gunicorn --workers 2`, so the app
   that showed the bug scales out without configuration. Until then a
   template that declares it would fail to deploy for unlisted accounts.

## Several gateways

Slots and their wait queues live in one gatewayd-internal process
(`vmConcurrencyManager`, `pkg/gateway/vm_concurrency.go`). Production runs one
`gatewayd-public` → `gatewayd-internal` pair per node, and the upstream
balancer spreads an app's requests across nodes. Any node's gateway forwards to
any instance through that instance's vmmd (ADR-028). With k gateways in front
of an app, one instance can therefore hold up to k×N requests. The burst
signal is split the same way: each gateway asks schedd for
`ceil(local in-flight / N)` instances, so the gateways together can ask for up
to k times too few.

Options:

- **(a) Per-gateway limit, fleet-wide correction (chosen).** The gate stays
  local and fast. Two schedd-side pieces correct the fleet-wide picture:
  - The `concurrent_requests` trigger reads vmmd's per-instance in-flight
    count. Every gateway's forwards to an instance pass through the vmmd that
    hosts it, so that count is the instance's true load. The trigger scales
    on it with N as the target (Decision 6). An instance above N therefore
    gets siblings within a trigger tick or two, up to `max_concurrency`.
  - The trigger logs `targets: per-instance concurrency oversubscribed across
    gateways`, with the app, in-flight count and N, at most once per app per
    minute. Operators can see how often it happens.

  The guest absorbs the transient excess in its own listen backlog, bounded
  by (k−1)×N rather than today's plan bound of 80. Customers size N as their
  server's real concurrency and keep a normal listen backlog. The customer
  docs say this.
- **(b) Route an app through its owner node's gateway.** This gives an exact
  cap but adds a cross-node hop for every request that lands on another node.
  It also concentrates an app's traffic on one gateway and needs a
  gateway-to-gateway forwarding path that does not exist. Rejected.
- **(c) Enforce in vmmd as well.** vmmd is the one place every request to an
  instance passes through. It could hold a per-instance semaphore of N and
  answer `ResourceExhausted`, and the gateway would re-pick or queue. That
  gives an exact fleet-wide cap. It needs a `ForwardHTTPStream` and
  `ForwardRawStream` field carrying N (additive, so N±1 safe) and a gateway
  retry path for the refusal. This is the follow-up if the oversubscription
  log shows the transient excess matters in practice. Not done here.

**Reaper interaction (H4-70).** The aggressive reaper's in-flight floor takes
the larger of the local gateway's in-flight count and vmmd's per-instance
counts. The gateway count covers only this node's gateway. vmmd's counts
cover every gateway, but only for instances the schedd sees. The fleet
request-rate floor (`instances.request_count`, H4-70) remains the backstop for
traffic served entirely through other nodes. With N declared, the floor
divides by N instead of the plan bound, so it errs toward keeping instances.
It never parks below what either view needs.

Tests pin the semantics:

- `pkg/gateway` `TestPerInstanceConcurrencySlotsArePerGatewayProcess`: two
  gateways put 2×N on one instance, and the next request through either one
  waits.
- `pkg/sched/targets` `TestTrigger_PerInstanceConcurrencyScalesOnFleetInflight`:
  a fleet-wide in-flight count above N admits an instance and logs the
  oversubscription once.

## Invariants

- **§6.2-1 (instance cap)** is unchanged. A per-instance limit changes how many
  instances the gateway and schedd's triggers ask for. It does not change who
  admits them. schedd's ledger still holds at most `max_concurrency(plan)` +
  `RolloutConcurrencyGrant` instances in {WAKING, COLD_BOOTING, RUNNING}, and
  the gateway's request is capped at the app ceiling before it reaches schedd.
  A property test drives the wake fan-out with every per-instance value from 1
  to the plan bound and checks that it never wants a wake at or past the
  ceiling.
- **§6.2-2 (RAM)**: every extra instance goes through the RAM ledger and
  measured admission (ADR-909) exactly as an autoscale admission does.
- **§6.2-3, -4, -5** are untouched: snapshots, parking and restore identity are
  per instance, not per request.

## Billing

Unchanged: plan RAM + 8 MB per running second, per instance (§4.7). A lower
per-instance concurrency can run more instances for the same traffic. That
is the customer's explicit choice, it is bounded by their own
`max_concurrency`, and the instances show up on `gregale app <slug> costs`.
The default changes nobody's bill.

## Release compatibility

- **No migration.** The value lives in the `apps.scaling_policy` jsonb, as in
  [ADR-194](194-multi-signal-scaling-targets.md) and [ADR-195](195-scheduled-scaling-floors.md).
- **New JSON keys are `omitempty`.** The only new key is
  `per_instance_concurrency`, on `api.ScalingPolicy` and in the stored jsonb
  shape. `effective_limits.concurrency_per_instance` keeps its shape; only its
  value changes, and only for an app that declares a limit.
- **N-1 readers.** gatewayd-internal and schedd decode the jsonb leniently and
  ignore the key, so they keep the plan bound until they are upgraded. The
  gateway alone gives the fix: gate plus burst. schedd adds the fan-out and
  scale-in pieces.
- **N-1 apid.** Its strict scaling-policy decoder rejects the key with
  `422 unknown field(s): per_instance_concurrency`. A new CLI talking to an old
  apid fails loudly instead of dropping the setting. The deploy order (control
  plane before edge/node) upgrades apid first.
- **Skew hazard.** While an N-1 apid is still serving, a CLI read-modify-write
  of the policy through it (`--max-queue-depth` and similar flags) drops the
  key, because N-1 GET does not return it. The window is the apid hand-off on
  one control-plane node. The customer re-applies the setting with the next
  deploy or `scale` call.

## Consequences

- An app whose server handles a few requests at a time can say so with one
  line, and it then scales out at the edge on the first request over its
  capacity instead of queueing inside one guest.
- Requests over capacity wait at the gateway, where they reach the first free
  slot on any instance, instead of in one guest's backlog.
- Nothing changes for an app that does not set it.
- `scale_out_cooldown_s` still spaces successive scheduler admissions. The CLI
  and manifest default it to 5 s. One burst admission takes every instance it
  needs at once. A burst that grows in steps can wait one cooldown for its
  last instance, and its requests wait for that instance in the warm queue.

## Not decided

- **Inference (B/D).** A follow-up can have the builder report a detected worker
  count as a deploy hint, for example "gunicorn --workers 2 detected; set
  `scaling.per_instance_concurrency: 2`", instead of setting the value
  silently. The safest first case is the gunicorn start command that
  `pkg/frameworkprofile` writes itself: one sync worker, and the platform knows
  it because it wrote the command.
- **Exact fleet-wide enforcement** (*Several gateways*, option c), if the
  oversubscription log shows the transient excess matters.
- **Per-deployment values.** The value belongs to the app, like the rest of the
  scaling policy, and is pinned with it in project-environment workload specs.
  A canary whose server differs from the stable revision gets the app's value.
