# ADR 20261022160000 · Wake failover away from a node that is up but not ready

- **Status:** accepted (design approved by the owner, 2026-10-22). Built in
  five steps, one PR each; this ADR is updated as each step lands.
- **Date:** 2026-10-22
- **Related:** ADR-005 (cold boot always works), ADR-025 (placement),
  ADR-028 (remote routing), ADR-062 (per-node schedd), ADR-137 (node
  lifecycle), ADR-199 (rollout concurrency grant), ADR-421 (ownership
  recovery), ADR-510 (snapshot backing identity), ADR-909 (measured
  admission), ADR-1344 §6 (zero-downtime contract), ADR-1527/1528
  (generation-fenced node admission), issue #299 (scan gate), spec §4.3,
  §6.2, §12

## Context

On 2026-10-22, 15:32–15:40 UTC, every new wake placed on fsn-3 in
production-us returned 503. The rc.290 rollout had upgraded the control
plane and fsn-2. fsn-2's imaged published a new shared base generation
(`base-minimal` 5ca69ea…). fsn-3's drain then timed out and the job
restored fsn-3 to rc.288, whose vmmd expects generation db529ed…. From then
on, vmmd on fsn-3 refused every wake before starting a VM:

```
503 scan_critical runtime base generation unavailable:
runtime base "…" could not adopt its configured generation: …
```

fsn-3 kept answering heartbeats. Placement kept choosing it: fsn-3's schedd
prefers its own node, and warm affinity prefers the node of the last wake.
Nothing retried the wake on fsn-2, which was healthy. Service came back only
when an operator drained fsn-3 by hand.

Gregale fails over today only when a node is unreachable or dead:

| Failure | Detected by | Effect |
|---|---|---|
| gatewayd on a node does not answer | gatewayd-public's compute gateway pool (dial error) | the request goes to the next node |
| vmmd does not answer Ping | schedd heartbeat, 3 failed probes / 90 s | `lifecycle=unavailable`; ownership recovery (ADR-421) re-homes apps; `ReconcileDeadNodeInstances` fails rows after 120 s |
| vmmd answers Ping but refuses every wake | nothing | every wake placed there fails |

The third row is the gap this ADR closes.

## Decision

A wake that a node refuses before the VM starts moves to another node inside
the same gateway hold. The node's readiness becomes state that has to be
proven, and placement prefers proven nodes. The five steps below are ordered
so that each one is useful on its own. Step 1 alone would have turned the
incident into slower wakes instead of failed ones.

### 1. schedd re-places a refused wake

`Engine.admitAndDispatchWithOptions` is the single admission-to-vmmd
pipeline behind `Wake`, `EnsureWake`, `AdmitInstance`, burst siblings and
explicit-deployment admissions. It now runs each admission as an *attempt*.
When vmmd refuses an attempt with a **node refusal** (below), schedd:

1. releases that attempt exactly as the existing boot-error path does: the
   ledger reservation is released and the row moves to FAILED with
   `wake_boot_error`/`vmm_boot_failed`. A restore RPC error stays terminal
   for its attempt. ADR-005's cold-boot fallback still lives in vmmd.
