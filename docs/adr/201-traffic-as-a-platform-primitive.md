# ADR-201 · Traffic resilience as a platform primitive

- **Status:** accepted
- **Date:** 2026-09-21
- **Decision:** Three additions so that resilience against a failing
  *callee* stops being application code: (1) `kind=retry`, a replay of a
  failed request against a different healthy instance, on the public edge
  and the service proxy; (2) `kind=circuit_breaker`, a real
  closed/open/half-open breaker over instance health, replacing the fixed
  5 s quarantine in `ServiceProxy`; (3) an **egress breaker** that fails a
  tenant's connection to a declared upstream fast, as an nftables reject
  rule in the instance's own netns, driven by the probe outcomes ADR-098
  already collects.
- **Why:** Gregale already owns eight of the nine traffic concerns an API
  developer would otherwise hand-roll — rate limiting, CORS, auth, IP and
  geo gating, body caps, wall-clock budgets, per-VM concurrency, and
  load balancing. Retry and circuit breaking are the two that are still
  the customer's problem, and they are the two that cost the most
  application code to get right. The platform is also strictly better
  placed to do both than the app is: only the gateway knows the *other*
  healthy instances, and only the host knows the tenant's egress path.
  The concrete failures this closes are (a) a transport failure against a
  dead instance returns 502 to the client even when a healthy sibling was
  available and already picked out (`pkg/gateway/handler.go` evicts the
  dead target but does not replay the request), and (b) a customer whose
  database is unreachable pays the full TCP timeout on every request,
  which burns the `kind=budget` deadline and then the wake slot, turning
  one dependency outage into a per-app capacity outage.
- **Consequences:** two new edge-rule kinds (closed set 15 → 17), one new
  per-app egress policy surface, four new limits in `pkg/api/limits.go`,
  three migrations. `ServiceProxyMaxAttempts` and the fixed-TTL quarantine
  map are removed in favour of the breaker. New metrics:
  `gateway_retry_attempts_total{outcome}`,
  `gateway_retry_exhausted_total{reason}`,
  `gateway_circuit_transitions_total{from,to}`,
  `gateway_circuit_open_targets{app_id}` (all four on the
  gatewayd-internal-local registry), and
  `<prefix>_egress_circuit_state{app_id,upstream_hash}` on the shared
  `OpsMetrics` registry, since schedd emits it.

  Two corrections to an earlier draft of this list. `circuit_state{app_id,
  target}` keyed by instance id is **unbounded** — instance ids churn on
  every wake, so that series set grows for the daemon's lifetime; it is
  replaced by `circuit_open_targets{app_id}`, a count, which answers the same
  operator question at one series per app. And `retry_attempts_total` carries
  no `kind` label, because retry has exactly one kind.

  `egress_circuit_rejects_total` is **deferred**: the reject count lives in
  an nftables counter inside each guest netns, so surfacing it needs a
  vmmd-side `nft list counter` scrape that does not exist yet. The state
  gauge already tells an operator that a circuit is open; the packet count is
  a refinement. New env:
  `FAAS_GATEWAY_RETRY`, `FAAS_GATEWAY_CIRCUIT_BREAKER`,
  `FAAS_EGRESS_CIRCUIT_BREAKER` — all default **off**, matching the
  ADR-098 rollout posture. No change to the wake path or the §6.3 budget.
- **Rejected alternatives:** listed per section.

---

## 1. `kind=retry` — replay against a different healthy instance

### The enabler

`admitRequestBody` (`pkg/gateway/request_body_admission.go`) already reads
the entire bounded body before wake and capacity admission, into either a
`bytes.Reader` or a seekable spool file — and then sets `r.GetBody = nil`
at the end. Both backing stores are rewindable; only that assignment
prevents replay. The admission path now populates `GetBody` with a closure
that rewinds the spool (`Seek(0, io.SeekStart)`) or re-wraps the memory
slice. Upgrade requests and `http.NoBody` are untouched — they never enter
admission.

This is the whole cost of making the public path retryable. No new
buffering, no new memory ceiling: the bytes are already resident and
already counted against `MaxRequestBodyBytes`.

### Safety rules (all must hold)

A request is replayed only when every one of these is true. They are
checked in this order and each failure is counted on
`gateway_retry_exhausted_total{reason}`:

1. **The response is uncommitted.** No status line, header, or body byte
   has reached the client. The service proxy's existing
   `serviceProxyResponseWriter` buffer already provides this; the public
   path gains the same guard. A streaming response that has flushed is
   never replayed.
