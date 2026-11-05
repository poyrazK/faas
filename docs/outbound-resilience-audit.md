# Outbound deadlines, retries and pool budgets

The November 2026 audit follows outbound client construction and retry loops in
`cmd/`, `pkg/`, `guest/` and the Go, Node and Python SDKs through their calling
contexts. A zero `http.Client.Timeout` is permitted for a stream or a call with
an enforced context deadline; it is not sufficient by itself to classify a call
as unbounded. This is a source audit, not a production load or capacity measurement.

## Corrections

| Boundary | Previous gap | Effective bound |
| --- | --- | --- |
| Ordinary and direct Postgres pools (`pkg/db`) | Startup Ping bounded only startup; later connects and pool waits could use a daemon-lifetime context | Default connect 5s; acquire at most 5s or the caller deadline; queries, batches, COPY and prepare without a caller deadline get the existing 5-minute daemon statement backstop. Completion releases their timers. Explicit query and connection deadlines are preserved. |
| Alternative pools: livequeryd, operator release/secrets commands, qualification/capacity commands, data repair, mail outbox and Neon checkpoint maintenance | Constructors bypassed the shared connection/acquire/query backstops; several inherited CPU-dependent pool maxima | Shared bounds via `ConfigureTimeouts` / `NewBoundedPool`; explicit 2-connection operator/repair pools and 4-connection livequery/qualification pools. Existing maintenance budgets remain authoritative. |
| Postgres transaction/generation conflicts and durable-entity CAS conflicts | Immediate repeated reads/writes after a definite aborted transaction or rejected CAS | Existing attempt limits retained; 25ms exponential base, equal jitter, 500ms maximum, cancellation-aware waits. Covers environment batches, source patch generations, snapshot replicas, workflow failure observations, live-query table locking, quota policies, grants and cleanup checkpoints. |
| AMQP poller | Custom dial omitted the library handshake deadline; synchronous RPCs and shutdown ignored caller deadlines | 5s dial/handshake and operation/batch bounds; cancellation closes the transport to unblock pending RPCs. A closed poller is rebuilt by the scheduler. Shutdown uses `CloseDeadline`. One connection/channel per trigger, serialized operations. |
| Environment queue delivery | An unset policy immediately requeued failures | Platform delivery backoff (5s exponential base, 5-minute cap, equal jitter); the pinned policy and existing plan/row attempt ceiling remain authoritative. |
| NATS reconnects | Fixed 2s cadence | Equal-jitter exponential reconnect delay, 2s base / 30s cap. Reconnect continues for the durable consumer's lifetime. Initial dial is 5s and fetches already have a 250ms limit. |
| Kafka reader | Retry behavior depended on library defaults | Explicit 3 attempts, 100ms–5s reader backoff; existing dial, context-bounded fetch/commit and lag-read deadlines retained. |
| Redis trigger pool | `PoolSize=4` allowed excess connections; custom TLS negotiation could outlive dial timeout | Maximum 4 active connections, 1s pool wait, context deadlines enabled, 2 retries with 25–500ms SDK backoff; TLS negotiation bounded to 5s. |
| Redis response cache | `PoolSize=8` was not an active cap | Maximum 8 active connections, 100ms operation/pool wait, 2 retries with 5–25ms SDK backoff. |
| Redis shared retry budget | CPU-dependent pool size, context deadlines disabled, automatic retry could repeat counter increments after a lost reply | Maximum 8 active connections; 100ms operation/pool wait; automatic mutation replay disabled. |
| Certificate watcher | An extra ordinary query connection was parked for LISTEN | Shared notification hub with reconnect/resync; initial setup failures use capped jittered backoff. Removes a connection from demand without increasing the fleet budget. |
| Generated Go MCP task starter | CPU-dependent Postgres pool did not follow configured worker concurrency; later pool waits and queries could use process-lifetime contexts | Worker count plus four reserved connection slots, 5s connect/acquire bounds and a 10s query fallback. Explicit query deadlines and notification stream lifetimes are preserved. |
| Generated Node MCP task starter | Fixed four-connection pool could serve up to sixteen workers plus notification and maintenance work | Worker/combined processes have worker count plus four reserved slots; web/observer processes keep four. Existing 5s connection/acquire and 10s statement bounds remain. |
| Deployment readiness / GitHub webhook proxy | A wall-clock polling limit did not interrupt HTTP; webhook proxy requests dropped the inbound cancellation context | Readiness uses a total readiness context and a 2s attempt timeout. Webhook forwarding retains inbound cancellation and has a 30s total deadline plus phase/pool bounds. |
| Replication setup (`pkg/livequery/capture.go`) | Connection/slot setup had no default deadline; a busy slot retried forever every 50ms | 5s default connect and 30s total setup; at most eight busy-slot retries, 50ms exponential base / 2s cap, cancellable. The established replication stream keeps its independent lifecycle. |
| Guarded HTTP egress | Bare transport omitted TLS/header limits and explicit DNS resolution escaped the dialer's timeout | Default 5s DNS+connect, 10s TLS, 30s headers; existing caller body/attempt deadlines and SSRF checks retained. |
| Paddle, remote SQL compiler, OAuth diagnostics, hosting smoke, realtime callbacks | Missing fallback HTTP deadlines | Existing provider timeout for Paddle; 30s compiler, 10s diagnostics/smoke/callback defaults. Caller clients are copied before defaults are applied. Writes acquire no new automatic retry layer. |
| Public object reads | Headers were bounded but a stalled transfer could retain resources indefinitely | Registry transfer timeout, matching the S3 gateway; earlier request cancellation still wins. |
| Application outbound adapter / scheduler outbound relay | Calls without a caller deadline could wait forever | 30s fallback; existing caller deadlines retain long-operation semantics. |
| AI usage settlement, export transfer, realtime owner rediscovery, GCS tombstone CAS | Immediate or non-cancellable retry waits | Existing attempt counts retained; cancellable capped backoff. GCS retirement also has the CI transfer time budget. |
| Go SDK auth/client and generated Data API client | Token requests or injected zero-timeout clients could wait forever | 10s auth fallback; ordinary SDK requests retain 30s; generated Data API client's default is 30s. SSE retains its lifetime but headers have a 30s transport bound. |
| Node SDK | No request deadline unless configured; uncapped exponential delays | Ordinary calls default to 30s including retries; maximum 9 total attempts, positive backoff and 8s exponential cap. SSE retains caller cancellation; an explicit timeout option still applies. |
| Python low-level SDK clients | Generated clients explicitly passed `timeout=None` | 30s default connect/read/write/pool timeout, also applied by the generator postprocessor. Explicit `None` remains available for streams. |
| Go / Python SDK retries | Zero/negative backoff could spin; Go duration shifts could overflow | At most 8 retries; positive default delay and saturation at 8s before arithmetic can overflow. Longer server Retry-After hints are honored; Go/Node waits remain caller-cancellable. Python surfaces cooldowns over eight seconds intact for caller scheduling, and closes retry responses asynchronously on the async path. |