2. counts the refusal against the node (`schedd_wake_failover_total`, and
   from step 3 the node's breaker);
3. excludes the node and runs a new attempt for the same wake. Placement
   skips excluded nodes the same way it skips nodes without headroom.

Bounds, all in `pkg/api/limits.go`:

- `WakeFailoverMaxAlternates = 2`: at most two alternate nodes, three
  attempts in total.
- `WakeFailoverMinRemaining = 2 s`: an alternate starts only while at least
  this much of the caller's deadline remains. The gateway's coordinated
  wake runs under the 30 s wake hold (`WakeQueueTTLSeconds`), and every
  attempt inherits that deadline, so failover never extends the hold. The
  gateway sees a slower success.

When no other node fits, or the bounds are spent, the wake fails with the
node's refusal. It is the useful error; "no capacity" would hide it.

**Node refusals** form a closed set. They are failures that vmmd reports
before any guest code runs, and they depend on the node rather than on the
app or its artifacts:

| vmmd Problem | Failover reason |
|---|---|
| `scan_critical` "runtime base generation unavailable" (the incident) | `base_generation` |
| `scan_critical` "scan sidecar generation mismatch" | `base_generation` |
| `scan_critical` "scan sidecar missing" (read through the node's cache) | `scan_sidecar_missing` |

Not node refusals:

- `scan_critical` "Fixable CRITICAL vulnerability" and "scan sidecar
  incomplete". vmmd re-reads the canonical sidecar before refusing, so
  every node gives the same verdict. The scan gate (#299) is unchanged.
- The customer's `app_startup_timeout`, `app_startup_crash`,
  `app_not_listening` and `app_loopback_bound`. Customer code ran; running it
  again elsewhere would repeat its startup side effects and fail the same way.
- `internal`, deadline expiry, and transport `Unavailable`. vmmd may have
  started the VM before the error, so a second VM could exist. Step 3 adds
  an explicit pre-start marker from vmmd so jailer, netns, cgroup and disk
  failures can join the set without guessing.

Titles are matched by prefix, because a peer older than grpcerr's split
title/detail fields sends `title: detail` in the status message.

An alternate attempt is the same logical wake, so it skips three gates that
the first attempt has already passed or armed:

- the wake-rate limiter: the token was spent once;
- the scale-out cooldown: the first attempt stamped it;
- the snapshot-miss backoff gate, and recording another miss.

It keeps every gate that protects capacity or money: the app concurrency cap
and rollout grant (ledger), the RAM, vCPU and CPU ceilings including
ADR-909's measured charge (ledger), the account spend cap, node lifecycle,
and ADR-1528 generation fencing. Mirror admissions never fail over: they are
best-effort shadows with their own deadline.

**Invariants.** Each attempt releases its reservation before the next
admits, and every admission goes through `NodeLedger.Admit`, so:

- §6.2 invariant 1 (≤ `max_concurrency` + `RolloutConcurrencyGrant` live
  instances) holds;
- invariant 2 (per-node RAM ceiling, and ADR-909's measured-admission
  bounds) holds.

A property test drives random fleets, refusal patterns and concurrent wakes,
and checks both invariants and the release of every refused attempt.

### 2. Node readiness is proven state

`compute_nodes.readiness` (expand-only migration with a CHECK) takes the
values `ready`, `degraded`, `unavailable` and `draining`. schedd's heartbeat
is its only writer. `ready` requires all of:

- a fresh heartbeat;
- vmmd's Ping reports that it can adopt the fleet's current base
  generation. This is an additive Ping field; a vmmd too old to report it
  does not block `ready`, so a mixed fleet keeps placing;
- recent wakes on the node succeeding (the step 3 breaker is closed).

`unavailable` and `draining` mirror `lifecycle`, so one column and one gauge
answer "can this node take a wake now". `lifecycle` remains the authority for
operator and recovery states, and readiness never overrides it. A degraded
node is not `lifecycle=unavailable`, because that starts the recovery arbiter
and moves running instances. In the incident, fsn-3's running VMs were fine.

Placement uses `ready` nodes, and `degraded` nodes only when no ready node
has capacity. The column defaults to `ready`, so behaviour is unchanged until
the evaluator writes.

### 3. A per-node wake breaker

`NodeWakeBreakerFailures` (3) consecutive platform-attributable wake failures
within `NodeWakeBreakerWindow` (60 s) mark the node `degraded`. After
`NodeWakeBreakerCooldown` (30 s) the breaker is half-open: one wake may probe
the node. Success closes the breaker and the node becomes `ready`; failure
opens it again.

Only platform-attributable failures count: the step 1 node refusals, plus
failures that vmmd marks as pre-start and platform-caused (new ErrorInfo
metadata, `wake_stage=pre_start`). That metadata lets jailer, netns, cgroup,
disk-full and vmmd-side capacity failures count. The customer's crashes,
readiness timeouts and OOMs never count.

Each schedd keeps its own breaker in memory and writes the verdict to
`compute_nodes.readiness`, so peer schedds place by it. A platform-owned
canary wake per node per minute, which proves readiness without customer
traffic, is optional and stays behind a flag until its cost is measured.

### 4. The previous base generation stays adoptable

This was the root cause of the incident. A node rolled back in the middle of
a release must still serve:

- imaged keeps the previous shared base generation;
- vmmd adopts its configured generation from the retained copy when the
  canonical key already holds a newer one.

The scan gate is not weakened. The retained generation needs its own scan
sidecar naming that generation, and the fixable-CRITICAL refusal applies to
it unchanged. ADR-510's backing identity (digest sidecar) is checked the same
way. The step 4 PR records the storage layout here after reading imaged's
publication path.

### 5. gatewayd-public retries responses that were never dispatched

gatewayd-internal marks a response as *not dispatched* when it failed before
any request byte reached the guest app:

- a wake refusal from schedd;
- a wake hold that expired on that node;
- a proxy connection that was refused.

The marker is an internal response header that gatewayd-public strips.
gatewayd-public may retry such a response through another node only when:

- the request body is replayable: there is none, it was not read, or it is
  buffered and at most `GatewayFailoverMaxBufferedBodyBytes`;
- a global retry budget allows it (`GatewayFailoverRetryBudgetPct`, 10 % of
  requests);
- the retry uses at most 2 extra nodes.

Nothing that reached customer code is retried, so POSTs stay at-most-once.
This is the "retries requests that were refused before any byte reached a
guest" mechanism that ADR-1344 §6 requires.

## Observability (spec §12)

| Metric | Step | Alert |
|---|---|---|
| `schedd_wake_failover_total{reason,outcome}` (counter; `outcome` ∈ `succeeded`, `refused_again`, `no_alternate`, `out_of_time`, `failed`) | 1 | none. A diagnostic: `outcome="succeeded"` is a wake the incident would have failed |
| `schedd_compute_node_readiness{node,state}` (one-hot gauge) | 2 | — |
| `FaasComputeNodeDegraded` | 3 | `state="degraded"` for 5 m warn; no node `ready` for 2 m page |
| `gateway_wake_failover_total{reason}` (gatewayd-public) | 5 | none yet; collect before setting a threshold |

The owner's brief named `gateway_wake_failover_total{reason}`. Step 1's
failover happens in schedd, which names its metrics `schedd_*`, so it gets
its own counter. The gateway name is kept for step 5's cross-node retry,
which does happen in the gateway.

## Consequences

- A node that answers heartbeats but refuses wakes costs one extra vmmd round
  trip per wake (step 1) until the breaker degrades it (step 3). After that
  it costs nothing.
- A refused attempt leaves a FAILED row with its own `wake_id`. The gateway
  receives the `wake_id` of the attempt that succeeded.
- A fleet-wide refusal (every node on the bad generation) costs up to two
  fast extra refusals per wake before the original error returns.
- Placement and node lifecycle change, but no new owner appears: schedd still
  writes instances and node state; vmmd still alone touches Firecracker.

## Rejected alternatives

- **Retry in gatewayd-internal by calling Wake again.** The gateway does not
  know which node refused, and schedd would place on the same node again
  (owner preference plus warm affinity).
- **Mark the node `unavailable` on the first refusal.** One bad artifact
  would drain a node, and the recovery arbiter would move running instances
  that were fine.
- **Re-place on every vmmd error.** A customer startup crash would run
  twice and fail twice. An ambiguous transport error could leave two VMs for
  one wake.
- **Fail over only in gatewayd-public (step 5 alone).** It would work only
  for requests whose body can be replayed, and it costs a full extra hop. The
  schedd re-place happens inside the hold, for every request.