2. **The failure is a transport failure, not an application answer.** Only
   the existing `markStaleTarget` signal — vmmd/netns transport death —
   arms a retry. A guest that answers 500 or 503 has *served* the request;
   replaying it would double-execute a side effect the app chose to
   perform. This is the single most important rule in this ADR.
3. **The method is idempotent**, or the rule explicitly opted in. Default
   set is `GET HEAD OPTIONS TRACE PUT DELETE`. `POST` and `PATCH` are
   excluded unless the rule sets `allow_non_idempotent: true` **and** the
   request carries a non-empty `Idempotency-Key`. The application must honor
   that key; the platform does not claim that an arbitrary side effect is
   idempotent merely because the header exists.
4. **A different target exists.** The retry re-picks through the normal
   picker with the failed instance already evicted, so it cannot select
   the same instance. No healthy sibling means no retry.
5. **The remaining `kind=budget` allows it.** Retry borrows from the same
   `pkg/reqbudget` remaining time as every other hop. If a budget rule
   matched and the remaining time is below `min_remaining_ms` (default
   250 ms), the retry is skipped and the original error is returned. A
   retry can never extend a customer's deadline.
6. **The app's aggregate retry budget has capacity.** The default permits
   retries equal to 10% of originals in a 10-second window, with a minimum of
   one for low-traffic recovery. The public edge and internal service proxy
   share this budget, so a broad outage cannot replay every failed request.
7. **`max_attempts` is not exhausted.** Total attempts, not retries:
   default 2, ceiling 3.

### Action shape

```json
{ "kind": "retry",
  "retry": { "max_attempts": 2,
             "allow_non_idempotent": false,
             "min_remaining_ms": 250,
             "backoff_ms": 0,
             "budget_percent": 10,
             "budget_min_retries": 1 } }
```

`backoff_ms` defaults to 0 because the failure mode being retried is a
*dead peer*, not a loaded one; the next instance is a different process
and delay buys nothing. It is exposed as a bounded knob (≤ 1000 ms) for
the case where the sibling is still waking.

The aggregate budget complements `max_attempts`: the attempt ceiling bounds
one request, while `budget_percent` and `budget_min_retries` bound total
amplification when many requests fail together. Exhaustion is exported as
`gateway_retry_exhausted_total{reason="aggregate_budget"}`.

This guarantee is deliberately scoped to retries Gregale generates: public
edge replay, service-proxy replay, and durable invocation redelivery. Guest
code can still issue arbitrary outbound HTTP calls; Gregale cannot infer that
two such calls are retries without owning the client protocol. Egress budgets
or an SDK-level retry client would be a separate capability.

### Plan gating

`EdgeRulesRetryPerApp`: Free 0 · Hobby 3 · Pro 10 · Scale 25. Free is
excluded because a retry doubles the worst-case work a single free
request can cause, and Free already has `max_concurrency` 1 — there is
rarely a sibling to retry against.

### Rejected

- **Retry on 5xx from the guest.** Breaks rule 2. A 500 is an answer.
- **Retry the wake.** Wake already has its own single-flight, rate
  limiter and queue; a second wake attempt inside a request is how you
  turn a cold-start storm into an outage.
- **Unbounded `GetBody` for streaming uploads.** A request that was never
  admitted (upgrade, `NoBody`) has nothing to replay, by construction.

---

## 2. `kind=circuit_breaker` — instance health with real state

`ServiceProxy` today benches a failed endpoint for a fixed 5 s
(`serviceProxyDefaultEndpointTTL`) and the public path evicts the route
cache entry. That is outlier ejection: there is no failure-rate
threshold, no open state, and no controlled probe. A flapping instance is
re-admitted every 5 s regardless of how many times it has just failed.

`pkg/circuit` introduces a three-state breaker keyed by
`(app_id, instance_id)`. It lives at the top level rather than under
`pkg/gateway` because schedd's egress breaker (§3) uses the same state
machine, and one implementation is what makes the two surfaces behave
identically:

- **closed** — requests flow. A rolling window (default 10 s) counts
  transport failures and successes. Transition to **open** when
  `failures ≥ min_requests` (default 5) *and*
  `failure_ratio ≥ failure_threshold` (default 0.5). The dual condition is
  what stops a single failure on a low-traffic app from opening the
  circuit.