## Existing controls retained

- OCI manifest requests already have a 60s default and bounded transient retries
  (3 retries, 100ms exponential base, 2s cap). Blob transfers use header/TLS and
  body-stall deadlines instead of a short whole-transfer timeout. Bearer-token
  challenge negotiation is protocol progression, not failure replay.
- Webhook delivery, durable queues/workflows, log drains, mail, billing providers,
  GitHub/GitLab clients, managed Postgres probes, workload identity, binding probes,
  certificate transparency, readiness and profiling calls already enforce client,
  operation or request-context deadlines. Durable delivery owns its retry ladder;
  adding transport retries would multiply attempts or repeat uncertain writes.
- Guest lifecycle hooks and profile collection have their own bounded contexts.
  Gateway streams, SSE, WebSockets, Postgres notifications and replication streams
  retain cancellation-controlled lifetimes. Finite RPC deadlines must not truncate
  a healthy stream. Scenario/fault-injection clients use the scenario's explicit
  budget, including cases that intentionally keep a request in flight.
- Gateway failover to a different peer, CAS snapshot re-evaluation, pagination and
  interactive input loops are distinct from replaying a failed remote mutation.
  Their existing request/attempt budgets remain in force.

## Pool and concurrency review

The daemon maxima in `pkg/db/db.go`, their direct session siblings and
`deploy/ansible/roles/postgres_capacity` form one fleet budget. apid's foreground
and background pools remain separate; LISTEN uses the hub/direct pool. No daemon
maximum is raised without pool-wait and database capacity evidence. Pool pressure
now fails within the acquisition budget instead of building an unbounded wait.

The shared deadline tracer lives in `pkg/db/poolbounds`, which has no migration
or server-wiring dependencies. The repair guest uses this leaf directly; the
daemon package delegates to it and retains abandoned-acquisition metrics.

Redis clients now set `MaxActiveConns` as well as `PoolSize`; response cache and
retry-budget consults have a 100ms latency budget, so excess concurrent calls
wait only within that budget. Trigger Redis has four slots for polling,
acknowledgement and health reads. AMQP serializes its single channel; NATS shares
one goroutine-safe broker connection; Kafka owns its reader's partition workers.

OCI idle sockets now cover its configured parallel request-setup slots (eight by
default); the transport is cloned so a caller's pool is not mutated. Active blob
connections cannot be capped to those setup slots: callers may retain multiple
layer bodies before consuming them, which would deadlock layer acquisition.
Synthetic probes retain up to sixteen idle connections per host instead of the
standard library's implicit two; the runner's semaphore owns active concurrency.

Before increasing production concurrency, inspect pool wait/canceled-acquire
metrics and the fleet capacity calculation, including replicas and overlapping
rollouts. Source-level budget alignment does not prove production saturation
headroom.

## Validation

Focused Go race checks cover shared DB deadlines, canceled acquisitions, retry
caps/cancellation, Redis pools, AMQP stalls, broker pollers, AI settlement and
state conflict/default queue retries. Full leaf-package race checks cover OCI,
certificate watching, OAuth/hosting diagnostics, durable sockets, generated Data
API clients, synthetic probes and deployment readiness. Callback and logical
replication/compiler checks also pass; replication uses a separate local
Postgres fixture with `wal_level=logical`.

The full gateway race suite passes on a sequential rerun. Existing memory and
Postgres queue-delivery contract checks also pass with explicit retry policies.
All 31 changed production Go packages, including command binaries, build with
race instrumentation.

Go SDK auth/client checks pass. Node SDK retry tests pass (33 tests), Python SDK
client/postprocessor tests pass (54 tests), and the Node MCP starter runtime
tests pass (11 tests). The Go MCP starter pool/deadline regression passes.

The whole-repository race suite remains incomplete. Initial parallel attempts
exhausted the environment's memory. Sequential runs avoided further OOMs but
the full end-to-end package exceeded its 18-minute package limit and reported
failures in a billing fixture that depends on public Stripe responses, imaged
capability prerequisites, and a policy-analysis database timing threshold
(1.84s versus 1.75s). These failures remain unresolved; the focused results
below do not establish a passing whole-repository suite. Production load and
fleet capacity are not measured by these tests.

### Repeatable saturation and load gates

`make test-outbound-resilience` now includes real Postgres pool saturation,
query cancellation and recovery, Redis saturation/recovery, and HTTP fallback
deadline/override regressions. Eight Postgres waiters must time out while two
slots are held; 32 Redis callers must fail within their budgets while all eight
slots are held. Both pools must serve requests after the reservations release.

`make test-outbound-resilience-load` requires explicit isolated service URLs
and exercises Postgres, the Redis response cache and shared retry accounting.
The full CI workflow runs 32- and 64-caller profiles. See
[the fixture/profile instructions](../tests/outboundresilience/README.md).

An initial successful pair of runs used local Postgres 16 / Redis 7.4, race
instrumentation and two Go processors:

| Concurrent callers | Operations per workload | Postgres p95 | Cache p95 | Retry accounting p95 |
| --- | --- | --- | --- | --- |
| 32 | 640 | 52ms | 7ms | 16ms |
| 64 | 1280 | 119ms | 17ms | 21ms |

Those 5,760 workload operations succeeded and met the configured 250ms p95
limit. Postgres stayed at four connections and returned every acquired slot.

Extended repeats did not pass consistently. Ten 32-caller profiles completed
19,200 operations without errors, but ten 64-caller profiles recorded 287
Redis operation failures and three Postgres p95 breaches (maximum p95 662ms).
Removing 5.2GiB of abandoned build files reduced environment memory use from
about 14.9GB to 9.4GB; a subsequent ten-profile 32-caller run still recorded 108
Redis retry-budget operation failures. Observer diagnostics confirm backend
errors; the limiting resource has not been established. The CI gate retains
the zero-error and latency requirements and runs both concurrency profiles
even when the first fails.

Pool headroom remains unverified. These synthetic measurements also do not
cover production query mixes, replica overlap or fleet capacity.