- **open** — the target is not selectable by the picker. After
  `open_duration` (default 5 s, doubling to `max_open_duration` 60 s on
  each re-open without an intervening healthy close) transition to
  **half_open**.
- **half_open** — exactly one in-flight request is admitted as a probe.
  Success closes the circuit and resets the backoff; failure returns it
  to **open** with the doubled duration.

The breaker replaces the quarantine map in `ServiceProxy` and becomes the
filter the public picker consults, so both paths get identical semantics
from one implementation. With the feature flag off, the breaker is
constructed with the legacy parameters (`min_requests: 1`,
`open_duration: 5s`, no doubling), which is byte-equivalent to today's
behaviour — that equivalence is pinned by a test.

This composes with §1: a retry re-picks, the picker skips open circuits,
so a retry naturally lands on a target that is not known-bad.

### Plan gating

`EdgeRulesCircuitBreakerPerApp`: Free 0 · Hobby 3 · Pro 10 · Scale 25.
The *default* breaker runs for every app on every plan — the edge rule
only tunes it. Resilience is not a paid feature; tuning it is.

### Rejected

- **Per-node breaker state in Postgres.** The signal is per-node
  transport health, it decays in seconds, and a DB round-trip in the
  picker is exactly the dependency ADR-190 §3 just removed from the hot
  path. State stays in-process and is rebuilt from observation.
- **Ejecting the whole node on repeated instance failures.** That is
  `deadnode_reconciler`'s job and it already exists.

---

## 3. Egress breaker — fail fast to a failing dependency

### Why this does not need an L7 proxy

The naive shape of "stop sending requests to an unhealthy dependency" is
a proxy that terminates the tenant's outbound connection, which would mean
intercepting tenant TLS. That is rejected outright: it breaks §11 (the
platform would hold plaintext customer traffic and credentials) and it
contradicts ADR-046's stateless-by-contract posture.

It is also unnecessary, because both halves already exist:

- **The health signal.** ADR-098's probe (`pkg/meter/upstream_probe.go`)
  already dials every captured `(host, region)` every 30 s with
  `crypto/tls.Dial` and writes `data_upstream_probes` rows classified
  `ok | timeout | refused | tls_handshake | dns | unreachable`. It is
  already §11-clean: hashed hosts in metric labels, no plaintext anywhere,
  and deliberately never `net/http.Get`.
- **The enforcement point.** `pkg/netns/denylist.go` and
  `config.go::NftCommands` already build a per-instance nftables egress
  ruleset — this is how §11's "deny 25/465/587, deny RFC1918 + link-local
  + metadata" is enforced today.

The egress breaker is therefore the *same* state machine from §2, fed by
probe outcomes instead of transport failures, whose open state adds one
`reject` rule to a netns that already has a deny chain.

### Mechanism

`pkg/sched` consumes `data_upstream_probes` by **polling at the probe's own
cadence**, not by subscribing.

An earlier draft of this ADR said the breaker would reuse an existing
notify. That was wrong: `data_upstreams_changed` fires on the *capture*
table, not on probe inserts, and `pkg/sched/upstream_affinity.go` records
that schedd deliberately does not LISTEN on it. Adding a notify per probe
row would put one notify per upstream per 30 s per app onto the same
`pg_notify` pipe the wake path uses. A poll carries the same information
for one indexed query per tick, and rebuilds breaker state after a schedd
restart for free.

Two properties the poll must have, both pinned by tests:

- **Never re-fold the same row.** The breaker counts *observations*, so
  re-observing one probe row on every tick would manufacture evidence and
  trip a circuit on a single real sample.
- **Ignore stale rows.** Past four probe intervals a verdict is treated as
  unmeasured, so a stalled meterd cannot freeze a circuit on evidence
  nobody is refreshing.

Per `(app_id, host_redacted_hash, port)`:

- `ok` → success. Anything else → failure. `tls_handshake` counts as a
  failure: the dependency is reachable but unusable.
- Thresholds are the §2 defaults with a longer window (default 120 s,
  `min_requests: 3`) because the probe samples at 30 s, not per request.
- **open** → schedd asks vmmd to install a reject rule for that
  `(daddr, dport)` in each live instance's netns for that app.
  `reject with tcp reset` — not `drop` — so the guest's connect fails
  *immediately* with ECONNREFUSED instead of hanging for the full TCP
  timeout. That is the entire product value: the app's own error handling
  runs in microseconds rather than after 30 s, so a dependency outage
  stops consuming the request budget and the wake slot.
- **half_open** → the rule **stays installed** and the next probe decides.
  This is the one place the design departs from a textbook breaker, and it
  departs deliberately. In a textbook breaker the half-open trial *is* a
  real request, so the gate must open to let it through. Here the trial is
  meterd's probe, which dials from the **host** while the reject rule lives
  in the **guest's netns** — the probe cannot see the rule at all. Removing
  it during the trial would therefore buy nothing and would re-expose every
  tenant request arriving in the trial window to exactly the hang this
  feature exists to remove. The guest never serves as the canary for its own
  dependency, and it never pays for the canary either.

Rule installation is `UpdateEgressCircuit`, a new `vmmd` RPC alongside the
existing netns surface; vmmd remains the only component that touches netns.
Rules are keyed by instance and torn down with the netns, so a park/wake
cycle cannot leak one — asserted by `make leakcheck`.

The RPC takes an app's **whole desired circuit set**, never a delta,
mirroring `UpdateEgressAllowlist`. This is forced by nftables: `delete
element` errors when the element is absent, so after a vmmd restart — which
re-renders the netns with an empty set while schedd still believes circuits
are open — every delete-based close would fail forever against a set that
was already in the desired state. Flush-plus-add converges from any prior
state, which makes a restart, a missed tick, or a partially-applied batch
self-heal on the next reconcile instead of needing a repair path.

Because the RPC is whole-set, a transition on one upstream re-pushes the
union of that app's open circuits; the breaker therefore tracks open
circuits grouped by app, and a failed push rolls its own view back so it
can never believe it is enforcing a rule the data plane never received.

### Resolution and the hashed-host problem

`data_upstreams` stores `host` in plaintext (it is a column on the row,
used for the probe dial) but the *customer-facing* surface exposes only
`host_redacted_hash` per §11. The breaker works off the row, so it has the
host; the API, metrics and logs continue to carry only the hash. The nft
rule is written against the resolved address, so a DNS change mid-open is
picked up at the next half-open probe.

### Surface

Per-app, not per-rule — an upstream is an app-level fact, not a route-level
one. `PATCH /v1/apps/{slug}/upstreams/{id}` gains
`circuit_breaker: {enabled, failure_threshold, min_samples, open_seconds}`,
and `GET` reports the current state and last transition. Default
`enabled: false` even when the feature flag is on: an operator flips the
flag, a customer opts an upstream in.

### Plan gating

`EgressCircuitBreakersPerApp`: Free 0 · Hobby 3 · Pro 10 · Scale 50 —
mirroring `DataPlacementHintsPerApp`, since a breaker can only exist for a
captured upstream and Free captures none.

### Rejected

- **L7 egress proxy with TLS interception.** Ship-blocking §11 violation.
  Not revisited.
- **eBPF connect-time interception.** More precise (it could break
  per-connection rather than per-destination) but adds a kernel-version
  dependency to a platform whose host contract is deliberately narrow, and
  duplicates a deny path nftables already owns.
- **A guest-side library.** Contradicts the entire premise — the point is
  to remove application code, not to ship more of it. It would also only
  work for the two runtimes we generate, not for arbitrary OCI apps.
- **Breaking on tenant traffic observation instead of the probe.** Would
  require seeing tenant connection outcomes, which means conntrack
  accounting per destination per instance. Deferred: the probe is a
  sufficient signal for the outage shape this targets, and it costs
  nothing new.

---

## 4. Rollout

Three flags, all default off, flipped per node:
`FAAS_GATEWAY_RETRY`, `FAAS_GATEWAY_CIRCUIT_BREAKER`,
`FAAS_EGRESS_CIRCUIT_BREAKER`. With all three off the tree is behaviourally
identical to pre-ADR-201 — pinned by equivalence tests on the legacy
quarantine parameters and on `GetBody` being unused when retry is off.

The egress breaker additionally requires ADR-098's `FAAS_UPSTREAM_PROBE`
to be on, since it has no signal without it. Enabling the breaker without
the probe is a startup error, not a silent no-op.

Capability-matrix entries land at `internal` and may not be promoted to
`preview` until the §14-style evidence exists: a recorded run showing a
retry converting a 502 into a 200 against a killed instance, and a
recorded run showing egress reject latency under 10 ms against a
blackholed upstream versus the unbroken TCP timeout.
