# Application scenario tests

By default, `gregale test` deploys source into expiring developer sessions and runs
declarative HTTP checks or repository-owned assertion commands through its
public Gregale URL. Each lifecycle
profile gets a distinct run and, when requested, isolated managed PostgreSQL
databases. The CLI destroys the sessions after the test, including after an
assertion failure. The server lease is a backstop if the CLI process disappears.
Each run registers a private service namespace. A service call from a test app
can only resolve a workload in that run; a missing name cannot reach a
production app.
The runner opens the platform's account-key and public-URL auth gates on each
expiring workload before deployment. This prevents plan defaults from rejecting
customer test requests at the edge. The primary app can instead require
Gregale consumer keys with `consumer_auth_mode: required`; application-owned
authentication remains the application's responsibility.

## Scenario-scoped chaos

Real-VM scenarios can inject bounded faults into Gregale-managed HTTP service
calls, private service-name DNS lookups, and TCP connections between workloads
registered in the same test run. Rules are armed after
the selected warm, cold, or restored profile is prepared and before the
scenario trigger when declared as a top-level plan. Staged scenarios can arm
and clear plans between steps. Plans expire automatically, are removed with the
run namespace, and cannot select a production workload.

Declare repeatable rules in the scenario manifest:

```yaml
    chaos:
      duration: 5m
      rules:
        - from: worker
          to: inventory
          kind: http_status
          status_code: 503
          percent: 10
          seed: 17
        - from: worker
          to: payment
          kind: latency
          latency: 1500ms
          percent: 20
          seed: 23
```

Or run a single fault experiment without editing the manifest:

```sh
gregale chaos inject --scenario customer-export --target inventory \
  --error 503 --percent 10 --duration 5m --from worker
gregale chaos inject --scenario customer-export --target payment \
  --latency 1500ms --percent 20 --profile restored
```

`--target` and optional `--from` name workloads in the scenario. `--error`
returns a synthetic 429 or 5xx response; `--latency` delays selected calls and then
forwards them. Selection is deterministic for a supplied trace ID and seed.
Reports include the installed rules and lease expiry, while service-call
metrics and traces identify injected requests. Fault plans last from one second
to five minutes; each delay is limited to 30 seconds, and each scenario may
declare at most 16 rules.

HTTP faults act at Gregale's internal HTTP service proxy after normal
identity, tenant, binding, and target authorization. They do not alter public
HTTP or external calls. TCP faults use the private TCP service proxy described
below. DNS faults apply only to `*.svc.gregale` lookups made by a verified
member of the same scenario run; public DNS and `.internal` aliases keep their
normal behavior. Chaos plans require the `real-vm` engine; local and simulated
runs reject them. Application assertions decide whether retries, circuit
breakers, rollbacks, and customer notifications match the expected policy.

### HTTP rate limiting and Retry-After

The existing `http_status` fault accepts 429 or 500–599. For 429 and 503,
optional `retry_after` emits the standard delta-seconds `Retry-After` header:

```yaml
chaos:
  duration: 30s
  rules:
    - to: inventory
      kind: http_status
      status_code: 429
      retry_after: 1s
      percent: 100
      min_matches: 3
```

`retry_after` must be whole seconds between 1s and 30s. It is invalid for other
statuses and for latency, TCP, or DNS faults. Omitting it emits no synthetic
retry header. The API uses `retry_after_seconds`, an integer from 1 to 30.
One-off experiments accept `--error 429 --retry-after 1s`. HTTP-date values,
arbitrary response headers, and custom/malformed response bodies are not part
of this parameter. The fault retains run isolation, selection, match evidence,
expiry and cleanup, and only affects managed internal HTTP dependency calls.

Updated API and gateway versions must be deployed before using this contract;
an updated CLI alone cannot inject it on an older fleet. JSON/JUnit receipts
retain the status and delay in the rule, and HTML shows both values.

The [HTTP retry example](../examples/scenario-http-retry/README.md) verifies
three attempts at most, two measured waits of at least one second, a controlled
failure while rate limited, and first-attempt success after fault clear. It
also tests success during retries, invalid headers, and deadline cancellation.

### Malformed and truncated HTTP bodies

Use `http_malformed_json` to return synthetic HTTP 200 with invalid JSON, or
`http_truncated_body` to return synthetic HTTP 200 with fewer bytes than its
declared `Content-Length`:

```yaml
chaos:
  duration: 30s
  rules:
    - to: inventory
      kind: http_truncated_body
      percent: 100
      min_matches: 3
```

These terminal faults use fixed small JSON prefixes and do not call the
upstream dependency. They accept the existing `from`, `to`, `percent`, `seed`,
and `min_matches` fields; status, delay, retry header, and TCP parameters must
be omitted. HEAD requests skip body faults and produce no match evidence.
HTTP/1 clients observe a short-body connection close; HTTP/2 clients detect a
content-length mismatch when the stream finishes. Both faults retain normal
scenario authorization, run isolation, expiry, metrics, and cleanup.

The [HTTP body suite](../examples/scenario-http-retry/README.md#malformed-and-truncated-response-bodies)
checks bounded read-only retries, controlled errors, body-read deadlines,
positive matches, and recovery after clear. These faults do not certify
idempotency of writes or simulate an upstream commit before losing its reply.
Native execution requires updated API and gateway deployments. One-off
experiments support `--malformed-json` and `--truncated-body`, mutually exclusive
with the other fault selectors.

### Lose an HTTP response after dependency execution

`http_response_lost` forwards the authorized request through the normal route,
wake and transport policy, drains a nonempty successful response, then makes
its entity incomplete while retaining the upstream 2xx status:

```yaml
chaos:
  duration: 30s
  rules:
    - to: payment
      kind: http_response_lost
      percent: 100
      min_matches: 3
```

Unlike the synthetic body faults, the dependency actually executes. The fault
adds no forwarding attempt and does not buffer the response body. Non-2xx,
204, 205, empty responses, HEAD and upgrades pass through and produce no loss
matches. Canceled requests are not counted as injections. An eligible response
gets `Content-Length: 1` and zero bytes, so HTTP/1 and HTTP/2 clients detect an
incomplete entity. Status, delay, retry-header and TCP parameters must be omitted.

Selection is latched for each authorized request; already-selected requests
can finish after clear. Clear and expiry stop new selections. Existing request
budgets and cancellation govern upstream draining. Match evidence and metrics
are recorded after draining an eligible response, not merely selecting a rule.
Updated API and gateway deployments are required. One-off experiments accept
`--response-lost`, mutually exclusive with other fault selectors.

The [committed-write example](../examples/scenario-http-write/README.md) checks
an independent durable provider ledger: one charge across retries with the same
idempotency key and payload, conflicts that remain visible during the fault,
and reconciliation without additional charges after clear. A successful HTTP
response alone is not proof of a durable commit; the application must supply
its own business evidence and idempotency contract before retrying writes.

### Private service DNS faults

DNS rules target the scenario workload named by the private service hostname.
They are useful for checking how a caller handles a dependency name that stops
resolving or resolves slowly:

```yaml
chaos:
  duration: 2m
  rules:
    - from: checkout
      to: cache
      kind: dns_servfail
      percent: 100
      min_matches: 1
```

| Kind | Parameters | Behavior |
| --- | --- | --- |
| `dns_latency` | `latency: 250ms` | Delay a matching private DNS response, then answer normally. |
| `dns_timeout` | No extra parameters | Drop a matching query so the guest resolver follows its normal timeout and retry behavior. |
| `dns_servfail` | No extra parameters | Return DNS `SERVFAIL`. |
| `dns_nxdomain` | No extra parameters | Return DNS `NXDOMAIN`. |

`to` must name a workload declared in the scenario; optional `from` limits the
fault to one caller. `percent` selects DNS queries, and seeded selection is
available as for HTTP and TCP rules. A fault affects new lookups only: guest
or library DNS caches can continue using answers resolved before the rule was
armed. Use `min_matches` to require evidence that a lookup actually reached the
resolver. Match counts are reported with the installed plan in JSON and HTML.

One-off DNS experiments use the same run-scoped policy:

```sh
gregale chaos inject --scenario cache-resilience --target cache --dns-servfail --from checkout
gregale chaos inject --scenario cache-resilience --target cache --dns-latency 250ms --percent 100
gregale chaos inject --scenario cache-resilience --target cache --dns-timeout --duration 10s
gregale chaos inject --scenario cache-resilience --target cache --dns-nxdomain --percent 100
```

### TCP dependencies

Declare private TCP listeners with `tcp_ports` on a scenario or service. These
listeners remain internal; the workload must start them itself. Built-in
`tcp-echo` and `tcp-relay` fixtures each provide one TCP listener plus an HTTP
health endpoint. `${service.cache.host}` resolves to `cache.svc.gregale` inside
the run. `${service.cache.port}` is available when exactly one TCP port is
specified; use a literal port for a service with multiple listeners.

```yaml
version: 1
scenarios:
  cache-resilience:
    project: checkout
    source: ./checkout
    secrets:
      CACHE_HOST: ${service.cache.host}
      CACHE_PORT: ${service.cache.port}
    services:
      cache:
        fixture: tcp-echo
        tcp_ports: [6379]
    chaos:
      duration: 2m
      rules:
        - from: checkout
          to: cache
          kind: tcp_bandwidth
          port: 6379
          direction: downstream
          rate_kib_per_second: 64
          percent: 100
          min_matches: 1
    command: [node, --test, test/cache-resilience.test.mjs]
```

| Kind | Parameters | Behavior |
| --- | --- | --- |
| `tcp_latency` | `latency: 200ms` | Delay each forwarded chunk, at most 30 seconds. |
| `tcp_bandwidth` | `rate_kib_per_second: 64` | Cap throughput per connection and direction, from 1 to 1,000,000 KiB/s. |
| `tcp_timeout` | No extra parameters | Stall forwarding until lease expiry or replacement; the application owns its timeout. |
| `tcp_reset` | Optional `reset_after: 1s` | Reset the application socket immediately or after up to 30 seconds. |
| `tcp_connect_timeout` | No extra parameters | Hold a new service connection before forwarding to its target; clear or expiry resets it. |
| `tcp_connect_refused` | No extra parameters | Reset a new service connection before it reaches or wakes the target. |

Every TCP rule requires `port`. Direction defaults to `both`; `upstream` means
application to dependency and `downstream` means dependency to application.
Reset and connect-phase rules require direction `both`. Percent selects connections, not queries or
requests. Seeded selection remains fixed for an individual connection, but
parallel connection ordering and node placement can change the selected set;
use 100 percent for reproducible assertions.

These connection faults act after the guest's TCP handshake with Gregale's
service proxy. The proxy skips dialing or waking the target and simulates the
failure on first use: timeout drains bytes until the lease is cleared or expires,
while refusal sends an immediate reset. An application may see `connect()`
succeed and then receive a reset on write or read; these faults do not emulate
SYN loss or a kernel-level `ECONNREFUSED` at the initial system call.

One-off experiments use a scenario without an existing chaos block:

```sh
gregale chaos inject --scenario cache-resilience --target cache \
  --tcp-port 6379 --latency 200ms --direction downstream
gregale chaos inject --scenario cache-resilience --target cache \
  --tcp-port 6379 --bandwidth 64
gregale chaos inject --scenario cache-resilience --target cache \
  --tcp-port 6379 --timeout --duration 5s
gregale chaos inject --scenario cache-resilience --target cache \
  --tcp-port 6379 --reset --reset-after 1s
gregale chaos inject --scenario cache-resilience --target cache \
  --tcp-port 6379 --connect-timeout --duration 8s
gregale chaos inject --scenario cache-resilience --target cache \
  --tcp-port 6379 --connect-refused
```

Active pooled connections refresh their policies every 250 ms. In-flight
lookups have a 1-second deadline. Allow for that propagation interval when
arming or replacing rules on an existing connection. Expiry releases pending
bytes without requiring a new connection; a reset connection must reconnect.
Removed runs, revoked authorization, or unavailable policy reads close the
affected test sockets. Node-wide active policy routes are capped at 256.
Production connections never consult test policies. Buffers remain bounded,
and normal stream byte and idle limits still apply during faults.

JSON and HTML reports show installed rules and their expiry, not a guarantee
that application traffic matched them. Add `min_matches` to a rule when the
scenario must prove the dependency was exercised:

```yaml
        - to: cache
          kind: tcp_timeout
          port: 6379
          percent: 100
          min_matches: 1
```

At the end of the scenario or staged step, Gregale records per-rule match
counts in JSON and HTML reports and fails the scenario when a minimum is not
met. A match means one HTTP request, DNS query, or TCP connection selected for
that rule; a TCP connection counts once even when the rule affects both directions.
Counts are scoped to the run and plan generation and include gateway nodes
across the run. The `GET /v1/dev/test-runs/{run_id}/chaos/matches` API returns
the same evidence. Warm lifecycle evidence for a dependency with `tcp_ports`
records whether a new wake occurred; TCP streams do not generate HTTP request
telemetry.

### External dependencies through a relay

A relay is an isolated test service with a fixed upstream; its private listener
is the fault target. For example:

```yaml
services:
  payment:
    fixture: tcp-relay
    tcp_ports: [16443]
    upstream: api.example.com:443
    # Optional IP allowlist, enforced by normal app policy:
    # egress_allowlist: [203.0.113.8/32]
```

Configure the application's connection endpoint with `${service.payment.host}`
and `${service.payment.port}`, and target `payment` port `16443` in TCP rules.
The relay forwards opaque bytes and declares only its upstream egress port.
Plan limits and forbidden destinations/ports still apply; nonstandard external
ports require a plan with that egress allowance. Upstream addresses cannot
contain credentials or manifest references. Managed database credentials and
bindings are not changed; configure an authorized test database endpoint and
application credentials separately.

For HTTPS or database TLS, keep verification enabled and explicitly set the
TLS server name to the original upstream hostname. Connecting to the relay's
service hostname must not change the certificate name being verified.

TCP scenarios require private service TCP routing enabled on the test compute
nodes: vmmd `service_tcp_enabled`, nftables `faas_service_tcp_enabled`, and the
internal gateway's `service_tcp_listen` and `service_tcp_dns`. The runner does
not enable these fleet settings. Ports 443, 10080 and 10081 are reserved for the
HTTP mesh; relay listeners must use another port. TCP faults model established
byte streams, not SYN loss, kernel-level connection refusal, or packet loss.
Private service DNS faults are handled separately by the node-local resolver;
UDP application traffic is outside the TCP proxy's scope.

### Staged fault and recovery checks

For a complete business-level example, use the
[checkout resilience starter](../examples/scenario-checkout/README.md). It
verifies a controlled checkout error within two seconds during a payment
dependency timeout, bounded retries, catalog availability, recovery within ten
seconds, and duplicate-order prevention. It combines existing staged faults,
native JSON assertions, load budgets, and repository-owned assertion commands.
The sample uses a stateful simulated payment fixture and persistent single-writer
ledgers. Portable tests exercise lost acknowledgements and SIGKILL/restart
recovery without duplicate charges or orders. Its README distinguishes process
crash evidence from VM replacement and production payment guarantees.

Its [concurrent customer batch](../examples/scenario-checkout/README.md#concurrent-customer-batch)
adds six distinct payloads, simultaneous retries, per-key order/charge evidence,
and conflict checks for idempotency-key reuse with a different payload. Portable
tests cover an in-flight crash; the batch manifest covers managed native replacement.

Use `steps` when one scenario needs to compare baseline behavior, behavior under
a fault, and recovery in one isolated run. Workloads retain their processes
unless a restart step is declared. The first step establishes
the baseline. A later step can install a chaos plan, and a subsequent step uses
`clear_chaos: true` to remove it before asserting recovery:

```yaml
steps:
  - name: baseline
    requests:
      - {name: baseline-probe, method: GET, path: /dependency-probe, expect: {status: 200}}
    load:
      vus: 5
      iterations: 100
      thresholds:
        p95: 250ms
        p99: 500ms
        error_rate: 0.01
        success_rate: 0.99
        min_samples: 100
  - name: slow-dependency
    chaos:
      duration: 1m
      rules:
        - to: cache
          kind: tcp_latency
          port: 6379
          direction: downstream
          latency: 250ms
          percent: 100
    requests:
      - {name: fault-probe, method: GET, path: /dependency-probe, expect: {status: 200}}
    load:
      vus: 5
      iterations: 100
      thresholds:
        p95: 1s
        p99: 2s
        error_rate: 0.05
        success_rate: 0.95
        min_samples: 100
  - name: clear-fault
    clear_chaos: true
  - name: recovery
    requests:
      - {name: recovery-probe, method: GET, path: /dependency-probe, expect: {status: 200}}
    load:
      vus: 5
      iterations: 100
      thresholds:
        p95: 250ms
        p99: 500ms
        error_rate: 0.01
        success_rate: 0.99
        min_samples: 100
      relative:
        compare_to: baseline
        p95_increase_percent: 20
        success_rate_drop: 0.01
        min_samples: 100
        steps:
          dependency:
            baseline_step: baseline-probe
            current_step: recovery-probe
            p95_increase_percent: 20
            success_rate_drop: 0.01
            min_samples: 100
        retry_until_passes:
          timeout: 30s
          interval: 500ms
          consecutive_passes: 3
```

Each step can run a command and one-time `requests` or `checks`. Adding `load`
repeats that step's HTTP journey instead of issuing it once, then measures
success rate, error rate, p95 and p99 latency. Its thresholds fail the step and
appear beside the measurements in JSON, HTML, and terminal output. `min_samples`
requires enough HTTP samples before the SLO can pass; thresholds use inclusive
limits. Captures from earlier steps are available to every repeated journey.
A step's load settings are independent, so baseline, fault, and recovery can
have different budgets. Staged scenarios require the `real-vm` engine, at
least three steps, and an explicit clear followed by a recovery step. They
cannot combine with top-level `chaos`, `trigger`, `command`, `requests`,
`checks`, `wait_for`, `load`, `local`, or `simulation` fields. Fault leases
still expire on their own as a safety bound if the run stops before the clear
step.

#### Restart a staged workload

A standalone `restart` step can cold-replace the primary workload (`app`) or a
declared service after baseline. It uses the existing authenticated fresh app
restart API, submits once, waits for a valid durable completion receipt, then
polls readiness. It never restarts undeclared apps. A processing receipt alone
does not prove application health or persistence.

Declare read-only `GET` probes with successful 2xx expectations. At least one
`storage_checks` probe must assert persisted JSON values; checks run both before
submission and after readiness. Capture a meaningful storage identity and record
from an earlier request rather than relying only on an unchanged zero count:

```yaml
- name: restart-checkout
  restart:
    workload: app
    timeout: 1m
    readiness:
      name: checkout-ready
      method: GET
      path: /catalog
      expect: {status: 200, json: {'/available': true}}
    storage_checks:
      - name: preserved-order-intent
        method: GET
        path: /durability
        expect:
          status: 200
          json:
            '/ledger_id': '${steps.ledger-before-restart.ledger_id}'
            '/orders': 1
            '/outage_state': pending
```

The checkout starter includes the preceding capture and the complete sequence.
Fresh restart destroys live VM state and invalidates snapshots. `/tmp`, the
writable layer, and process memory are not preserved. Use surviving external
storage such as a database; the runner verifies the declared storage contract,
not filesystem durability. The sample keeps its order ledger in the surviving
payment fixture and restarts checkout only.

`timeout` is required, between 1s and 5m, and covers preconditions, submission,
processing, readiness, and storage verification together. Restart budgets and
load scheduling windows must fit the scenario timeout. A restart cannot share a
step with commands, HTTP journeys, load, fault installation, or fault clear.
Restart probes cannot capture new values or mutate application state. Consumer
`as` credentials are supported for primary-app probes; service probes cannot
reuse that app's credentials. Active faults remain until explicit clear or
expiry, including during restart. Failure or cancellation still runs fault and
workload cleanup. Restart may finish after cancellation; an uncertain receipt
never causes automatic resubmission.

JSON, HTML, terminal, and JUnit output include target, wake ID, processing
status, verified cold-boot method, readiness attempt count, duration, and before/after storage results.
Persisted response values are omitted. This is a managed fresh replacement,
not a timed SIGKILL primitive; portable starter tests separately cover SIGKILL
at the ambiguous-charge boundary.

An optional `restart.in_flight` starts one primary-app JSON write (POST, PUT,
or PATCH) before replacement. Declare one to five read-only checkpoints on
`app` or a declared service; each must be a successful GET with JSON assertions
and no captures. Gregale polls until all checkpoints pass and the write remains
pending, runs pre-restart storage checks, and checks again immediately before
submitting the restart. A write that ends before submission fails the step.
The background write always targets the primary app, including when replacing
a service. Service checkpoints use the service URL without primary consumer
credentials. References can use captures from preceding steps only.

```yaml
    in_flight:
      request:
        name: interrupted-write
        method: POST
        path: /checkout
        headers: {idempotency-key: in-flight}
        json: {customer: customer-one, sku: item-one, quantity: 1}
        expect: {status: 200}
      checkpoints:
        - workload: payment
          request:
            name: provider-pending
            method: GET
            path: /checkpoints/in-flight
            expect: {status: 200, json: {'/pending': true, '/committed': false}}
```

The restart timeout bounds the write, checkpoints, and all replacement probes.
The original write's final response is diagnostic; it is expected to be
interrupted and its declared expectation does not determine step success.
Recovery assertions must replay and reconcile the business result. Gregale
cancels and joins the background request on every return path. Reports record
checkpoint attempts/results, pending-at-submission, verified replacement,
joined cleanup, response status, and `request_ended`, `canceled_after_restart`,
or `canceled_on_failure`. The last two mean Gregale canceled the request; they
do not prove the replacement closed its socket. Raw bodies and payloads are
omitted. Deterministic fixtures must hold the checkpoint until recovery;
independent GET probes alone cannot atomically freeze an application.

A restart-only scenario may use baseline, restart, and recovery without network
chaos. It remains real-VM only. Every installed fault still needs explicit clear,
and recovery must follow the last restart and fault clear. The HTTP write
starter's `gregale-in-flight-test.yaml` provides a complete held-write example.
The same starter's `gregale-restart-matrix-test.yaml` declares the
`http-restart-matrix` suite: before durable acceptance, persisted pending intent,
and committed charge before acknowledgement. Each scenario starts in an isolated
workspace and uses the existing restart, suite, resource guard, and report
contracts. The provider holds the selected checkpoint until matching business
replay; conflicts cannot release it. Reports label the interruption point by
scenario name. Run the complete suite without `--fail-fast` to collect every
case; any failed or skipped case fails the suite. See the starter README for
expected ledger counts, bounds, and portable/native evidence limits.



The lifecycle profile describes the run's initial state. For a restarted service,
only its verified planned restart wake IDs are exempted from the original-profile
wake check. Other wakes still fail. Reports list planned restarts separately and
do not claim `no_new_wake` for a service that was deliberately replaced.

Use the [native checkout restart acceptance gate](scenario-restart-native-acceptance.md)
to qualify this workflow on an actual fleet, including intentional failure,
cancellation, and independent API cleanup verification. Portable harness checks
do not replace that native execution.

Use `load.relative` to compare a step's aggregate measurements with an earlier
staged step that also has `load` configured. The optional `steps` map adds
per-request comparisons: each entry maps a `baseline_step` HTTP name from the
`compare_to` stage to a `current_step` HTTP name in the current stage. In the
example, both the aggregate journey and the named `dependency` request may have
p95 at most 20% above baseline and success rate may drop by at most 1 percentage
point. Per-request `min_samples` overrides the aggregate value; if omitted, it
inherits `load.relative.min_samples`. `p95_increase_percent` and
`p99_increase_percent` are percentages; `error_rate_increase` and
`success_rate_drop` are fractions from 0 to 1. `min_samples` requires both
measurements in each comparison to meet the sample count. Comparison names,
HTTP-step mappings, checks, and measured deltas appear in JSON, HTML, and
terminal reports, and a failed comparison fails the staged step.
`retry_until_passes`
repeats the recovery load and comparison until both the regular load thresholds
and relative budgets pass, or the timeout expires. Set
`consecutive_passes` to require a sustained recovery; it defaults to 1 and must
be between 1 and 100. A failed load or relative comparison resets the streak.
Reports show the current and required streak alongside each attempt. The default
retry interval is 1s; an explicit interval must be between 100ms and 30s. Reports
include the attempt count, elapsed time, and each attempt's measured comparisons.
A retry window must exceed the load scheduling duration and fit within the
scenario timeout; retrying is capped at 100 attempts and 100,000 HTTP steps. A
`clear_chaos` step can stand on its own without a command or probe.

### Timed fault phases during one load run

Use `load.chaos_schedule` inside a staged step's `load` when traffic should
continue while faults change. The phase durations must add up to the load
duration (or the combined duration of its load stages). Start with a no-fault
phase, end with a no-fault recovery phase, and separate fault phases with a
no-fault phase. Each phase lasts at least one second; a schedule can contain at
most 20 phases and run for at most 5 minutes. A later staged step can assert
application recovery after the scheduled load:

```yaml
steps:
  - name: baseline
    requests:
      - {name: baseline-probe, method: GET, path: /dependency-probe, expect: {status: 200}}
  - name: continuous-resilience-load
    requests:
      - {name: dependency-probe, method: GET, path: /dependency-probe, expect: {status: 200}}
    load:
      vus: 5
      duration: 60s
      thresholds: {error_rate: 0.3}
      chaos_schedule:
        - name: baseline
          duration: 10s
          thresholds: {p95: 200ms, error_rate: 0.01, min_samples: 20}
        - name: slow-cache
          duration: 15s
          thresholds: {p95: 1s, success_rate: 0.95, min_samples: 20}
          rules:
            - to: cache
              kind: tcp_latency
              port: 6379
              direction: downstream
              latency: 250ms
              percent: 100
              min_matches: 1
        - name: clear-slow-cache
          duration: 5s
        - name: cache-unavailable
          duration: 15s
          rules:
            - to: cache
              kind: tcp_connect_refused
              port: 6379
              percent: 100
              min_matches: 1
        - name: recovery
          duration: 15s
          thresholds: {p95: 200ms, p99: 400ms, error_rate: 0.01, min_samples: 20}
          recovery:
            within: 10s
            sustain: 5s
            window: 1s
            thresholds: {p95: 200ms, error_rate: 0.01, min_samples: 5}
  - name: recovery-check
    requests:
      - {name: recovery-probe, method: GET, path: /dependency-probe, expect: {status: 200}}
```

Each phase replaces or clears the run-scoped plan while the load journeys keep
running. Gregale clears faults before collecting each phase's match evidence;
`min_matches` is checked independently for that phase. JSON and HTML reports
include phase start and finish times, request counts, success and error rates,
latency percentiles, and the phase's fault matches. Request metrics are assigned
to the phase in which each request started. Control-plane changes and the
gateway's policy refresh can add a small difference between planned and actual
phase boundaries; reports include the actual timestamps. Timed chaos schedules
require staged `real-vm` scenarios and cannot use `retry_until_passes`.

Each phase can declare `thresholds` with `p95`, `p99`, `error_rate`,
`success_rate`, and `min_samples`. These optional budgets apply to all HTTP
requests started in that phase and are evaluated after the load finishes.
Phase thresholds do not inherit the overall load thresholds; both sets must
pass when configured. A phase with no samples fails any configured latency
or rate budget, and `min_samples` can require enough evidence before accepting
recovery. Any phase breach fails the scenario even if the overall load meets
its budgets. JSON, HTML, and terminal output include each threshold's limit,
observed value, and result; JUnit failures identify the phase and metric.

A phase without rules immediately after a fault phase can add `recovery`.
The recovery clock starts when the control API confirms the fault was cleared.
`within` limits the time to the first healthy rolling window; `sustain` requires
all subsequent checked windows to stay healthy for that duration. `window`
defines the trailing request window. Checks run every window/10 (at least
100ms), including the deadline and the end of the sustain interval. Recovery
is measured at this check resolution, and cannot be observed before one full
window has elapsed. Recovery windows count completed requests that started in the recovery phase,
using their completion times and full latency. Pending requests contribute no
healthy samples; their results enter later windows when they complete. Empty
windows fail. Whole-phase budgets continue to group requests by start time.
These recovery budgets are separate from whole-phase thresholds, so an initial
period of slow recovery can be allowed without relaxing the final health budget.

All three durations must be whole milliseconds from 100ms to 5m. `window`
must not exceed `within`, and `within + sustain` must fit the phase duration.
Recovery thresholds require a latency or rate budget and `min_samples >= 1`
for every window. Recovery failures fail the scenario and appear in terminal,
JSON, HTML, and JUnit output; JSON also records window size, check interval,
number of windows checked, and the confirmed recovery time when successful.


The runner clears faults on assertion or transport errors as part of cleanup.
API clients can also clear an active run plan directly with
`DELETE /v1/dev/test-runs/{run_id}/chaos`; the run must still have a live member.
The [TCP example suite](../examples/scenario-tcp/README.md) exercises latency,
bandwidth, application timeout, connection reset, and staged SLO checks through
`gregale test`.

Create `gregale-test.yaml` at the repository root:

```yaml
version: 1
scenarios:
  customer-export:
    project: export-api
    source: ./gateway
    secrets:
      WORKER_URL: ${service.worker.url}
      WORKER_TEST_TOKEN: ${run.secret}
    consumer_auth_mode: required
    consumers:
      - name: customer-a
      - name: customer-b
        scopes: [read]
    services:
      worker:
        source: ./worker
        async_routes:
          - path: /process
            methods: [POST]
        secrets:
          NOTIFICATION_URL: ${service.notifications.url}/deliver
          WORKER_TEST_TOKEN: ${run.secret}
      notifications:
        fixture: delivery-sink
        fail_first: 1
    postgres: true
    buckets:
      - name: exports
        service: worker
        prefix: EXPORT_STORAGE
        permission: read_write
    timeout: 15m
    setup:
      - [node, test/fixtures/seed.mjs]
    trigger: [node, test/submit-export.mjs]
    wait_for:
      invocations:
        - service: worker
          trigger_key: worker_invocation_id
      deliveries:
        - service: notifications
          min_attempts: 2
          last_status: 200
      objects:
        - bucket: exports
          prefix: reports/${GREGALE_TEST_RUN_ID}/
          min_count: 1
          min_total_bytes: 1
    command: [node, --test, test/customer-export.test.mjs]
    simulation: [node, --test, test/customer-export.simulated.test.mjs]
    cleanup:
      - [node, test/fixtures/cleanup.mjs]
```

## Named suites

Declare suites alongside `scenarios` to run a smoke or regression selection in
one command. Members execute sequentially in declaration order. Each member can
choose an engine, a real-VM profile, and a local JSON/CSV dataset:

```yaml
suites:
  smoke:
    scenarios:
      - scenario: api-health
        engine: local
      - scenario: customer-export
        engine: local
        data: test/export-cases.json
  regression:
    scenarios:
      - scenario: customer-export
        engine: real-vm
        profile: all
      - scenario: delivery-retry
        engine: real-vm
        profile: restored
```

These names must also be declared under `scenarios`. A suite contains 1..100
unique scenario names; use `--repeat` or `profile: all` for repeated runs.
Dataset paths resolve relative to the manifest directory. Data remains local
only; use separate suites for local data cases and real-VM lifecycle checks.

```sh
# Validate every selected member and dataset without starting apps or logging in.
gregale test --suite smoke --validate

# With local.command on each member, the CLI manages app startup and shutdown.
gregale test --suite smoke --report results.json --junit results.xml

# Stop after the first failed run and cleanup.
gregale test --suite smoke --fail-fast --junit results.xml

# Override all member engines for a local development run.
gregale test --suite regression --engine local --base-url http://localhost:3000

# Preflight all real-VM members and guard the total estimated VM usage.
gregale test --suite regression --preflight
gregale test --suite regression --max-workload-minutes 180
```

Without a member `engine`, the default is `real-vm`. Explicit `--engine` overrides
every member's engine. Explicit `--profile` overrides the real-VM members'
profiles; local and simulated runs retain their own engine labels and do not
claim lifecycle evidence. `--profile` requires at least one real-VM member.
`--repeat` applies to every member: all attempts and cases/profiles finish before
the next member starts. `--scenario` and `--suite` are mutually exclusive, and
suite datasets must be declared on members rather than passed through `--data`.

The CLI checks every selected member's source, engine requirements, data fields,
and load configuration before starting the suite. `--load` requires all members
to use the local engine and applies each scenario's load settings plus CLI
overrides. `--base-url` supplies the origin for local members. Each managed local
run starts a fresh app; fixtures finish and the app stops before the next run.
The VM workload-minute guard adds all real-VM members, profiles, and repeats,
rather than granting each member a separate budget. `--preflight` requires all
members to use real VMs and checks capacity for each sequential member.

By default, assertion or cleanup failures do not stop subsequent runs. The
command exits unsuccessfully if any run fails. `--fail-fast` also works with a
single `--scenario`: after the first failure and cleanup, remaining runs are
reported as `skipped`, with a reason. Interruption likewise records remaining
planned runs as skipped. JSON stdout and `--report` contain one combined array,
with a `suite` field on each suite receipt. JUnit contains one suite with distinct
scenario/profile/case/attempt names, failures, and `<skipped>` entries. A partial
suite never exits successfully. Human output ends with passed/failed/skipped
counts. Reports continue to omit dataset values and credentials.

Add `--html` to write a standalone report that can be opened in a browser or
uploaded as a CI artifact:

```sh
gregale test --suite smoke --report results.json --junit results.xml --html results.html
```

The HTML file has no external assets. It summarizes run outcomes, phases, HTTP
steps, load percentiles, baseline checks, and platform evidence. Expand a run to
inspect its evidence or the complete JSON receipt. Values are HTML-escaped, and
the report omits dataset values and credentials just like the JSON report.

### Generate a GitHub Actions workflow

Create a starter workflow and comparison budget from a declared suite:

```sh
gregale test ci init --manifest gregale-test.yaml --suite smoke
```

When the manifest has exactly one suite, `--suite` can be omitted. The command
validates the suite and creates `.github/workflows/gregale-test.yml` plus
`gregale-test-budget.yaml`. It refuses to overwrite either file. The starter
workflow runs on pushes and pull requests for the selected `--branch` (default
`main`), and can also be started manually with **Run workflow**. Successful
pushes and manual runs on the baseline branch publish a 30-day baseline
artifact. Pull requests compare against the latest successful baseline and add
Markdown results to the GitHub job summary. A pull request can run before the
first baseline exists; comparison is skipped with a job-summary note only while
no successful baseline run exists. Once one exists, the workflow fails if its
artifact is missing or expired, or if its run is older than the configured
baseline age. To create or refresh the baseline without a code change, run the
workflow manually on the baseline branch.

By default, the baseline run can be at most 30 days old, matching the artifact's
retention period. Set `--baseline-max-age-days N` to a value from 0 to 30 when
generating the workflow to choose a shorter age limit. Set it to `0` to disable
the age check; a missing or expired artifact still fails the comparison. This
option applies to local and simulated workflows, which publish and compare
baseline reports. Baseline lookup considers successful pushes and successful
manual workflow runs on the selected branch.

The starter budget checks run outcomes and matching run identities. Add load,
HTTP, or repeat-spread limits to make performance changes gate CI. The workflow
supports local and simulated suites. Local suites must declare `local.command`;
the generator supplies throwaway consumer keys and configures Node.js when it
detects a Node command. Add other runtime and dependency installation steps to
the generated workflow as needed. These workflows use read-only `contents` and
`actions` permissions.

For real-VM suites, generate a manual profile matrix with an explicit workload
limit:

```sh
gregale test ci init --manifest gregale-test.yaml --suite acceptance \
  --engine real-vm --profiles warm,cold,restored --max-workload-minutes 135
```

The generated workflow is started with **Run workflow** in GitHub Actions. Its
profile input runs every configured profile by default, or one selected profile;
each profile gets a separate, serial job and evidence artifact. The repeat input
allows one to three independent attempts. The CLI checks `--max-workload-minutes`
for each profile job using the selected repeat count, and each job performs a
capacity preflight before execution. The command requires this limit so a
credentialed workflow cannot be generated without a cost guard. Gregale cleans
up each test environment after its profile; the workflow also retains reports
for 14 days.

The generated job uses the `gregale-test` GitHub environment by default. Create
that environment (or pass `--environment NAME`), add the Gregale API URL as the
`GREGALE_TEST_API_URL` variable, and add an API token as the
`GREGALE_TEST_TOKEN` secret. Environment protection rules can require approval
before the workflow receives credentials. Real-VM workflows are manual-only so
pull request code cannot receive those credentials automatically. The API
credentials are scoped to the credential check and Gregale CLI steps. The
workflow sets up Node.js when a scenario's trigger, assertion, setup, or cleanup
command uses a Node executable; add dependency installation and other runtimes
to the generated workflow when needed. Real-VM workflows store per-profile
evidence and do not create a comparison budget.

### Compare two saved reports offline

Compare JSON reports from separate runs without starting the application or
contacting Gregale:

```sh
gregale test compare before.json after.json
gregale test compare before.json after.json --html comparison.html
gregale test compare before.json after.json --markdown comparison.md
gregale test compare before.json after.json --json
```

The command pairs runs by scenario, engine, profile, case, and attempt. It shows
added and removed runs, status changes, duration and phase changes, HTTP step
timings, and aggregate, per-step, and arrival-rate load metrics when available.
It also marks a changed load workload signature or label so those metric deltas
are easy to spot.
Without `--budget`, run-outcome and performance comparisons are informational.
Opt-in [behavior observations](#compare-declared-application-behavior) have a
separate gate. Pass a versioned YAML budget file to enforce run outcomes and
optional performance limits:

```sh
gregale test compare before.json after.json \
  --budget ci/test-budget.yaml --html comparison.html
```

```yaml
version: 1
runs:
  require_passing: true       # default: every later run must pass
  require_same_runs: true     # fail if the report adds or removes a run
  max_duration_increase_percent: 20
http:
  max_duration_increase_percent: 25
load:
  max_p95_increase_percent: 10
  max_p99_increase_percent: 15
  max_error_rate_increase_percentage_points: 0.5
  min_samples: 50
repeat:
  min_runs: 3
  max_spread_percent: 25
  max_error_rate_spread_percentage_points: 1
```

Run duration and each HTTP request duration use the configured percentage
limit. HTTP steps must keep the same name, method, and path. Load limits apply
to aggregate and per-step percentiles; error-rate increases are absolute
percentage points (`0.5` allows an increase of half a point). Load budgets
require matching versioned workload signatures and labels, valid reports, and
at least 20 samples in each report by default. A workload change, missing
comparison step, or too few samples is reported as inconclusive and fails the
gate so changed traffic cannot silently pass. A zero latency baseline permits
no increase.

Add `repeat` to compare repeated attempts as a group. Performance checks use the
median of each attempt's reported metric; Gregale does not average percentiles
or combine samples from separate runs. The default maximum spread limits are
25% for latency metrics and 1 percentage point for error rates. Latency spread
is reported as `100 * (maximum - minimum) / median`; error-rate spread is the
range in percentage points.
A group below `min_runs`, an undefined zero-median spread, or a group over
either spread limit is inconclusive. `runs.require_passing` still checks
each later attempt, and `runs.require_same_runs` still requires matching
attempt identities.

When a budget fails or is inconclusive, the command returns a nonzero exit code
after writing the requested JSON, HTML, or Markdown comparison report. The
Markdown summary shows the gate, metric medians and spreads, run changes, and
inconclusive reasons; it lists up to 100 checks and runs. In GitHub Actions,
`--github-summary` appends the summary to the file named by
`GITHUB_STEP_SUMMARY`. Unknown YAML fields and unsupported budget versions are
rejected. The checked-in
[`scenario test workflow example`](../examples/scenario-tests/README.md#github-actions)
shows how to compare each pull request with a saved `main` artifact and upload
the comparison for review. Use `--baseline` during a load run when you want its
in-run regression limits to gate that execution directly. Inputs are local
JSON report arrays written by `--report` or JSON stdout, capped at 64 MiB each.

### Compare declared application behavior

Opt a scenario into selected outcome collection with `behavior`. This supplements
the normal HTTP/assertion results with application-reported values that
`gregale test compare` can compare across executions:

```yaml
behavior:
  version: 1
  fixtures: [test/input.json]
  observers: [test/observe.mjs]
  observations:
    - key: export.response
      kind: http
    - key: export.report
      kind: object
    - key: notification.successful_deliveries
      kind: delivery
```

Paths are relative to the primary scenario's `source`. Declare 1–32 fixture
files and 1–32 observer files, each a regular file of at most 64 MiB that resolves
inside that source directory. Declare 1–100 uniquely keyed observations. Keys
start with a lowercase letter and contain lowercase letters, digits, `.`, `_`,
`/`, or `-`, up to 128 characters. Supported kind labels are `http`, `delivery`,
`object`, `database`, and `application`; labels describe application-reported
values rather than enabling a database reader or integration adapter.

The runner passes a fresh private `GREGALE_TEST_BEHAVIOR_OUTPUT` path to scenario
commands. An assertion command, staged command, or simulation writes one complete
version 1 JSON document there before it exits:

```js
import { writeFile } from "node:fs/promises";

await writeFile(process.env.GREGALE_TEST_BEHAVIOR_OUTPUT, JSON.stringify({
  version: 1,
  observations: [
    { key: "export.response", value: { status: 202, created: true } },
    { key: "export.report", value: { sha256: reportDigest, bytes: reportBytes } },
    { key: "notification.successful_deliveries", value: successfulDeliveries },
  ],
}), { mode: 0o600 });
```

Every declared key must appear exactly once with an explicit JSON value; `null`
is a value, while an omitted value is invalid. Output may not add observations,
fields, or a provenance claim. The format is bounded to 1 MiB and 64 nested JSON
containers. Unknown versions, duplicate JSON keys, invalid encoding, and malformed
or missing output fail the run. Keep credentials, personal data, and unneeded
payloads out of selected values: these values are deliberately retained in JSON,
HTML, terminal, and Markdown comparison artifacts.

The receipt records the selected values with `provenance: application_reported`.
It fingerprints the declared fixture files, observer files, observation schema,
and operation recipe (setup, trigger, commands, native requests/checks, staged
steps, wait conditions and load settings). Local case data contributes to the
fixture fingerprint. The application source and runtime configuration under
comparison are allowed to differ. Declared files are checked again when evidence
is collected; edits during execution invalidate the evidence. Collection happens
before runner-owned fixture cleanup, including on assertion failures, and private
output files are removed afterward. Failed commands remain failed even if they
produced valid evidence. Simulations retain their `simulated` engine label.

Comparisons match the existing scenario/engine/profile/case/attempt identities.
Object key ordering and equivalent JSON number spellings compare equally without
floating-point precision loss. Arrays preserve ordering and duplicate entries.
Select or normalize volatile IDs, timestamps, and intentionally unordered values
in the declared observer, identically for both runs; changing that observer makes
the comparison inconclusive.

When either report contains behavior evidence, missing evidence, a changed
contract or fixture fingerprint, or failed execution/cleanup produces an
`inconclusive` comparison. Changed selected values produce `failed`; equal values
produce `passed`. Failed and inconclusive behavior comparisons return a nonzero
exit code **even without a performance budget**. Legacy reports without behavior
evidence keep their existing informational comparison behavior. JSON, terminal,
HTML, Markdown, and GitHub job summaries include the behavior results. Displayed
values are bounded; the full selected values remain in JSON evidence.

This version supports ordinary local, simulated, and real-VM runs, including
normal `--repeat`. Resilience/campaign expansion is rejected when `behavior` is
declared until nested run comparison is supported. This collection contract
does not attest that an application used every declared fixture or that all
dependencies or external effects were observed. Observer imports and other input
files are pinned only when explicitly declared. It does not capture production
traffic, clone production data, intercept arbitrary outbound actions, or approve
intentional differences. The `rehearse` command below orchestrates two Git
revisions using this collection contract.

The [behavior comparison acceptance example](../tests/scenario-acceptance/behavior/README.md)
runs the existing export API and worker with in-memory storage and a notification
sink. Both implementations pass the same response and export checks; the
candidate duplicates a successful notification after a retry. Comparison shows
the unchanged response/report and the change from one to two successful
deliveries. This portable example is simulation evidence, not live-fleet VM or
object-provider qualification.

### Rehearse two Git revisions

Run one reviewed behavior scenario against two locally available Git revisions:

```sh
gregale test rehearse \
  --base main --candidate HEAD \
  --manifest gregale-test.yaml --scenario customer-export \
  --engine simulated \
  --report rehearsal.json --html rehearsal.html --markdown rehearsal.md
```

Both refs resolve once to immutable commits before execution. The selected
manifest is read from the invoking checkout, and the declared fixture and
observer files are captured once and overlaid into both committed source trees.
Declare shared harness scripts and observer imports in `behavior.observers` when
they must stay fixed across revisions. All other application files, configuration
and migrations come from the selected commits; dirty application files are
excluded. Git archive attributes apply. The checkout, index and branch remain
untouched; no worktree or remote fetch is created.

`--repo DIR` selects the local checkout (default `.`). `--manifest` is relative to
the invoking directory, even with `--repo`. The manifest and all source directories
must be inside that checkout. Snapshots use the existing source archive bounds:
250 MiB compressed, 625 MiB expanded, 256 MiB per archived file, and 10,000 archive
entries. Symlinks, submodules and non-regular files fail preparation explicitly.
The declared behavior files retain their 64 MiB per-file bound. Both revision
snapshots are validated before application execution or platform provisioning.

Every revision/profile/attempt gets a fresh source directory. Real-VM runs also
get the existing isolated, expiring apps, services and supported data resources.
Use `--repeat N` for 1–20 fresh runs per revision and lifecycle profile. Real-VM
execution is the default, with `--profile warm`; cold, restored and all are also
supported. It requires a workload-minute limit covering **both revisions**:

```sh
gregale test rehearse --base main --candidate HEAD \
  --scenario customer-export --profile restored --repeat 2 \
  --max-workload-minutes 180 --report rehearsal.json
```

Local rehearsals require a managed `local.command` so each revision starts its
own application. A shared `--base-url`, CLI data files, suites, load overrides and
resilience/campaign expansion are outside this first command's scope. Use declared
fixture files and scenario setup to populate fresh state. Simulations own their
fixture startup and cleanup and retain their `simulated` label. Host-side commands
run with the invoking user's permissions and environment; fresh directories do
not sandbox hostile code or automatically block external actions. Supply trusted
recipes and suitable test credentials. Unix command cancellation stops the active
process group; ordinary scenario cleanup and hosted resource expiry still apply.

Use `--validate` instead of output flags to prepare both snapshots without running
commands, logging in or provisioning resources. JSON mode prints the resolved
revision and input identities. Execution requires `--report`; all requested output
files must be new and distinct, and are reserved before any application runs.

The version 1 combined JSON receipt includes:

- Baseline/candidate commit and tree IDs, archive digests, and materialized-source
  digests covering the source files after the trusted overlays.
- The original manifest digest, selected recipe digest, and declared trusted file
  paths/digests. Fixture contents and secret values are not copied into metadata.
- `baseline_runs` and `candidate_runs`, including normal lifecycle, deployment,
  assertion, observation and cleanup evidence, plus the behavior comparison.

HTML and Markdown comparisons show the same revision and input identities.
Changed selected behavior returns exit 1. Failed execution, missing or incompatible
evidence, interruption, or failed cleanup prevents equivalence and returns exit 1.
All scheduled slots remain explicit, including those skipped after interruption.
Source digests identify configuration and migration files; they do not establish
that a migration ran or every fixture was applied. External action interception,
production snapshots and intentional-change approval remain separate capabilities.

The [export acceptance example](../tests/scenario-acceptance/behavior/README.md#two-committed-implementations)
compares two real commits and detects successful notifications changing from one
to two while the selected response and generated file stay equal.

### Capture managed webhook actions

Real-VM scenarios, including `gregale test rehearse`, can capture managed webhook
intents by declaring:

```yaml
actions:
  webhooks: capture
```

The runner requests a durable policy for every isolated workload before deploying
source, and records `actions: {webhooks: capture, policy_version: 1, status:
applied}` only after the server acknowledges it. Registration requires fresh
developer-session apps with no deployments or existing work. Older servers that
ignore the request, omit evidence-version support or return the legacy 204 response cannot satisfy this gate;
the runner stops and cleans up its sessions. Local and simulated engines reject
this option because they cannot enforce the platform dispatcher boundary.

Complete a supported managed HTTP operation or managed workflow step using the
[managed operation effects contract](managed-operation-effects.md). Captured
effects retain their stable identity, receiver ID, event type, bounded payload,
and rehearsal run ID in Gregale's completion transaction. Normal receiver and
customer authorization checks still apply. Inspect through:

```sh
gregale --json operations get OPERATION_ID
gregale --json workflows attempts WORKFLOW_RUN_ID STEP_NAME
```

Captured records have `status: captured`, `rehearsal_run_id`, zero attempts and no
delivery ID. Result completion does not contact the receiver. Expiry, namespace
cleanup and manual delivery retry do not restore outbound authority. Ordinary
webhook events associated with these apps are retained as suppressed `dead`
deliveries with a rehearsal reason. They indicate unsupported event coverage;
they are not successful business effects. Older completion workers can produce
these suppressed rows instead of the new captured-effect status, so upgrade API
and workers for captured intent inspection.

Before fixture cleanup, the runner waits up to ten seconds for managed operations,
workflows and invocations to settle, then seals an account-owned action snapshot.
Sealing closes subsequent managed-effect writes. The receipt includes the sealed
inventory under `actions.evidence`, with provenance
`gregale_captured_managed_webhooks` and scope `managed_operation_webhooks`.
Retries can retrieve that same snapshot after namespace cleanup or soft deletion;
physical purge of its anchor app/account removes retained evidence.

`test rehearse` and `test compare` compare the captured intents independently of
declared `behavior` observations. Each logical workload/source/effect name is
compared by destination configuration fingerprint, event type and exact canonical
JSON payload. Workflow name and step are included. Generated operation/receiver
IDs and completion order do not affect comparison; repeated notifications remain
separate intents. For example, unchanged application observations with one
captured notification before and two afterward produce a failed action comparison
and a nonzero command exit status. Console, JSON, HTML and Markdown show this
separate result. Declare a `behavior` contract to supply the trusted recipe,
observer and fixture hashes; those identities must match. Capture-only runs
still retain their action inventory, but cannot establish comparable input
identity without that contract.

Missing, pending, unsealed, legacy or partial evidence is **inconclusive**.
The version 1 format supports 256 intents, 512 KiB of aggregate stored payload
and a 1 MiB document. Exceeding these bounds seals an incomplete inventory without
a partial intent list. A collection failure fails the run; execution or cleanup
failure prevents a passing comparison. Upgraded completion workers must record
receiver fingerprints. Ordinary suppressed lifecycle events are counted, without
being treated as successful managed business actions.

Servers with event capture (`event_capture_version: 1`) also seal two more kinds
of evidence:
- **Published events.** Every event an enrolled app publishes through the in-guest
  event endpoint is captured into the run and never enters the account's event
  bus, in both capture-only and contained rehearsals. It must pass the same
  envelope and schema validation as a production publish. Events compare as a
  multiset per workload, source and type, by schema version and canonical data.
  A second publish of the same event fails the comparison; event IDs and times
  never count.
- **Suppressed ordinary webhook events.** These compare by count per workload and
  event type. Their payloads contain platform-generated identifiers, so they do
  not compare.

Events published after sealing are refused. More than 256 events, or event data
over 64 KiB, make the evidence incomplete. A comparison between servers with
different event capture versions is inconclusive. See
[ADR-20261020090000](adr/20261020090000-rehearsal-event-capture.md).

This setting covers Gregale-managed webhooks. Direct application HTTP calls,
email/payment SDKs and workload network containment are separate capabilities.
A passing action comparison applies to the declared scope; existing `behavior`
observations remain application-reported. Hosted/native qualification requires
the supported acceptance hosts. See the
[sealed comparison decision](adr/20261017201004-sealed-rehearsal-action-comparison.md).

Platform work done on behalf of an enrolled app stays inside the run as well,
in both capture-only and contained rehearsals:
- Account-wide alert rules do not count the app; app-scoped rules still do.
- Its route-discovery events do not fan out to other apps.
- Workflow outbound steps are refused, so they never use live provider credentials.
- Log drains, deployment-failure mail and Kafka/NATS/Redis Streams/SQS triggers do not run for it.
- Its crons never fire from the clock. To exercise one, send the cron's request or run its command from the recipe, so both revisions see the same work ([ADR-20261020200000](adr/20261020200000-rehearsal-crons-fire-from-the-recipe.md)).

Contained enrollment refuses an app that already holds real managed PostgreSQL
or object-storage bindings; declare the isolated fixtures below instead. Builds
of the candidate revision keep ordinary builder egress, so a receipt makes no
claim about build-script side effects. See
[ADR-20261019180000](adr/20261019180000-rehearsal-side-effects-stay-inside-the-run.md).

### Contain serving-VM outbound traffic

Rehearsal containment version 1 is an operator-gated extension:

```yaml
actions:
  webhooks: capture
  network: contained
```

The complete serving fleet must run the new scheduler, vmmd and internal gateway
code, with database-backed runtime policy reads. After native qualification,
the operator sets `FAAS_REHEARSAL_NETWORK_CONTAINMENT=1` on every serving vmmd
(which turns on its durable policy reads) and then on apid (which enables
enrollment). It is disabled by default. A vmmd without the flag makes no
rehearsal reads on ordinary wakes and resumes and refuses contained boots
([ADR-20261019120000](adr/20261019120000-rehearsal-runtime-hot-path-isolation.md)). Registration must echo `network_mode: contained`
before the runner deploys anything; every VM must also acknowledge the exact run
before the scheduler accepts its boot. An older runtime refuses the dedicated
boot methods. Policy read or firewall installation failures stop boot/resume.

This profile allows published app requests and private HTTP calls to active,
contained members of the same run through TCP 10081 (for example,
`http://worker.svc.gregale:10081/import`). Rehearsal DNS is transparently
redirected to a private-only resolver, including before the app becomes ready;
it never uses a public upstream. Direct HTTP/HTTPS, SMTP, public DNS, private-network
routes, direct service TCP, aliases, development tunnels and operator egress
exceptions are blocked. Host-mediated live outbound relay, federated identity and
guest event publishing are refused as well; the IP firewall alone cannot
contain those channels.
Database and object-store access require future isolated adapters. Ordinary app tasks,
live migration, managed application-standard native admission and GitOps
qualification remain unsupported for contained apps. Explicit
[contained rehearsal commands](#execute-rehearsal-commands-in-microvms) extend the
boundary to deployment-attached command VMs. A candidate's own release command,
for example a database migration, runs the same way: it executes as the run's
pinned contained command against the isolated fixtures before the deployment
primes. This requires `FAAS_REHEARSAL_COMMAND_EXECUTION=1` on imaged and apid;
without it, the candidate fails with `release_phase_unavailable` rather than
waiting. Builds, other job VMs and standalone Runs remain outside this boundary.

The receipt records `actions.network: contained`; comparisons with a different
network policy are inconclusive. Blocked requests are not simulated or captured
as business intents. Without simulation fixtures, the sealed action snapshot
compares managed webhook intent. A passing result does not establish complete asynchronous drain
or coverage of every integration.

Run `scripts/ci/rehearsal-network-acceptance.sh` on the dedicated native x86_64
Linux KVM host before enabling enrollment. See the
[containment decision and rollout boundary](adr/20261017201005-rehearsal-network-containment.md)
for supported paths, required assets and lifecycle acceptance. Portable checks
and metal compilation do not qualify a production fleet.

### Simulate HTTP and email actions

Contained serving apps can use declared fixtures through the managed outbound
relay. Simulation enrollment additionally requires the default-off
`FAAS_REHEARSAL_ACTION_SIMULATION=1` on apid after native qualification. Add this
to a scenario whose main workload is `import-api`:

```yaml
actions:
  webhooks: capture
  network: contained
  simulation:
    version: 1
    fixtures:
      - workload: import-api
        name: notification
        integration: email
        adapter: resend
        method: POST
        path: /emails
        capture_fields: [to, subject]
        responses:
          - fault: timeout
            delay_ms: 200
          - status: 200
            body: '{"id":"simulated-email-1"}'
      - workload: import-api
        name: import-service
        integration: imports
        adapter: http
        method: POST
        path: /imports
        capture_fields: [order_id]
        responses:
          - status: 429
            retry_after_seconds: 1
            body: '{"error":"rate_limit"}'
          - status: 200
            body: '{"ok":true}'
```

The application calls `GREGALE_OUTBOUND_URL + "/i/email/emails"` for the Resend
wire API, or configures a compatible integration client's base URL as
`GREGALE_OUTBOUND_URL + "/i/email"`. Supply fixture request content and a stable
`Idempotency-Key`. These logical integration names do not require a live provider
configuration. The simulator has no provider transport or credentials.

The HTTP adapter accepts bounded JSON-object requests at exact declared routes.
The Resend adapter requires `from`, `to`, `subject`, and `text` or `html` on
`POST /emails`. It returns the explicit JSON response in the fixture. Headers,
query strings, SMTP and direct calls to provider URLs are outside this adapter.
Other integrations can use declared HTTP fixtures within this contract; this is
not a claim of complete provider or SDK compatibility.

Response lists advance separately for each fixture and idempotency key; the
final response repeats. Without a key, calls share that fixture's sequence.
Timeout fixtures wait for the bounded delay then close the connection; the
application's deadline determines whether it observes timeout or connection
closure. Disconnect faults close immediately unless a delay is declared.

Registration negotiates action evidence version 2 and acknowledges the canonical
fixture digest before deployment. Reports use scope
`managed_webhooks_and_simulated_outbound` and keep webhook intents and simulated
attempts distinct. Every attempt retains its ordinal, hashed idempotency key,
canonical request digest, declared payload fields and injected outcome. Exact
request digests also compare fields excluded from payload display; no arbitrary
headers or unselected body content are stored. Selected nested fields are retained
in full. Runtime IDs and interleaving between independent groups are ignored.

Unknown or invalid relay calls make evidence incomplete. Storage errors refuse
admission; interrupted requests and protocol overflow cannot pass. Sealing waits
for pending simulations. Recipes must also wait for application completion:
arbitrary timers and external queues have no general drain guarantee, and late
calls are refused after sealing. An extra simulated attempt is evidence of an
extra request, not proof that a real provider delivered an email twice.

Simulation registration also requires `recorder_version: 1` acknowledgement
before deployment. vmmd opens a durable recorder before each serving VM starts
and records each host relay request before contacting the broker. Sealing first
waits for supported managed work, then fences new relay admission and requires
the owning recorders to shut down their HTTP connections and acknowledge all
request receipts. A missing owner, rejected broker connection, or storage failure
cannot establish a passing empty inventory. Pending receipts exhaust the bounded
collector and produce inconclusive evidence; failed sessions make it incomplete.
The snapshot includes recorder version, session count and pending/incomplete
session counts. Keep the tested VM alive until collection completes; retiring
it before its closing acknowledgement currently makes the rehearsal incomplete.

This covers requests received by the host relay. Assert application and
integration outcomes in the recipe, including client failures before that
boundary and expected injected faults. General external queues and timers still
need application completion checks. Host shell commands and separate job/Run
executions do not acquire this serving-VM recorder coverage. See the
[recorder session and closing decision](adr/20261017201007-rehearsal-recorder-sessions.md).

Bounds are 64 fixtures/64 KiB per plan, 16 responses per fixture, 8 KiB response
bodies, 2-second delays, 64 KiB request bodies, 256 attempts, 256 recorder
sessions and 256 request obligations per run. Existing
combined payload and snapshot bounds still apply. Enrollment remains disabled
until the native qualification gate passes. See the
[outbound simulation decision](adr/20261017201006-rehearsal-outbound-simulation.md).

### Execute rehearsal commands in microVMs

Opt in on a behavior scenario that already declares contained action simulation:

```yaml
command_execution:
  version: 1
  runtime: contained
  env: {IMPORT_MODE: fixture}
command: [node, tests/import-and-observe.mjs]
checks:
  - name: serving-app
    method: GET
    path: /status
    expect: {status: 200}
```

Use `--engine real-vm --profile warm`, or the separately qualified cold profile
below. Registration requires `command_version: 1`
before configuration or source deployment. The operator must first qualify and
set `FAAS_REHEARSAL_COMMAND_EXECUTION=1`, in addition to the containment,
simulation and ordinary app-task gates. All remain opt-in; portable tests do not
qualify the native runtime.

Every `setup`, `trigger`, assertion `command` and `cleanup` runs in a fresh
app-task microVM pinned to that run's deployment. There is no host fallback.
The reviewed `behavior.fixtures` and `behavior.observers` are overlaid as regular
files beneath the app working directory. Each command starts with the deployment
filesystem; changes made by a preceding command do not persist. Supply the whole
observation document from the final observer command.

The command inherits the deployment-scoped environment and sealed secrets, plus
explicit `command_execution.env` values and generated `GREGALE_TEST_*` variables.
The CLI host environment and account token are not forwarded. Explicit values
and input files are retained in task intent; keep credentials in the existing
sealed deployment environment. `GREGALE_TEST_URL` and service URL variables name
same-run private HTTP services, for example `http://import-api.svc.gregale:10081`.
The behavior and trigger output variables point to guest-private files. Their
bounded contents return in the command receipt and feed the existing validators.
These observations retain application-reported provenance.

Version 1 supports warm and explicitly enrolled cold runs with a native HTTP request or check for serving-app
lifecycle evidence. Local/simulated engines, staged steps, resilience, load, TCP,
ordinary Postgres bindings, buckets, consumer credentials and token-bearing built-in fixtures are refused.
The explicit PostgreSQL fixture adapter below has its own containment and evidence
contract. The object fixture adapter below is also explicit. Restore/resume command
comparisons remain unsupported. Ordinary scheduler concurrency, RAM and account quotas apply to the
additional VM; the workload-minute estimate includes its capacity and cleanup.

Inputs are limited to 32 files/8 KiB and a 15 KiB command envelope. Combined
stdout/stderr is bounded to 8 KiB, behavior output to 24 KiB, trigger values to
4 KiB, and the receipt to 64 KiB. Overflow, malformed or foreign receipts, missing
completion, cancellation and timeout cannot pass. The JSON report's `commands`
list records each task, stage, deployment, input/receipt hashes, exit and bounded
output. Output hashes bind receipt bytes; they do not compare runtime identifiers
between revisions.

A leased command network identity enables private calls without making the task
VM a serving target. It is revoked before address release and on vmmd restart.
If revocation storage is unavailable, cleanup retains the address reservation.
Exit zero alone is insufficient: vmmd must stop relay admission, join handlers,
confirm VM cleanup and persist the exact command retirement receipt before the
scheduler can mark success. Sealing also counts queued and failed commands, so a
missing recorder cannot become a passing empty action inventory. Behavior is
collected before cleanup commands; action sealing follows their completion.
Detached processes are terminated with the command VM. The explicit managed-worker
adapter below tracks supported async work; other delegation still needs recipe
completion checks and assertions for failures before the host relay.

See [ADR-20261017201008](adr/20261017201008-contained-rehearsal-commands.md) for rollout and native
qualification requirements.

### Observe isolated PostgreSQL effects

Add `postgres_fixture` to a warm, contained command scenario:

```yaml
postgres_fixture:
  version: 1
  fixture: tests/import-fixture.sql
  max_lock_wait_ms: 250
  tables:
    - schema: public
      name: imports
      columns: [external_id, state]
```

The SQL file is separate from `behavior.fixtures`; it is executed by the platform
under a generated, unprivileged database login. It is not uploaded as command
argv or executed on the CLI host. An example fixture is:

```sql
CREATE TABLE public.imports (external_id text NOT NULL, state text NOT NULL);
INSERT INTO public.imports VALUES ('seed-item', 'ready');
```

Gregale creates a fresh database and login for each revision before deployment,
captures the initial selected rows directly using the restricted login, and injects `DATABASE_URL` into the
deployment's sealed ephemeral secrets. Commands and serving instances of the
primary workload share this run's database. Explicit `DATABASE_URL` overrides
are refused; sibling services receive database credentials only through explicit
`managed_work` capabilities, described below.
The private PostgreSQL proxy accepts only a live same-run
execution and the generated database/login. It has no arbitrary destination,
production URL or managed-binding fallback. Fixture SQL must contain sanitized
data and no credentials. `test rehearse` pins the trusted SQL file alongside the
recipe and observer inputs; both sides must have the same fixture hash and the
same independently observed initial-data hash.

After commands finish and supported actions seal, the collector fences new
connections, disables the database, checks for unfinished SQL, terminates
retained connections, captures final committed rows and confirms database/login
deletion. Recipe `cleanup` commands are refused for this adapter; Gregale owns
fixture cleanup. A lost creation response still triggers cleanup. Durable
journals expire after 15 minutes and are reaped when the collector is running,
including with new fixture admission disabled. Journals pin the cluster and local endpoint; a changed backend cannot acknowledge cleanup of an older fixture. Active journals block owner purging until cleanup is confirmed. A new process can clean old
resources but cannot adopt their observations into a passing receipt.

The report's `postgres` section has `platform_postgres_collector` provenance,
run/app/deployment identities, fixture/contract/initial-data hashes, initial and
final rows, inserted/deleted row multisets, sampled lock waits and confirmed
cleanup. Duplicate rows retain their multiplicity. An update appears as one
deleted row and one inserted row. JSON, text, Markdown and HTML comparisons have
a separate PostgreSQL result; any changed row effects or exceeded lock budget
fails comparison. Missing evidence, different starting data, a failed command,
unfinished SQL, collector loss or unconfirmed cleanup is inconclusive.

Version 1 supports one fixture database per run on a dedicated, local PostgreSQL
16+ cluster, 1–16 ordinary persistent `public` tables, 1–32 selected columns per
table, and at most 1000 rows per table. Declared tables must be ordinary
persistent tables whose selected columns have built-in scalar types; other
relations (up to 64 in total) and columns appear in the schema inventory below.
Forced row-security filtering and additional extensions are refused. Select only stable, non-sensitive columns; omitted
columns, sequence counters, schema/index differences and intermediate statements
are outside the comparison scope. SQL fixtures are bounded to 64 KiB; each
snapshot to 256 KiB and the full evidence document to 1 MiB. Overflow cannot pass.
The collector samples lock waits approximately every 50ms; short waits may be
missed. A command or startup migration failure remains a failed execution with
its existing task/deployment diagnostics.

A deployment's release command runs against this fixture as the run's contained
release, before the candidate primes (see
[ADR-20261020150000](adr/20261020150000-rehearsal-contained-release-commands.md)).
Declared tables compare row by row.

The collector also seals a schema inventory
([ADR-20261020180000](adr/20261020180000-rehearsal-postgres-schema-inventory.md)). It covers every relation (with column types), index, constraint, user type, function and trigger, and a bounded row count for each undeclared table. A migration that creates tables, types or indexes is therefore compared as `schema_changes` rather than refused.

Two limits remain:
- Extensions, views replacing declared tables and forced row security are still refused.
- An intended migration still reports `failed`, with the exact schema differences listed. Arbitrary detached work and external
queues still require explicit recipe completion checks.

The adapter is **disabled pending dedicated native qualification**. Operators
must use a separate disposable cluster with a `gregale_rehearsal_admin` database,
authenticated local TCP access, `max_prepared_transactions=0`, and no production
or platform databases. Set `FAAS_REHEARSAL_POSTGRES_ADMIN_URL` only on apid; it
must explicitly select that database and a loopback address with
`sslmode=disable`. Configure
`FAAS_REHEARSAL_POSTGRES_PROXY_TARGET=127.0.0.1:PORT` on the same node's private
gateway. After qualification, `FAAS_REHEARSAL_POSTGRES_FIXTURES=1` on apid and the
gateway enables admission in addition to the existing rehearsal gates. Port
10083 is private on the service bridge; plaintext PostgreSQL stays on the local
fixture path and inside the VM network boundary. Other placements, TLS-required
clients, protocol cancellation packets and production snapshots are unsupported.
Keep the admin URL configured during draining so expired journals can be cleaned.

Portable SQL acceptance uses an explicitly disposable fixture cluster:

```bash
FAAS_REHEARSAL_PG_TEST_ADMIN_URL='postgres://USER:PASSWORD@127.0.0.1:PORT/gregale_rehearsal_admin?sslmode=disable' \
  go test -race -count=1 ./pkg/rehearsalpg/...
```

This acceptance does not qualify KVM, private bridge isolation, guest secret
delivery or native VM cleanup. Native qualification must exercise the contained
import command, both revisions, cross-run/database/login rejection, lock waits,
cancellation, collector failure, cleanup failure and leakcheck before enabling
the gate. See [ADR-20261017201009](adr/20261017201009-rehearsal-postgres-fixtures.md).

### Observe isolated object-storage effects

Add `objects_fixture` to a warm, contained command scenario that already has
behavior observations and action simulation:

```yaml
objects_fixture:
  version: 1
  bucket: imports
  fixtures:
    - key: customers/seed/source.csv
      file: tests/source.csv
      content_type: text/csv
      metadata: {owner: seed-customer}
```

Use `fixtures: []` for an empty starting bucket. Use sanitized files without
credentials; they may contain binary data.
They are read once before allocation, cannot traverse symlinks, and are separate
from the command input files and their 8 KiB bound. `test rehearse` captures the
trusted fixture bytes with the recipe and supplies them to both revisions.

Namespace creation waits for the qualified private gateway to acknowledge ownership
before deployment. Replacing or losing that collector makes an old capture window
inconclusive, including when no mutation rows were recorded. The initial rollout
requires a single qualified private gateway placement.

Each run receives a fresh namespace in dedicated rehearsal tables. This is an
S3 fixture simulator, with no connection to managed buckets or a production
provider. The primary app and its contained commands receive five sealed
ephemeral secrets: `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, `AWS_REGION`,
`AWS_ENDPOINT_URL_S3`, and `S3_BUCKET`. Use the supplied endpoint explicitly if
your SDK does not read `AWS_ENDPOINT_URL_S3`, and enable path-style addressing.
These values cannot be overridden in the scenario or command environment.
Sibling services receive bindings only through explicit `managed_work` capabilities
below. Do not combine the
adapter with ordinary `buckets` or recipe cleanup commands; Gregale owns cleanup.
It can be combined with the isolated PostgreSQL fixture adapter.

The private bridge endpoint on port 10084 resolves the live caller and deployment
for every request, verifies the namespace's SigV4 credentials, and checks the
retained run policy under the action closing lock before admission. It never
forwards a request to another storage endpoint. Ordinary VMs gain no new port
permission. Customer object keys are names within the isolated namespace; they
are never host filesystem paths.

Supported operations are header-signed, path-style PutObject, GetObject,
HeadObject, DeleteObject, HeadBucket, and ListObjectsV2 with an optional prefix.
Listing returns the complete bounded inventory; pagination, delimiters, and
`max-keys` below 32 are unsupported. Use ordinary, nonstreaming request bodies.
For the AWS Go SDK, set `RequestChecksumCalculation` and
`ResponseChecksumValidation` to `WhenRequired` to avoid automatic streaming
trailers and checksum-mode requests. Content-MD5 and explicit SHA-256, CRC32 or
CRC32C upload checksums are verified. Multipart, copy, presigned URLs, versions,
tags, encryption controls, range/conditional requests, caching metadata, storage
notifications and other provider features are refused and make collection
incomplete. This scope does not qualify production-provider behavior or collect
arbitrary guest filesystem writes.

The platform durably admits each request before reading its upload body. A
successful mutation and its receipt commit together. Repeated PUTs and DELETEs
remain separate receipts, including writes whose final contents are identical.
At collection, new requests are fenced and admitted requests have at most five
seconds to finish. Mutation receipts use completion order, so concurrent writes
are replayed in the order they changed storage. The original collector must acknowledge the closed capture window before final
collection; a replacement cannot adopt it. The collector hashes every final
object directly and publishes evidence in the same transaction that removes all
object bodies. Lost requests, unsupported operations, changed deployments,
expired namespaces, exceeded bounds and failed cleanup cannot pass. Cleanup is
idempotent after an ambiguous response. Fixed 14-minute resource expiries and an
apid reaper bound abandoned resources; cleanup remains active when admission is
disabled. Active journals block owner purging until cleanup; terminal journals
follow owner retention.

The JSON report's `objects` evidence has `platform_object_collector` provenance,
run/app/deployment/resource/collector identity, its closing acknowledgement, fixture and contract hashes, initial/final
inventories, mutation receipts and confirmed cleanup. Inventories include keys,
sizes, SHA-256 digests, content types and bounded user metadata, without content
or credentials. JSON, text, Markdown and HTML comparisons report a separate
object-storage result. They compare the final inventory and the multiset of
mutation attempts. Thus a second identical upload fails comparison even if all
final objects match. Ordering across independent operations and intermediate
read timing are outside this comparison scope. Calls that never reach the private
listener need explicit application outcome assertions. Collector storage or identity
lookup errors make the listener incarnation fail closed until it is restarted.
General detached workers still
need explicit completion checks in the recipe.

Version 1 permits one bucket, 32 current objects, 64 KiB per object, 256 KiB of
current content, eight metadata entries per object and 256 requests per run.
Evidence is bounded to 1 MiB. These are format bounds; existing account and app
quotas still apply.

The adapter stays **disabled pending native qualification**. After qualification,
`FAAS_REHEARSAL_OBJECT_FIXTURES=1` on apid and the private gateway enables it in
addition to the existing containment, simulation and command gates. The gateway
needs the existing fleet/host secret opener. No provider credentials or customer
endpoint are configured. The apid expiry reaper starts independently of this
admission flag.

Portable acceptance exercises the actual AWS Go S3 client and both durable
store implementations:

```bash
go test -race -count=1 ./pkg/rehearsalobj/...
```

The PostgreSQL cases require the repository's disposable test database and must
run without skips for qualification. Native acceptance must additionally cover
the guest SDK/secret delivery, both firewall hooks with real packets, cross-run
and ordinary-caller denial, an upload racing sealing, cancellation, collector
restart, expired-resource cleanup, both revision reports and leakcheck. Portable
acceptance does not establish those native properties. See
[ADR-20261017201010](adr/20261017201010-rehearsal-object-fixtures.md).

## Native HTTP workflows

For common API tests, declare requests directly. `requests` run after the
lifecycle profile is prepared and before `wait_for`; `checks` run after
`wait_for`. Steps in each list run in order. An assertion `command` remains
optional for application-specific checks. A `trigger` command may also be used
alongside native requests; it runs first.

```yaml
version: 1
scenarios:
  customer-export:
    project: export-api
    source: ./gateway
    consumer_auth_mode: required
    consumers:
      - name: customer-a
      - name: customer-b
    requests:
      - name: submit
        as: customer-a
        method: POST
        path: /exports
        json: {format: csv, run: '${run.id}'}
        expect:
          status: 202
          content_type: application/json
          json: {'/owner': customer-a}
        capture: {export_id: '/id'}
    checks:
      - name: forbidden-read
        as: customer-b
        method: GET
        path: /exports/${steps.submit.export_id}
        expect: {status: 403}
```

`as` uses a short-lived Gregale consumer key declared under `consumers`.
Application-owned identity still needs the application's own headers or
fixtures. `expect.json` and `capture` use JSON Pointers such as `/result/id`;
the latter accepts string, number, and boolean values. Captures can be used
in later paths, headers, JSON bodies, and expectations. `${run.id}` supplies
the isolated run ID. Captured values in paths and queries are URL escaped.
The JSON report lists each request's name, method, path without query, status,
duration, and result; it does not include response bodies or credentials.
Native requests stay on the isolated app URL and do not follow redirects.
Request and inspected response JSON are limited to 1 MiB each. Each request
has a 30-second timeout within the scenario timeout.

An asynchronous request may capture an invocation ID under the name used by
`wait_for.invocations[].trigger_key`. The existing object, delivery, and queue
wait conditions then run before `checks`. Native request steps run with
`real-vm` or `local`. A `--engine simulated` run still needs its own
`simulation` command.

To create a small starting manifest from an OpenAPI document:

```sh
gregale test init --from openapi.yaml --project export-api --source ./gateway
gregale test --validate
```

The generator selects public GET routes with no path or required query
parameters and a declared 2xx response. It adds status and JSON content-type
checks where applicable, skips other operations, and never overwrites an
existing manifest. Review the generated requests, then add authentication,
fixtures, ownership, idempotency, and business assertions explicitly.

## Local HTTP tests and case data

Start your application with its normal local development command, then run the
same native requests and checks against it:

```sh
gregale test --scenario api-smoke --engine local --base-url http://localhost:3000
gregale test --scenario customer-export --engine local --base-url http://localhost:3000 \
  --data cases.json --repeat 2 --report results.json --junit results.xml
```

The `local` engine sends real HTTP requests to a local app. It can start the app
from the manifest, or use an already running app with `--base-url`.
It requires no platform login and provisions no Gregale resources. `--base-url`
must be an HTTP origin on `localhost` or a loopback IP address, with no path,
credentials, query, or fragment. Redirects are not followed. Execution order is
`setup`, `trigger`, `requests`, `checks`, assertion `command`, then `cleanup`.
Cleanup runs after failures and interruption. The scenario's `source` directory
is the working directory for commands; it need not contain a deployable app.

Local runs do not deploy `services`, create PostgreSQL databases or buckets,
apply `secrets` or consumer authentication policy, or create consumers. Configure
your local app and its dependencies beforehand, or use `setup` and `cleanup`
commands. For a step with `as: customer-a`, provide the existing local test key
in `GREGALE_TEST_CONSUMER_CUSTOMER_A_KEY`; an optional
`GREGALE_TEST_CONSUMER_CUSTOMER_A_ID` is also passed to commands. Missing keys
fail before setup. These keys remain owned by your local fixtures.

Platform `wait_for` conditions are rejected by the local engine. Use `real-vm`
for queue, invocation, delivery, object, and lifecycle evidence. `--profile`,
`--preflight`, and `--max-workload-minutes` apply only to real-VM tests. Reports
label local runs with `engine: local` and `profile: local` and contain no VM wake
evidence. A local HTTP test and a simulated test do not establish that an app
behaves correctly after being parked and restored.

### Rehearse managed async workers

Add `managed_work` to a contained-command scenario to share fixtures with declared
workers and collect durable completion evidence. The initial adapter supports
Gregale managed HTTP async routes. Each revision receives fresh resources with
identical pinned initial data; participants share resources within that revision.

For an existing behavior scenario named `import-api`, with the PostgreSQL and
object fixture adapters above, declare:

```yaml
services:
  worker:
    source: ./worker
    async_routes:
      - path: /import
        methods: [POST]
        retry_policy: {max_attempts: 3, base_seconds: 1, max_seconds: 2}
managed_work:
  version: 1
  primary: import-api
  participants:
    - workload: import-api
      postgres: read_write
      objects: read_write
    - workload: worker
      async: true
      postgres: read_write
      objects: read_write
```

List every enrolled workload, including services with no resource permissions.
The primary must own declared fixtures with `read_write`; at least one sibling
must have `async: true` and declared async routes. A service with no resource
capability gets no fixture credentials. PostgreSQL currently supports explicit
`read_write` access to this fixture database; object access supports `read` and
`read_write` to this fixture namespace. Declared workers get distinct database
logins and workload-bound object credentials. Ordinary provider bindings and
account tokens are never copied. Service secrets cannot override these bindings.

An authorized private call to `http://worker.svc.gregale:10081/import` now follows
the worker's enabled async route path/method/header rules and returns the ordinary
202 invocation receipt without waking the worker inline. The existing scheduler
then dispatches the admitted job and its configured retries. Admission strips
credential and platform headers using the existing async-route contract. Routes
with advanced match expressions, callbacks, release overrides or upgrades are
outside this adapter. Ordinary HTTP routes such as `/status` retain their serving
behavior. Public async admission also participates in the durable work journal.

Sealing closes admission to new jobs before checking completion. Admitted jobs
and retries retain fixture access while draining. The platform records each
job's workload, deployment, payload digest, dispatch instance and attempt outcome.
After work settles, recorder owners must acknowledge closing before database,
object and external-effect collectors can publish complete evidence. Each declared
async worker must execute at least one job; an idle queue alone is insufficient.

Admission during closing is durably cancelled and makes evidence incomplete; the
private ingress returns a conflict. New writes after sealing are rejected. A
cancelled job, an unknown or lost attempt, replacement deployment, missing worker,
missing recorder acknowledgement, journal loss or unfinished retry cannot pass.
Requests rejected before durable admission are outside the job inventory; assert
expected HTTP outcomes in the recipe. The existing collection deadline is ten
seconds after the recipe finishes. Use
explicit outcome assertions or invocation waits when the operation needs longer;
unsettled work at the deadline is inconclusive. New child work must be admitted
before collection starts. Arbitrary external queues, cron, trigger records,
customer timers and detached processes are outside this completion proof.

The action snapshot's `managed_work` section retains the participant plan and
resource bindings, plus jobs and retry outcomes. The CLI verifies these identities
against its own deployment and fixture receipts. Action comparisons include an
`async` group for each worker method/path, compare payload digests and attempt
outcomes, and preserve duplicate jobs. Generated app, deployment, resource and
instance IDs support inspection and do not become equality requirements between
revisions. Object mutation receipts also retain the responsible logical workload.

For example, two revisions can return the same API response and finish with
identical database rows and export contents while the candidate uploads the
export and simulates a notification twice after a retry. Their effect comparison
fails even though the final data matches. Missing completion evidence instead
makes comparison inconclusive.

The adapter remains **disabled pending native qualification**. After native VM
identity, containment, secret delivery and cleanup acceptance passes, operators
may set `FAAS_REHEARSAL_WORKERS=1` on apid alongside the required earlier rehearsal
gates. The private gateway continues honoring durable enrollment when new
admission is disabled. Cleanup removes participant database roles and isolated
object data; existing expiry cleanup stays available with admission disabled.
Contained cold starts use the separate preparation and boot protocol below;
snapshot restore remains unsupported. The separately qualified in-place suspend/resume checkpoint below uses warm execution. The worker-termination extension uses warm execution.
See [ADR-20261017201011](adr/20261017201011-rehearsal-managed-workers.md).

Portable acceptance (the fixture URL must name a separate disposable cluster):

```bash
FAAS_REHEARSAL_PG_TEST_ADMIN_URL='postgres://USER:PASSWORD@127.0.0.1:PORT/gregale_rehearsal_admin?sslmode=disable' \
  go test -race -count=1 ./pkg/rehearsalwork/...
```

The v1 evidence bounds are 64 jobs per run, 32 attempts per job and 8 KiB of stored
JSON payload per job, in addition to each fixture adapter's limits.

### Rehearse worker termination and recovery

An optional `managed_work.worker_termination` exercises a single worker job's
retry after its VM is terminated at an explicit application checkpoint. It uses
the managed worker setup above and remains disabled pending native qualification.

```yaml
managed_work:
  version: 1
  primary: import-api
  participants:
    - workload: import-api
      postgres: read_write
      objects: read_write
    - workload: worker
      async: true
      postgres: read_write
      objects: read_write
  worker_termination:
    workload: worker
    checkpoint: after-commit
actions:
  network: contained
  simulation:
    version: 1
    fixtures:
      - workload: worker
        name: after-commit
        integration: checkpoint
        adapter: http
        method: POST
        path: /after-commit
        responses:
          - status: 200
```

After its chosen operation, the worker awaits a POST with a JSON body to
`GREGALE_OUTBOUND_URL + "/i/checkpoint/after-commit"`, using the existing
outbound relay configuration. Allow at least 35 seconds for that checkpoint call.
The host relay receives the simulator's durable receipt and holds the response;
schedd then asks vmmd to fence effects, join collectors and destroy that VM. The
first checkpoint call ends with the VM's termination. The same call on a retry
receives the ordinary fixture response. Configure the managed async route's retry
policy and invocation deadline to allow this recovery, and use existing HTTP
checks/waits to confirm completion before collecting evidence.

V1 permits exactly one admitted job for the selected worker and terminates its
first dispatch only. Other declared workers may participate normally. This is a
cooperative checkpoint: the application must await it, and the receipt does not
assert that an earlier database commit actually happened. Database, object and
outbound evidence establish the resulting effects independently.

Reports retain the original VM, recorder, checkpoint and job identities alongside
the replacement VM's dispatch. Missing checkpoints, an early disconnect, uncertain
cleanup, unknown attempts or recovery on the old VM cannot pass. A declared fault
never excuses missing collector evidence. Cold/resume profiles, process-only
termination and dependency faults remain unsupported by this contract.

After native qualification, `FAAS_REHEARSAL_WORKER_FAULTS=1` on apid enables new
fault enrollment alongside all preceding rehearsal gates. Existing enrolled fault
cleanup and receipt reconciliation continue when new admission is disabled.
See [ADR-20261017201012](adr/20261017201012-rehearsal-worker-termination.md).

The native VM cleanup component gate is
`scripts/ci/rehearsal-worker-fault-acceptance.sh`. It requires the dedicated root
x86 KVM host and staged metal-test images. It complements the portable store,
relay, scheduler, retry and effect-comparison suites; it does not replace the
preceding containment, credential-delivery and resource qualification gates.

### Rehearse PostgreSQL disconnect and recovery

On a contained warm managed-worker scenario with an isolated `postgres_fixture`,
add this to `managed_work`:

```yaml
postgres_fault:
  workload: worker
  checkpoint: after-commit
  unavailable_ms: 1000
  recover_within_ms: 5000
```

Declare exactly two participants, primary and one async worker, both with
`postgres: read_write`. The matching simulation must use integration `checkpoint`,
adapter `http`, method `POST` and one undelayed HTTP 200 response. Await
`GREGALE_OUTBOUND_URL + "/i/checkpoint/after-commit"` with a JSON object body and
at least a 35-second deadline. Its response releases after independent confirmation
that existing sessions were terminated and new connections are refused.

The fence affects the generated database shared by both participants. Committed
rows survive; open transactions roll back. Restoration retains credentials and
checks a fresh connection and role privileges. The worker must handle idle pool
errors and use its real retry policy. Later attempts should resume after the
commit, rather than repeat the checkpoint. Allow backoff for collector scheduling
delay: the qualification fixture uses three seconds for a one-second outage.

V1 permits one warm job on its original worker. Additional jobs, replacement
workers, cold start, suspend/resume and worker termination cannot combine with
this fault. Unavailability accepts 100–30,000 ms and recovery 1–120,000 ms.
`outage_ms` records the confirmed active interval before restoration starts;
`recovery_ms` includes scheduling delay after the planned restore time through
the recovery probe. This models a
database connection fence; server crashes and arbitrary network faults need
their own adapters.

The receipt appears under
`actions.evidence.snapshot.managed_work.postgres_fault`. SQL, objects, retries and
simulated actions still compare independently. Missing activation/recovery,
interrupted checkpoints, owner loss or failed cleanup cannot pass. Confirmed
recovery over budget fails even if both revisions exceed it. Disabling enrollment
leaves recovery and expiry cleanup active.

Admission remains **off pending native qualification**. The additional apid flag
is `FAAS_REHEARSAL_POSTGRES_FAULTS=1`. See the
[complete recipe](../tests/scenario-acceptance/rehearsal-running-job/gregale-postgres-fault-test.yaml),
[native coordinator](rehearsal-native-acceptance.md) (`--journey postgres-recovery`)
and [ADR-20261018000200](adr/20261018000200-rehearsal-postgres-fault-recovery.md).

### Rehearse contained cold starts

Cold-start support remains **disabled pending native qualification**. After the
preceding rehearsal gates and native acceptance pass, operators may enable
`FAAS_REHEARSAL_COLD_START=1` on apid for new enrollment. Turning admission off
continues honoring existing durable preparations and cleanup receipts.

Reuse a contained command recipe and select `--profile cold`:

```yaml
command_execution:
  version: 1
  runtime: contained
  startup_within: 10s
```

`startup_within` bounds host-observed VM startup through guest readiness for
every declared serving workload. It accepts 1ms through 2m and defaults to 2m.
HTTP assertion deadlines still bound application readiness and operation outcomes.
For example, run the reviewed import recipe against both pinned revisions:

```sh
gregale test rehearse --base main --candidate HEAD --scenario import \
  --engine real-vm --profile cold --max-workload-minutes 60 \
  --report import-cold.json --html import-cold.html
```

Enrollment acknowledges the exact budget and workload inventory before deployment.
Cold-enrolled previews verify their deployment artifacts without the normal
serving warm-up boot. Application startup failures are exercised by the scenario
trigger. Commands still execute in separate contained task VMs; they cannot
supply serving lifecycle proof.

The existing preview-only fresh park prepares each workload. If setup started a
serving VM, its original relay owner must fence requests, join collectors, and
acknowledge normal physical teardown before schedd retires its instance row.
After all old instances drain and snapshots are invalidated, preparation becomes
ready. The next serving boot rejects snapshot inputs and records its actual
`cold_boot` method, startup duration, deployment, instance, wake and recorder
identities. The runner matches these receipts to new completed wake timelines,
including operations triggered from command VMs through private service URLs.
All managed async job attempts must use the corresponding verified cold VM.

Reports retain receipts in `actions.evidence.snapshot.cold_start`, observed wake
correlation in `cold_wake`, and the existing command, database, object, job and
outbound evidence. Comparison ignores generated identities and timing jitter
within the declared budget. A complete boot that exceeds the budget fails;
missing retirement, boot, matching wake or collector proof remains inconclusive.
Uncertain retirement is never retried as another planned kill.

V1 permits one deployment, one preparation and one post-preparation serving VM
per declared workload. An optional setup VM must be the sole running VM with no
pending tail work or unsettled effects. Setup must not admit managed async jobs
before preparation; admit them from the trigger. Multiple replicas, standalone
job execution modes, snapshot restore, and combining cold start with suspend/resume or worker
termination remain unsupported. `--profile all` includes restored execution and
is therefore rejected for contained command recipes.

Portable acceptance covers both stores, original-owner loss, unsettled effects,
missing/foreign boot proof, scheduler recovery, RPC capability refusal, startup
budgets and duplicate notifications. The native component gate is
`scripts/ci/rehearsal-cold-start-acceptance.sh`; run it only on the dedicated root
x86 KVM host with the staged metal-test images. It complements the required
containment, credential-delivery and fixture qualifications. See
[ADR-20261017201013](adr/20261017201013-rehearsal-cold-start-parity.md).

### Rehearse contained suspend/resume

Operator-qualified contained command recipes can declare one in-place pause per
selected HTTP-serving workload. Use the warm profile and add:

```yaml
suspend_resume:
  version: 1
  checkpoint: after_setup
  workloads: [api, worker]
  pause_for: 1s
  resume_within: 2s
```

`workloads` names existing logical workloads, including the scenario's project
name for its primary app. `pause_for` is 1ms–30s and `resume_within` is 1ms–2m.
The checkpoint runs after setup commands and warm preparation, before the trigger,
requests, checks and behavior observers. Keep setup synchronous; V1 refuses runs
that have already admitted managed jobs. Each selected workload needs one
request-serving VM, one deployment and its original live collector. Pending
commands, effects, tail work and active operations cannot cross the checkpoint.

```sh
gregale test --scenario customer-import --engine real-vm --profile warm \
  --report import-resume.json --html import-resume.html
```

Gregale removes the VM from routing, retains its full memory reservation and
fences new recorded outbound effects while it pauses the resident VM. It joins existing idle relay
connections, revalidates containment before resuming, runs the real guest resume
hook, then checks readiness. The same VM, lease and original recorder continue;
fixture credentials stay in memory and no persistent VM snapshot is written.
Declared async HTTP workers can execute their managed jobs after this checkpoint.
Those jobs must use the verified resumed instance for complete evidence.

The resume hook can reconnect to contained dependencies before the VM becomes a
serving target. Hooks must wait until the checkpoint completes before enqueueing
managed jobs. Existing isolated PostgreSQL sessions retain their transaction
semantics: pausing a VM does not pause its database. The fixture collector's SQL
completion checks and the object adapter's mutation receipts still define their
respective evidence boundaries.

Reports retain the observed checkpoint in `suspend_resume` and the sealed host
receipts in `actions.evidence.snapshot.suspend_resume`. Each receipt identifies
its run, workload, deployment, VM and original recorder, actual pause duration,
resume-readiness duration and scheduler acknowledgement. Comparison excludes
runtime IDs and raw timing jitter. Exceeding `resume_within` is a behavioral
failure even if both revisions exceed it. Missing physical receipts, collector
loss, an unsupported owner, changed configuration or uncertain cleanup remains
inconclusive. A lost reply can use a durable resume receipt; it cannot repeat an
uncertain pause or count a fresh VM as an in-place resume.

For example, the comparison can expose an unchanged import result accompanied by
a duplicate captured notification after resume. This is an illustrative result,
not a finding from an existing customer run.

V1 supports only `real-vm --profile warm` with contained commands and simulation.
Replicas, standalone worker/job lifecycle modes, private
network members, gRPC readiness, cold-start and worker-termination combinations,
persistent snapshot restore, and fixture credential rotation during the pause
remain outside this protocol. `restored` and `all` do not qualify it.

New admission requires `FAAS_REHEARSAL_SUSPEND_RESUME=1` on apid, off by default.
Previously enrolled intent and receipt handling continues when admission is
disabled. Portable tests exercise both metadata stores, ownership loss, missing
proof, lost replies, configuration changes, budget failures and post-resume
notification multiplicity. Before rollout, run
`scripts/ci/rehearsal-suspend-resume-acceptance.sh` on the dedicated native root
x86 Linux KVM host with staged V6 guest images, and satisfy the existing network,
credential-delivery and cleanup gates. Compilation and portable tests do not
replace those gates. See [ADR-20261017201014](adr/20261017201014-rehearsal-suspend-resume.md).


#### Pause a running managed job

New running-job admission additionally requires operator qualification and
`FAAS_REHEARSAL_RUNNING_JOB_SUSPEND=1` on apid, off by default.
To pause inside an operation, use the same warm contained recipe with one declared
async HTTP worker and one managed job in the entire run:

```yaml
suspend_resume:
  version: 1
  checkpoint: managed_job
  workloads: [worker]
  worker_checkpoint: after-commit
  pause_for: 1s
  resume_within: 2s
```

Add an HTTP simulation fixture to the recipe's `actions.simulation.fixtures`:

```yaml
- workload: worker
  name: after-commit
  integration: checkpoint
  adapter: http
  method: POST
  path: /after-commit
  responses:
    - status: 200
```

The worker must await `POST /i/checkpoint/after-commit` through its existing
contained outbound relay at the application's chosen boundary. For an import,
commit the records, await this call, then send the notification. Its name does not
prove a SQL commit: the application chooses the location and the database collector
independently verifies final results and completion. Existing database sessions and
the fixture database keep their normal transaction semantics during the pause.

Gregale holds the successful simulated response locally before the guest can
receive it. The original relay arms the checkpoint; the CLI does not start it with
the after-setup POST endpoint. The VM pauses with that exact first dispatch and
response still open. All prior recorded effects must be settled; other relay
connections are joined. Ordinary freeze still refuses an active request. After
containment revalidation, the resume hook and readiness, the scheduler publishes
the same VM. Only then does the original relay release the response and let the
worker continue.

Setup must finish before admitting the job. A fast worker may reach its checkpoint
while the contained trigger command is still retiring; Gregale holds the response
and waits for that existing command to settle before making the pause eligible.
Additional jobs, other async workers,
concurrent forwards, replicas and termination/cold combinations are unsupported.
The checkpoint permits a bounded host hold of 170 seconds, covering the maximum
pause and readiness intervals plus coordination. Keep the application's request
and job deadlines long enough for the planned pause; Gregale does not extend them
or change lease renewal. New dispatch attempts remain fenced while the original
checkpoint is pending or uncertain; an actual retry can proceed after the
checkpoint completes. An expired lease, lost relay, early disconnect, changed
attempt or missing checkpoint leaves the rehearsal inconclusive.

Receipts additionally retain `invocation_id`, `invocation_attempt` and
`checkpoint_attempt_id`. Sealed comparison requires the matching independent job
journal and completed simulation receipt. A pause injects no retry. If the
application actually retries after release, that retry remains visible and can
produce a behavioral difference, including duplicate notifications or object
writes. This protocol retains the original worker VM and collector for the
operation, including retries. A retry that replaces that VM remains inconclusive
and needs a separate ownership protocol. The CLI polls this lifecycle while draining and sealing managed work;
JSON and HTML retain its identities for inspection. Missing evidence cannot be
replaced by a fresh VM or a reconstructed collector. See
[ADR-20261017201015](adr/20261017201015-rehearsal-running-job-suspend.md).

The [native running-job acceptance journey](rehearsal-native-acceptance.md)
combines this checkpoint with contained commands, a real HTTP job retry,
PostgreSQL and object fixtures, and a duplicate-notification candidate. It
requires a passing control comparison, the specific captured regression and
confirmed cleanup on a dedicated native installation. Portable checks and
offline fixture preparation do not qualify that installation or enable rollout.

### Start and stop the app with the test

Declare the foreground app command in `local` to run everything with one command:

```yaml
version: 1
scenarios:
  api-smoke:
    project: my-api
    source: .
    local:
      command: [node, server.js]
      readiness:
        path: /health
        status: 200
        timeout: 30s
      shutdown_timeout: 5s
    checks:
      - name: health
        method: GET
        path: /health
        expect: {status: 200}
```

```sh
gregale test --scenario api-smoke --engine local
gregale test --scenario api-smoke --engine local --load --vus 5 --duration 30s \
  --progress --report results.json --junit results.xml
```

Gregale chooses an available loopback port. The app and fixture commands receive
`HOST`, `PORT`, `GREGALE_TEST_HOST`, `GREGALE_TEST_PORT`, and `GREGALE_TEST_URL`,
along with the usual run identity and case data. Inherited `HOST` and `PORT` are
replaced for this run. Configure your app to bind these values; for example,
Node's HTTP server can use `server.listen(Number(process.env.PORT), process.env.HOST)`.
Commands run in `source` and do not implicitly use a shell. An executable can also
take `${local.host}`, `${local.port}`, or `${local.url}` in its argument list:

```yaml
local:
  command: [python3, -m, http.server, '${local.port}', --bind, '${local.host}']
```

When `local.command` is declared, `--base-url http://localhost:3000` selects port
3000 for the managed app. An occupied port fails before startup; Gregale does not
attach to or stop the existing server. `localhost` resolves to `127.0.0.1` in this
mode. The port reservation is released immediately before launching the command,
so the app must bind it itself. Without `local.command`, `--base-url` continues to
target an app you started separately.

Execution is `startup` and readiness, `setup`, `trigger`, `requests`, `checks`,
assertion `command`, `cleanup`, then `shutdown`. With `--load`, the concurrent
journeys replace the individual requests and checks. Each data row and repeat
starts a fresh app process. Cleanup can still call the app after failed checks,
Ctrl-C, or SIGTERM. An app that exits before shutdown fails the run, even with exit
code zero. The command must stay in the foreground; it must not detach or daemonize.

Readiness sends unauthenticated GET requests every 100 ms without redirects or
proxies. Defaults are `/`, status `200`, and `30s`; timeout is limited to `1s..5m`
and also respects the overall scenario timeout. The path cannot contain a query,
fragment, or template. Setup does not run until readiness succeeds. Put any setup
needed to start the server in the app command itself.

Shutdown sends SIGTERM to the app's process group, waits up to
`shutdown_timeout` (`1s..30s`, default `5s`), and uses SIGKILL for remaining
processes when necessary. Forced shutdown is recorded and does not by itself
fail the test. JSON and JUnit include `local_app` readiness and shutdown evidence
and the `startup` and `shutdown` phases. App output is held in a bounded 64 KiB
tail and printed to stderr only after a failed run; it is omitted from reports
and JSON stdout. Managed startup is supported on Unix platforms, including Linux
and macOS. The `local` block is used only by `--engine local`.

### Run the same scenario for JSON or CSV rows

`--data` currently applies to the local engine. A JSON data file is an array of
objects with the same fields in every row:

```json
[
  {"format": "csv", "limit": 10, "include_archived": false},
  {"format": "json", "limit": 25, "include_archived": true}
]
```

Reference these fields in paths, queries, headers, JSON bodies, or expectations:

```yaml
requests:
  - name: submit
    method: POST
    path: /exports
    json:
      format: '${data.format}'
      limit: '${data.limit}'
      include_archived: '${data.include_archived}'
      run: '${run.id}'
    expect: {status: 202}
    capture: {export_id: '/id'}
checks:
  - name: read
    method: GET
    path: /exports/${steps.submit.export_id}?format=${data.format}
    expect:
      status: 200
      json: {'/format': '${data.format}', '/limit': '${data.limit}'}
```

An exact `${data.limit}` or `${data.include_archived}` JSON value preserves the
number or boolean type. `${data.limit.string}` forces a string even for a
numeric input. Embedded references such as `limit-${data.limit}`
produce text. CSV uses a header row and treats every value as a string:

```csv
format,label
csv,small-export
json,large-export
```

Data files are limited to 1 MiB, 1–100 rows, and 1–32 fields. Field names begin
with a lowercase letter and contain lowercase letters, digits, and underscores
(at most 64 characters). JSON fields accept strings, numbers, and booleans;
nested values and null are rejected. Unknown `${data.*}` references fail
manifest validation for the selected scenario. Add `--scenario NAME` when other
scenarios need a different data file. Validate before sending requests with:

```sh
gregale test --validate --engine local --data cases.json
```

Each row runs the whole scenario with a fresh run ID and capture map. Cases run
in file order; `--repeat N` repeats all rows N times. With a manually supplied
`--base-url`, rows share the running app, so use run IDs or fixture cleanup to
avoid shared state. With `local.command`, each row/attempt starts a fresh app.
An assertion failure is reported for its row; remaining rows continue, and the
command exits unsuccessfully if any row fails. Interruption stops new rows.
Commands receive `GREGALE_TEST_CASE` (`row-1`, `row-2`, etc.) and
`GREGALE_TEST_DATA_JSON`, plus the local URL, run ID, engine, profile, and scenario
name. Without a data file, there is one unnamed case with empty `{}` data.
The CLI strips its `FAAS_TOKEN` account credential from command environments.
Command output goes to stderr under `--json`.

JSON and JUnit reports identify the row and attempt, including request evidence
and cleanup failures. They omit data values and captures. JUnit names such as
`customer-export/local/row-1#2` distinguish the second run of the first row.

## Native local load tests

Run the same HTTP workflow under concurrent load without installing a separate
test tool:

```sh
gregale test --scenario api-smoke --engine local --base-url http://localhost:3000 \
  --load --vus 5 --iterations 100 --report load.json --junit load.xml
gregale test --scenario api-smoke --engine local --base-url http://localhost:3000 \
  --load --vus 5 --duration 30s
```

Each user runs a journey consisting of `requests` followed by `checks`, in order,
then immediately starts the next journey. Users share the total iteration budget;
`--iterations 100 --vus 5` runs 100 journeys in total. This is a closed concurrency
model: throughput depends on the app's response speed. Add `rate` and `duration`
to choose the fixed arrival schedule described below. Captures start empty for
every journey. `${run.id}` becomes
`<run-ID>-<iteration-number>` so requests can create distinct resources. Cleanup
commands receive the parent `GREGALE_TEST_RUN_ID` and can remove resources by
that prefix.

Keep defaults and CI thresholds in the scenario:

```yaml
load:
  vus: 5
  iterations: 100
  thresholds:
    p95: 250ms
    p99: 500ms
    error_rate: 0.01
    success_rate: 0.99
    min_samples: 100
    steps:
      submit: {p95: 200ms, p99: 400ms, error_rate: 0}
      read: {p95: 100ms, p99: 200ms}
```

`thresholds.steps` keys must name declared HTTP requests or checks. Step budgets
add to the aggregate limits; they do not replace them. Only explicitly supplied
step limits apply. A step with a budget and no samples fails, including a check
skipped because an earlier request failed. `min_samples` applies to the total
HTTP request count, while each entry under `thresholds.steps` can set its own
sample minimum for a critical request.
This prevents an aggregate percentile or an allowed error rate from hiding a
problem in a critical step. JSON reports include evaluated budgets under each
step, terminal summaries identify them by step name, and failed budgets fail
the JUnit case and command. Success rate and error rate are calculated from
requests that matched the declared expected status and response assertions.

This optional block configures `--load`; it does not enable load for a normal
test. Replace `iterations` with `duration: 30s` to choose a timed run. CLI flags
override the manifest; selecting `--duration` clears its iteration setting and
stages, and selecting `--iterations` clears its duration, stages, and rate. Without
configuration, `--load`
uses one user and 100 journeys. `--vus`, `--iterations`, `--rate`, and `--duration` require
`--load`. Validate the configuration without contacting the app:

```sh
gregale test --validate --scenario api-smoke --engine local --load
```

`setup` and `trigger` run once before load. The assertion `command` runs once
after successful load and thresholds. `cleanup` runs once after each run,
including a failure or interruption. With `--data`, every row gets a separate
load run and fixture lifecycle; rows run sequentially. `--repeat` repeats those
runs. Commands receive `GREGALE_TEST_LOAD=1`, `GREGALE_TEST_LOAD_MODE`,
`GREGALE_TEST_LOAD_VUS`, `GREGALE_TEST_LOAD_ITERATIONS`, and
`GREGALE_TEST_LOAD_DURATION`. After load, assertions and cleanup can also inspect
aggregate evidence in `GREGALE_TEST_LOAD_JSON`.

### Compare against a performance baseline

Save a successful local load report, then use it as a regression gate:

```sh
gregale test --scenario api-smoke --engine local --load --repeat 3 \
  --report baseline.json
gregale test --scenario api-smoke --engine local --load --repeat 3 \
  --baseline baseline.json --report current.json --junit current.xml
```

These commands use `local.command`; add the same `--base-url` for an app you
start yourself. The same workflow works with `--suite` and member case files.
`--baseline` requires `--load` and test execution. Recording a report does not
require a baseline, and the comparison never updates the baseline file.

By default, the aggregate and **every HTTP step** must have at least 20 samples
in both runs, p95 latency may increase by at most 10%, and the error rate may not
increase. Keep different budgets in the scenario:

```yaml
load:
  workload: exports-small-v1
  vus: 5
  iterations: 100
  thresholds: {p95: 250ms, error_rate: 0.01}
  regression:
    p95_percent: 15
    error_rate_increase: 0.005
    min_samples: 50
    steps:
      submit: {p95_percent: 10}
      read: {p95_percent: 20, error_rate_increase: 0}
```

Step budgets inherit unspecified values from the aggregate regression block.
Limits are inclusive. `p95_percent` is a relative increase: baseline p95 of
100ms and a 15% budget permit at most 115ms. A zero baseline permits only zero
latency. `error_rate_increase` is an absolute fraction: `0.005` allows an increase
of 0.5 percentage points, such as 1% to 1.5%. The allowed ranges are 0–1,000 for
the percent, 0–1 for the error fraction, and 1–100,000 for minimum samples.
Absolute `thresholds` still apply; regression budgets add a separate gate only
when `--baseline` is supplied. Validate the manifest budgets with the usual
`--validate --engine local --load` command.

Before starting any app or fixture, the CLI matches each selected run by
scenario, engine, profile, case, and attempt number. A baseline can contain
additional runs, but missing or duplicate matches fail. Only successful load
runs with successful cleanup qualify. Incomplete runs, dropped arrivals,
insufficient baseline samples, and malformed metrics are rejected. Reports from
older CLI versions without workload metadata must be recorded again.

New load reports include versioned `load.workload` metadata. Its signature checks
the ordered request templates (including query, JSON body, assertions, captures,
and header template dependencies), data field names and scalar types, managed
versus manually started app mode, scenario timeout, VUs, iteration or duration
budget, rate, stages, and pacing. Thresholds, regression budgets, progress,
app source revisions, commands, and loopback ports are excluded. This lets an
application change while keeping its test workload constant.

Resolved consumer credentials, literal header values, environment values, and
case values are excluded from the signature. They are not written into workload
metadata. Set `load.workload` to identify your dataset and environment version,
and change that label when those inputs change. Use equivalent fixture state and
generator resources for the two runs; the CLI cannot certify those conditions.
Literal values declared in request bodies and expectations enter the signature
as part of their templates; the report contains only their hash.

Each repeated attempt and data row is compared separately. Percentiles are not
averaged or reconstructed from report summaries. Use enough journeys and repeats
to measure your application reliably; a regression budget is a deterministic
gate, not a statistical confidence test. A sparse current run fails its sample
gate even when the observed latency and errors would pass.

JSON records comparison status, the baseline report's SHA-256, and per-metric
baseline/current values, deltas, limits, and pass/fail results under `baseline`.
Failed comparisons fail the command and JUnit case. Cleanup completes before
comparison; `--fail-fast` then skips remaining attempts and suite members.
Their comparison status is `not_compared`, as it is for a run that fails its
execution or cleanup. A regression-only failure can have `load.status: passed`
while the receipt and `baseline.status` are failed. Report destinations that
alias the baseline file are rejected, including symlinks and hard links.
Baseline inputs are local JSON report arrays, capped at 64 MiB.

### Schedule a fixed arrival rate

Use `--rate` to schedule journeys independently of response speed. A journey is
the complete ordered workflow, so 20 journeys per second can produce more than
20 HTTP requests per second:

```sh
gregale test --scenario api-smoke --engine local --base-url http://localhost:3000 \
  --load --rate 20 --duration 30s --vus 10 --progress \
  --report arrival-load.json --junit arrival-load.xml
```

Keep the same settings in the manifest to reuse them from a named suite:

```yaml
load:
  rate: 20
  duration: 30s
  vus: 10
  thresholds:
    error_rate: 0
    steps:
      submit: {p95: 200ms}
      read: {p95: 100ms}
```

`vus` caps active journeys in this mode. The CLI starts one arrival at the
beginning of the scheduling window and then spaces arrivals at `1 / rate`
seconds, ending before `duration`. The schedule contains `ceil(rate * duration
in seconds)` arrivals. A busy pool drops the arrival immediately rather than
queuing it. If the generator misses multiple deadlines, it counts the missed
arrivals as late and admits at most the most recent due arrival. It never replays
a backlog in a burst or starts arrivals after the scheduling window ends.
Admitted journeys finish their entire workflow during the 30-second drain
allowance. Their run identity uses the arrival's schedule number; dropped
arrivals can leave gaps in those numbers.

**Any dropped arrival fails the run**, even when HTTP error and latency budgets
pass. This indicates that the requested traffic was not delivered; increase the
VU cap or reduce the rate, and check generator capacity. Drops are reported
separately from failed HTTP steps. The HTTP error-rate metric remains failed
steps divided by attempted steps.

The JSON `load.arrival` object records the target journeys/second, planned and
scheduled arrivals, drops at the VU limit, late drops, total drops, scheduling
duration, and achieved journey starts/second. `scheduled` counts starts plus
drops; a canceled or resource-limited run can leave planned arrivals unscheduled.
Achieved start rate uses the scheduling phase, while HTTP-step throughput uses
the whole load phase including drain. Human summaries and progress also show
the target rate and drops. JUnit embeds the same evidence and marks a dropped
run as failed. A skipped suite member retains its arrival plan as not started.
Commands receive `GREGALE_TEST_LOAD_MODE=arrival-rate` and
`GREGALE_TEST_LOAD_RATE` in addition to the other load variables.

Rates are whole journeys/second from 1 to 1,000. Duration is required, from 1
second to 5 minutes, and concurrency remains capped at 50 VUs. The CLI rejects
schedules whose planned complete journeys would exceed 100,000 HTTP steps.
Arrival-rate load cannot use iteration counts, VU stages, or nonzero pacing.
Explicit `--rate` selects this mode and clears manifest iteration counts,
stages, and pacing; an accompanying positive `--pacing` remains an error.
`--duration` can override an existing arrival duration, while `--iterations`
selects the concurrency model and clears the manifest rate. Passing both
`--rate` and `--iterations` is an error. `--rate` requires `--load`.

### Ramp traffic, pace users, and follow progress

Declare a stage schedule instead of `iterations` or `duration`:

```yaml
load:
  vus: 1
  pacing: 100ms
  stages:
    - {duration: 10s, target: 5}
    - {duration: 20s, target: 5}
    - {duration: 10s, target: 20}
    - {duration: 20s, target: 20}
    - {duration: 10s, target: 0}
  thresholds:
    error_rate: 0.01
    steps:
      submit: {p95: 200ms, error_rate: 0}
      read: {p95: 100ms}
```

`vus` is the initial user count. Each stage interpolates linearly from the
previous target to its target, rounded to the nearest whole user. Repeating a
target holds that concurrency. A target of zero stops starting journeys for the
affected users; it can also hold an idle period. During a ramp down, running
users finish their entire current journey before becoming inactive. Active
journeys can temporarily exceed the new target while they finish. After the
schedule ends, in-flight journeys get the same 30-second drain allowance as a
timed run.

Schedules may contain up to 20 stages, each at least 1 second, with a combined
duration of at most 5 minutes and targets from 0 to 50 users. The scenario timeout
and HTTP-step budget still apply. Reports label the mode and stop reason as
`stages`, record initial/max/peak users, and include each stage's duration, start
and target user counts, and journeys started in that stage. Journeys are assigned
to the stage in which they start, even if they finish in a later stage.

`pacing` pauses each user after a journey finishes before starting its next one,
including after a failed journey. It applies to concurrency load modes and defaults to
zero. It adds no pause after the final journey and does not count toward HTTP
step latency. The allowed range is 0 seconds to 1 minute; `--pacing 0s` disables
manifest pacing. Stages and pacing use the concurrency model.

```sh
gregale test --scenario customer-export --engine local --base-url http://localhost:3000 \
  --load --progress --report load.json --junit load.xml
```

`--progress` prints a snapshot once per second to stderr: elapsed time, current
stage or draining state, target and active users, completed/started journeys,
HTTP steps, and failures. It also works with `--json`; stdout remains a single
JSON result. `--pacing` and `--progress` require `--load`. Commands additionally
receive `GREGALE_TEST_LOAD_MAX_VUS` and `GREGALE_TEST_LOAD_PACING`.

A failed request or assertion ends that journey; other journeys continue.
`error_rate` is failed HTTP steps divided by all attempted HTTP steps, as a
fraction from 0 to 1. `success_rate` is its complement. An expected 403 passes.
The default error threshold is zero; setting only `success_rate` replaces that
default with the success-rate threshold. There is no default latency threshold.
Configured limits are inclusive. Crossing a threshold fails the receipt and
exits with status 1. No samples, canceled runs, time limits, incomplete runs
caused by the request budget, and failed cleanup also fail, regardless of the
error tolerance.

The terminal summary shows throughput, success and error rates, p95 and p99
latency, threshold results, and the first error for up to ten failed steps. The
JSON `load` object reports started, completed, failed, and interrupted journeys,
peak concurrency, HTTP-step throughput, response status counts, success and
error rates, and min/mean/p50/p95/p99/max latency. Each step gets its own metrics
and up to five distinct error examples. Status `0` means no HTTP response
was received. Latency is client-observed step time, including template expansion,
request encoding, the full bounded response read, and assertions. Percentiles
use the nearest-rank method; they describe these client timings, not isolated
server processing time. Throughput uses the entire load phase, including draining
in-flight work. Receipts contain aggregate evidence rather than one entry per
journey. JUnit names contain `/local/load/` to distinguish load cases; response
bodies, request headers, data values, and captures are not added to reports.

Load currently requires `--engine local` and either `local.command` or an already
running HTTP app on loopback. It uses native Go HTTP requests, with no k6 or
Postman runtime download.
Limits are 50 users, 10,000 journeys for an iteration run, 5 minutes of scheduling,
and 100,000 HTTP-step attempts per case/run. Iteration configurations that could
exceed the HTTP-step budget are rejected before execution. Timed runs fail if
they exhaust it. Timed runs stop starting journeys at the requested duration and
allow up to 30 additional seconds for in-flight work; iteration runs have a
5-minute execution deadline. The scenario timeout covers setup, trigger, load,
and assertions and can shorten these limits. Each request has a 30-second
timeout, responses are capped at 1 MiB, redirects are not followed, and connections
are reused. Cleanup has its own 45-second deadline.

## Import a Postman collection

Create a native scenario from a local [Postman Collection v2.1 JSON
export](https://schema.postman.com/json/collection/v2.1.0/docs/index.html):

```sh
gregale test import --from collection.json --project export-api
```

The command creates `gregale-test.yaml` with an `api-collection` scenario. Use
`--source`, `--scenario`, and `--output` to change those defaults. Import is local
and never sends collection requests, downloads a runner, or requires a platform
login. The new manifest has private file permissions and cannot overwrite an
existing file.

This is a request scaffold. Review its expected statuses and add native
`expect.json`, `capture`, `checks`, or assertion commands for business behavior.
A request with one distinct saved response status uses that code. Requests with
no saved status default to `200`; `--status CODE` changes the fallback. Multiple
distinct saved codes require an explicit `--status CODE`. Saved examples supply
draft expectations; they do not establish what the original test asserted.

### Supported conversion

- Requests keep their order through nested folders; duplicate names get unique
  native step names.
- Enabled structured headers and query parameters are copied. Disabled entries
  are omitted, repeated query parameters are preserved, and literal structured
  query values are URL encoded.
- Raw JSON bodies are converted to native `json` bodies. Quoted `{{count}}`
  references become `${data.count.string}` to keep their string type; unquoted
  references become `${data.count}` and use the case input's JSON type. Literal
  numbers are preserved or the import fails with a request for a case input.
- `noauth` and bearer authentication follow collection, folder, and request
  inheritance. A bearer token must be a complete named variable such as
  `{{token}}`. Literal bearer tokens and literal `Authorization`, `Cookie`, and
  `X-Api-Key` credentials are rejected.
- URL strings and structured URL objects are supported. Declared `:id` path
  parameters become case inputs.

All requests must share one origin. The original host is removed so execution
targets the local app or isolated Gregale app. A leading origin variable such
as `{{baseUrl}}` is removed too. If its exported value includes a path prefix
such as `https://api.example/v1`, `/v1` is retained in the native paths. An
undefined origin variable is assumed to represent an origin with no path prefix.
Review that prefix if an environment supplied a different URL. A collection that
calls multiple external services must be split into separate scenarios.

Other `{{variable}}` references become runtime `${data.field}` inputs.
Camel case names are converted to lowercase with underscores, so `customerId`
becomes `customer_id`. Import prints the required variable-to-field mappings;
colliding names are rejected. Exported values for these inputs are omitted.
Supply fresh test values in the existing JSON/CSV case file:

```json
[{"customer_id": "customer-a", "count": 10, "token": "local-test-token"}]
```

```sh
gregale test --validate --scenario api-collection --engine local --data cases.json
gregale test --scenario api-collection --engine local \
  --base-url http://localhost:3000 --data cases.json --junit results.xml
```

The importer does not read Postman environment files or reproduce variable
default values and scope changes. Supply those values as case inputs or explicit
native fixtures. Imported scenarios that use `${data.*}` currently run with the
local engine; real-VM case data is not supported yet.

### Scripts and unsupported features

An enabled collection, folder, or request script stops import by default.
To deliberately generate only the requests:

```sh
gregale test import --from collection.json --project export-api --requests-only
```

The import summary reports how many scripts were omitted. Port their assertions,
response captures, setup, and execution order changes into native steps before
using the scenario as a replacement for the collection's tests. Scripts are
never executed, even with `--requests-only`. That option does not allow other
unsupported request features.

Form data, files, binary and GraphQL bodies, variables in JSON object keys,
dynamic and vault variables, authentication types other than bearer/noauth,
custom proxies and certificates, and protocol profile options are rejected.
Native requests do not reproduce Postman's cookie jar or automatic redirects.
Collections are limited to 5 MiB, 100 requests, 16 folder levels, and 32 required
case fields. Import errors leave no partial manifest. `gregale --json test import`
returns request counts, fallback status counts, omitted script counts, and input
field mappings without copying variable values or script contents.

## Real-VM lifecycle tests

Run all three profiles, or select one:

```sh
gregale test --validate
gregale test --scenario customer-export --preflight
gregale test --scenario customer-export --report test-results.json
gregale test --scenario customer-export --profile restored --junit test-results.xml
gregale test --scenario customer-export --profile restored --repeat 3 --report repeated.json
gregale test --scenario customer-export --profile restored --repeat 3 --max-workload-minutes 135
gregale test --scenario customer-export --engine simulated
```

`--validate` checks every declared scenario and source directory without a
platform login or resource provisioning. Add `--scenario NAME` to validate one
scenario. `--preflight` also reads the current account plan, free developer app
slots, consumer-key allowance, async-invocation entitlement, and any required
managed PostgreSQL or object-storage entitlement. It shows how many workloads
must coexist and how many deployments the selected profiles will attempt;
the platform remains authoritative when provisioning begins. `--repeat N` runs
each selected profile in a fresh environment N times (1–20); preflight includes
these runs in its deployment estimate. Use it to reproduce intermittent
lifecycle failures. Preflight also shows the ceiling in workload-minutes:
workload count times per-run timeout times run count, assuming every VM runs
for the entire timeout. `--max-workload-minutes N` stops a real-VM run before
provisioning when that ceiling exceeds N. Actual compute billing follows VM
running time and plan allowances. This is a planning guard, not a billing cap:
cleanup time, lease expiry after an interrupted CLI, storage, and network usage
are outside this estimate.
`--junit PATH` writes one test case per profile and attempt alongside the JSON
report; both formats identify the execution engine and include cleanup failures.
The JSON report also records each run's start, finish, and elapsed time.
When a real-VM run fails, its report also includes up to 20 recent wake rows
and 30 request-telemetry rows per isolated workload. These diagnostics carry
request, trace, wake, and instance IDs, status, route, and timing metadata;
they exclude request bodies, headers, credentials, and app secrets. The real-VM
acceptance workflow uploads both the JSON and JUnit reports even on failure.

`real-vm` is the default engine. `--engine simulated` runs the separate
`simulation` command locally, once per requested attempt, without platform provisioning or login.
It receives `GREGALE_TEST_ENGINE=simulated`,
`GREGALE_TEST_PROFILE=simulated`, and `GREGALE_TEST_SCENARIO`. The report omits
VM wake evidence and labels its engine `simulated`. VM lifecycle profiles only
apply to `real-vm`, and passing `--profile` with `--engine local` or `simulated` is an
error. A simulation can test application logic quickly, while the real-VM runs
prove the platform lifecycle path.
Keep application behavior assertions in a shared module when both engines can
exercise them. The checked-in customer-export fixture uses one contract for
duplicate submission, ownership, unauthorized reads, direct worker access,
and notification delivery in both engines. Its simulation separately checks
the local queue model; the real-VM runner separately checks platform invocation
and wake evidence.

The assertion command receives `GREGALE_TEST_URL`,
`GREGALE_TEST_APP_SLUG`, `GREGALE_TEST_RUN_ID`, `GREGALE_TEST_PROFILE`, and
`GREGALE_TEST_ENGINE=real-vm`. Send application requests to
`GREGALE_TEST_URL`; it is a local pass-through proxy for the isolated app.
This lets the CLI record the first request's `X-Faas-Wake` and
`X-Faas-Wake-ID` without changing the application's request or response.
The setup and cleanup commands receive the same variables. Commands are
argument arrays, not shell strings. Additional services receive logical names
such as `http://worker.svc.gregale:10081` inside the platform. Local commands receive
`GREGALE_TEST_SERVICE_WORKER_URL` and
`GREGALE_TEST_SERVICE_WORKER_APP_SLUG`. Each service has its own source directory
and developer session. The base project plus service name must fit in 40
characters; plan developer-session quotas apply to every workload.
The CLI does not pass its `FAAS_TOKEN` account credential to scenario commands;
use the run-scoped credentials below for customer requests.
Declared `consumers` are created for the primary app before deployment. Local
commands receive each consumer's ID and key as
`GREGALE_TEST_CONSUMER_CUSTOMER_A_ID` and
`GREGALE_TEST_CONSUMER_CUSTOMER_A_KEY` (using the consumer name in uppercase,
with hyphens changed to underscores). A key has `read` and `write` scopes by
default; `scopes` can narrow or expand them. Send a key as an
`Authorization: Bearer` token. Set `consumer_auth_mode: required` to have
Gregale reject requests without a valid consumer key. Application assertions
can use both credentials to exercise the gateway's authentication and scope
rules. Ownership checks still depend on the application's own identity model.
The keys are never written
to the report. The runner revokes the consumers during cleanup, and their keys
expire one hour after the scenario timeout as a backstop. Consumer key plan
limits apply. Applications with their own authentication scheme can create
test identities in `setup` instead.
Each workload's normal `gregale.yaml` is applied during deployment. A worker
can declare `queue_bindings` and retry policy there; the runner waits for
queues on all test workloads when `wait_for.queue_idle` is enabled. Queue
binding plan gates still apply.
For a worker reached through a public async edge route, declare
`services.NAME.async_routes`. Gregale creates each route on the isolated
workload's actual hostname after deployment and waits for the fleet to apply
it. The worker URL in a sibling secret then reaches a durable HTTP 202 queue
entry before the worker processes the request.
The runner sets per-workload `secrets` on these expiring apps before deploying.
Values can reference `${service.NAME.url}`, `${service.NAME.slug}`,
`${bucket.NAME.name}`, `${run.id}`, or `${run.secret}`. The latter is a fresh
256-bit secret shared only with workloads that declare it, omitted from reports
and local command environments. The example gives the worker a test
notification endpoint. The built-in `delivery-sink` is deployed as another
real workload. `fail_first: 1` makes its first `POST /deliver` return 503 and
later deliveries return 200. The assertion command can inspect the sink's
recorded attempts with `GET /__gregale_test__/attempts` at
`GREGALE_TEST_SERVICE_NOTIFICATIONS_URL`, using
`Authorization: Bearer $GREGALE_TEST_SINK_NOTIFICATIONS_TOKEN`. The token is
only supplied to local commands and the sink. Custom notification services can
still use `source` instead of `fixture`. Secret values are sent to Gregale's
sealed secret API and are not included in the test report.
`wait_for.deliveries` waits for the configured number of attempts and final
status, then records their status sequence in the report. In cold and restored
runs it waits for the sink to wake after the trigger before reading attempts,
so the inspection request cannot create the lifecycle evidence itself.

For example, a Node assertion can check the failed delivery and successful
retry:

```js
import assert from "node:assert/strict";

const response = await fetch(
  `${process.env.GREGALE_TEST_SERVICE_NOTIFICATIONS_URL}/__gregale_test__/attempts`,
  { headers: { Authorization: `Bearer ${process.env.GREGALE_TEST_SINK_NOTIFICATIONS_TOKEN}` } },
);
if (!response.ok) throw new Error(`notification evidence: ${response.status}`);
const { attempts } = await response.json();
assert.deepEqual(attempts.map(({ status }) => status), [503, 200]);
```

`trigger` runs immediately after Gregale prepares the selected lifecycle
profile. It can submit the authenticated export request through
`GREGALE_TEST_URL`. If `wait_for.invocations` is declared, the trigger writes
a JSON object to the path in `GREGALE_TEST_TRIGGER_OUTPUT`, for example
`{"worker_invocation_id":"<id from the async 202 response>"}`. Gregale polls
that invocation by ID, checks that it belongs to the named isolated workload,
and requires its terminal state to be `completed`. Failed, cancelled, and
dead-letter invocations fail the scenario immediately. This ties completion
to the submitted export rather than an unrelated empty queue.
When `wait_for.queue_idle` is enabled, Gregale polls the isolated workloads'
queues until each has zero depth and in-flight work on two consecutive polls.
It also polls declared bucket prefixes until the minimum object count and
total size are present.
`${GREGALE_TEST_RUN_ID}` in an object prefix is replaced with the current run
ID. The assertion `command` then checks application-specific ownership,
authorization, deduplication, and delivery policy. The overall `timeout`
includes deployment, trigger, waiting, and assertions.

For each declared bucket, Gregale creates an isolated bucket and a compute
binding on the selected `service`, or on the primary app when `service` is
omitted. It exposes the bucket name to local commands
as `GREGALE_TEST_BUCKET_EXPORTS` and the binding's secret key prefix as
`GREGALE_TEST_BUCKET_PREFIX_EXPORTS`. The app receives the binding's sealed
S3 connection settings under that prefix. The runner deletes bucket objects,
the bucket, and then the app during cleanup. Bucket support requires object
storage to be enabled on the target Gregale installation.

The profiles establish these conditions immediately before the assertion
command:

| Profile | Preparation | Required first request |
|---|---|---|
| `warm` | Park and explicitly wake every workload, then wait for running instances. | The primary request has `X-Faas-Wake: hot` with no wake ID; each service has an app-handled hot request and no new wake. |
| `cold` | Drain the preview and invalidate its snapshots. | A wake ID whose completed boot method is `cold_boot`. |
| `restored` | Drain the preview while retaining its deployment snapshot. | A wake ID whose completed boot method is `restore`. |

The report fails a profile when the assertion command fails, sends no request
through the proxy, or when the completed wake method differs from the requested
profile. A restore that falls back to cold boot is recorded as cold boot and
fails the restored profile. Reports include the run ID, app slug, deployment ID,
first response status, wake headers, completed method, correlated invocation
state and attempt count, and cleanup outcome.
For cold and restored profiles, Gregale also compares each declared service's
wake timeline before and after the trigger. Every service must show a new wake
whose completed boot method matches the requested profile; the report records
these in `service_wake`. The warm profile explicitly wakes all workloads before
the trigger, verifies the primary app's first request is hot, and checks each
service's request telemetry against a pre-trigger baseline. A service must
handle a request on a running instance without a new wake; its request and
instance IDs appear in `service_hot`.

The assertion commands own application-specific expectations.
For an export test they should submit and retry the same
export request, inspect the produced object through both customers' credentials,
and check notification delivery. The delivery sink records each attempt's
status and body for retry assertions. Real platform reports label this path
`real-vm`; simulated runs never count as lifecycle proof.

`--profile cold` relies on the preview-only `fresh=true` form of
`POST /v1/apps/{slug}/park`. The API rejects that option for a production app.
This keeps the test's destructive snapshot invalidation inside its expiring
environment.

### Repeated resilience runs

A staged scenario with timed chaos recovery assertions can repeat the entire
scenario and require a minimum pass rate:

```yaml
scenarios:
  cache-resilience:
    project: my-app
    resilience:
      runs: 10
      min_pass_rate: 0.9
    # services, steps and recovery assertions as in the timed chaos example
```

`runs` must be between 2 and 20. `min_pass_rate` is greater than 0 and at most 1;
Gregale rounds the required successful run count up (0.9 requires 9 of 10).
Each run gets a fresh application and service namespace, starts with the first
step's HTTP baseline assertions, executes the complete staged scenario, and
finishes fault and resource cleanup before the next run starts. All existing
assertions, including recovery-time budgets, must pass for a run to count.
Ordinary assertion failures are retained and the remaining runs continue.
Cleanup failures stop the group, mark remaining runs skipped, and fail the
group regardless of pass rate. Interruption and skipped runs also fail the
group. `--fail-fast` applies to groups; `--repeat` repeats the complete group.
The scenario timeout applies separately to each run. Preflight deployment and
workload-minute estimates include every configured run.

The scenario's final status and CI exit code use the required pass rate. JSON
includes `resilience.history` with each run's IDs, timestamps, steps, load,
errors, and cleanup evidence. Terminal and HTML output show pass rate and
recovery-time p50/p95 per step and phase. Percentiles use confirmed successful
recovery assertions, including those from runs that failed a later assertion;
failed recovery assertions are excluded and sample counts are shown. JUnit
has one case per group, with all individual run evidence in `system-out`, so
an allowed failed run does not fail CI independently of the group budget.

### Dependency fault matrices

Add `resilience.fault_matrix` to repeat a staged scenario with different rules
in a selected timed fault phase. This example replaces the `cache-unavailable`
phase in the earlier `continuous-resilience-load` step:

```yaml
resilience:
  runs: 10
  min_pass_rate: 0.9
  fault_matrix:
    - name: latency
      step: continuous-resilience-load
      phase: cache-unavailable
      rules:
        - {to: cache, kind: tcp_latency, port: 6379, direction: downstream, latency: 250ms, percent: 100, min_matches: 1}
    - name: bandwidth
      step: continuous-resilience-load
      phase: cache-unavailable
      rules:
        - {to: cache, kind: tcp_bandwidth, port: 6379, direction: downstream, rate_kib_per_second: 16, percent: 100, min_matches: 1}
    - name: timeout
      step: continuous-resilience-load
      phase: cache-unavailable
      rules:
        - {to: cache, kind: tcp_connect_timeout, port: 6379, percent: 100, min_matches: 1}
    - name: reset
      step: continuous-resilience-load
      phase: cache-unavailable
      min_pass_rate: 1
      recovery:
        within: 5s
        sustain: 5s
        window: 1s
        thresholds: {p95: 200ms, error_rate: 0.01, min_samples: 5}
      rules:
        - {to: cache, kind: tcp_reset, port: 6379, direction: downstream, reset_after: 100ms, percent: 100, min_matches: 1}
```

Matrices require 2–10 uniquely named cases. Each case names an existing fault
phase immediately followed by a recovery assertion and supplies replacement
`rules`; all other phases, assertions, and
services remain part of that case. The template scenario must also be valid.
Rules use the existing workload, TCP-port, duration, and match-count checks.
A case inherits the resilience run count and minimum pass rate. Optional
`min_pass_rate` replaces that case's pass-rate budget; optional `recovery`
replaces the full recovery assertion immediately after its target phase.
Each variant is validated before any resources are created.

Cases run sequentially. Each case executes the configured number of fresh
scenario runs and produces independent pass-rate and recovery p50/p95 results.
The matrix passes only when every case passes; one case's good results cannot
mask another case's failure. Assertion failures allow later cases to run;
cleanup failure or interruption skips remaining cases and fails the matrix.
`--repeat` repeats the whole matrix and `--fail-fast` applies to its final
result. Resource estimates include cases × runs × profiles × CLI repeats.

Terminal and HTML output show named case results. JSON stores case groups in
`fault_matrix.cases`, with individual receipts under each case's
`resilience.history`. JUnit uses one case for the matrix and includes all
case and run evidence in `system-out`; any failed matrix case fails CI.

### Baseline-relative recovery budgets

Recovery assertions can derive their limits from the fault-free phase immediately
before the fault. For example, require p95 to return to within 20% of normal:

```yaml
- name: recovery
  duration: 15s
  recovery:
    within: 10s
    sustain: 5s
    window: 1s
    thresholds: {min_samples: 5, error_rate: 0.01}
    relative:
      baseline_min_samples: 20
      p95_increase_percent: 20
```

`relative` supports `p95_increase_percent`, `p99_increase_percent`,
`error_rate_increase`, and `success_rate_drop`. Percent limits are between 0
and 1000; rate changes are fractions between 0 and 1. Explicit zero requires
no degradation. `baseline_min_samples` must be between 1 and 100000 and at
least one relative metric must be set. `thresholds.min_samples` remains
required for every recovery window. Absolute latency and rate budgets are
optional when relative metrics are present; both sets must pass if configured.

Only requests started in the baseline phase and completed before the fault
begins contribute to its metrics. Requests overlapping the fault are excluded
so injected slowness cannot relax the recovery budget. Too few completed
baseline samples fail recovery. Relative latency budgets also require a
positive baseline percentile. Each repeated run and fault-matrix case measures
its own baseline; no baseline is shared across runs.

Latency limits are baseline percentile × (1 + allowed percent / 100). Error-rate
limits add the allowed fraction (capped at 1); success-rate limits subtract it
(floored at 0). The same rolling-window, recovery deadline, and sustain rules
apply. JSON retains the baseline phase and metrics plus the derived limits,
observed values, and results at the confirming window (or final checked window
on failure). Terminal and HTML reports show those comparisons. Scenario and
JUnit failures identify the recovery phase and failed relative metrics.

### Recovery regressions between CI runs

Set a recovery regression budget on a resilience scenario:

```yaml
resilience:
  runs: 10
  min_pass_rate: 0.9
  recovery_regression:
    p95_increase_percent: 20
    min_samples: 5
  # optional fault_matrix cases
```

First record a successful run with this CLI, then compare a later run:

```bash
gregale test --scenario cache-resilience --profile warm --report previous.json
gregale test --scenario cache-resilience --profile warm --recovery-baseline previous.json --report current.json --junit current.xml --html current.html
```

The saved report is selected explicitly; Gregale does not fetch CI artifacts or
automatically choose a historical run. `--recovery-baseline` requires `real-vm`
resilience scenarios declaring `recovery_regression`, and cannot be combined
with `--baseline`, validation, or preflight. For suites, every selected member
must satisfy those requirements. The budget alone permits recording a baseline;
comparison runs only when the flag is supplied.

`p95_increase_percent` allows 0–1000% (zero permits no increase).
`min_samples` must be between 2 and the configured number of resilience runs.
Every recovery phase in every matrix case must have that many confirmed
recovery samples in both reports. Percentiles are recomputed from individual
run evidence, including confirmed recoveries before a later assertion failure;
failed or unconfirmed recovery assertions contribute no samples. Missing,
incomplete, duplicate, or invalid recovery evidence is rejected.

Reports must match scenario, engine, lifecycle profile, CLI repeat number, and
named matrix cases. Each repeated group records a fingerprint of request
assertions, chaos rules, timing, recovery budgets, declared TCP ports and
scenario timeout. Changed experiment settings require a new baseline; application
source code can change. Older reports without this metadata must be recorded
again. Only previously successful groups with successful cleanup are accepted,
and baseline validation happens before deployment. The baseline file is read
only; output files cannot overwrite it.

A recovery p95 above previous p95 × (1 + allowed percent / 100), or insufficient
current samples, fails the scenario and CI. Comparisons are independent per
matrix case, step, and phase. Current runs that fail execution or cleanup are
marked `not_compared` and retain their original failure. JSON, terminal, and
HTML reports show previous/current p95, sample counts, percentage change, limit,
and result, plus the baseline report digest. JUnit failure messages identify
the regressed case and phase and include full evidence in `system-out`.

### Retry amplification during HTTP faults

A timed HTTP fault phase can limit dependency traffic per named incoming load
request. Add this to the fault phase alongside its `name` and `duration`:

```yaml
rules:
  - from: app
    to: inventory
    kind: http_status
    percent: 100
    status_code: 503
retry_amplification:
  request: checkout
  max_attempts_per_request: 3
  min_requests: 20
```

`checkout` must name exactly one request or check in the load journey. Its request
count includes successful and failed requests started during that phase; other
journey steps do not contribute to the denominator. The numerator is the captured
HTTP fault match count for the selected caller and dependency. Exactly one rule,
100% coverage, and an explicit `from` workload are required. HTTP `latency` and
`http_status` rules are supported. TCP connections and DNS lookups cannot measure
HTTP request retries and are rejected for this assertion.

This is an aggregate traffic ratio, not trace-level attribution. Background calls
from the selected caller also contribute. Requests overlapping phase boundaries
can contribute to different windows at ingress and at the dependency. Use long
enough phases and sufficient samples for the workload. Gateway-internal retries
that bypass fault evaluation are not included. A zero dependency count is allowed;
use the rule's `min_matches` when the dependency must actually be exercised.

Missing fault evidence or too few incoming samples fails the assertion. A ratio
above the budget fails the run and CI. JSON, terminal, HTML, and JUnit reports
retain the counts, ratio, budget, and failure reason independently for every run
and matrix case.

### TCP connection-attempt budgets

A timed TCP fault phase can limit application connection attempts per named
incoming load request:

```yaml
rules:
  - from: app
    to: cache
    kind: tcp_connect_refused
    port: 6379
    percent: 100
connection_attempts:
  request: checkout
  max_attempts_per_request: 3
  min_requests: 20
```

Use exactly one TCP fault rule with an explicit caller, dependency, declared TCP
port, and 100% coverage. All TCP fault kinds support this budget. `request` must
name exactly one request or check in the phase's load journey. Its count includes
successful and failed requests started in that phase; other load steps are
excluded from the denominator.

The gateway counts each authorized application connection reaching the TCP chaos
path before forwarding. Injected connect refusals/timeouts, failed upstream dials,
and idle connections are included. Reads, writes, and policy refreshes on pooled
sockets do not increment this counter. Connections already open when the fault
begins are excluded. The proxy's own retries across backend replicas still belong
to one application connection attempt. Connections rejected before the chaos path
(for example, authorization or session limits) are excluded.

Counts are isolated by fault-plan generation, caller, dependency, and port. They
are separate from fault-match counts: a socket can attempt a connection without
exchanging any payload or experiencing a stream fault. Background connections
from the selected caller also contribute; this is a phase-wide ratio, not
trace-level attribution. Requests and connections overlapping phase boundaries
can land in different windows.

Too few incoming request samples, an exceeded budget, or incomplete attempt
evidence fails the run and CI. The gateway must support connection-attempt
recording. Missing counters, including a phase with no new connections, fail
closed instead of being interpreted as zero. This makes these budgets suitable
for exercises that create new connections; omit the budget when intentionally
reusing an existing pool without reconnecting. JSON, terminal, HTML, and JUnit
reports include the route, port, attempt/request counts, ratio, budget, and result
for every run and fault-matrix case. Recovery-baseline fingerprints include the
budget when configured.

### Circuit-breaker activity assertions

Add `circuit_breaker` to a timed fault phase to verify dependency traffic
suppression, probe limits, and sustained traffic after the fault clears:

```yaml
load:
  duration: 10s
  rate: 40
  vus: 2
  chaos_schedule:
    - name: baseline
      duration: 2s
    - name: dependency-outage
      duration: 4s
      rules:
        - from: app
          to: cache
          kind: tcp_connect_refused
          port: 6379
          percent: 100
      circuit_breaker:
        counter_path: /test/cache-attempts
        # as: tester  # optional declared consumer for the counter endpoint
        request: checkout
        min_requests: 5
        stop_within: 2s
        window: 1s
        max_probes: 1
        resume_within: 2s
        resume_min_attempts: 5
    - name: recovery
      duration: 4s
```

`checkout` must name exactly one incoming request or check in the load journey.
The fault phase requires one 100% rule with an explicit caller, between two
fault-free phases. HTTP, TCP, and DNS faults can use this assertion. Existing
staged-scenario baseline and subsequent recovery-step requirements still apply.

The application must expose a counter endpoint returning HTTP 200 with a single
JSON object, for example `{"attempts": 123}`. Count all application attempts to
the selected dependency, including failed attempts and background probes. The
counter must increase monotonically and cover the entire isolated run, across
all replicas. It must continue recording after the fault clears. Reading the
endpoint must not call the dependency or increment its counter. A run-scoped
shared counter is appropriate; a different replica's process-local counter can
reset or undercount and is unsuitable. Gregale does not derive this timeline
from aggregate gateway fault-match counts.

`counter_path` is a local absolute path on the application under test. Templates,
fragments, and external URLs are rejected. `as` optionally uses a declared
scenario consumer's credential. Redirects are refused. Responses are bounded to
4 KiB and must contain only a nonnegative int64 `attempts` value. Counter values
are held in memory; reports retain window deltas, not endpoint response bodies or
credentials. Reads run separately from the load journey and are excluded from
its request counts and latency measurements, though they add application traffic.

Gregale samples every one-tenth of `window`, bounded to 100–250 ms. It evaluates
rolling windows at each sample. Baseline windows must reach
`resume_min_attempts`; the first fault window at or below `max_probes` must finish
within `stop_within`. Every later fault window must remain within the probe
budget. After clearing the fault, a window reaching `resume_min_attempts` must
finish within `resume_within`, and every later complete recovery window must
maintain that activity. Both suppression and resumption need confirmation for at
least another complete window. `resume_min_attempts` must exceed `max_probes`.
Choose the recovery activity threshold to represent normal traffic for your
workload; Gregale does not infer it from the baseline.

Every evaluated window must contain at least `min_requests` starts of the named
incoming request, including failures. Paused load cannot appear to be a healthy
open breaker. Baseline must fit a window plus a polling interval; fault and
recovery phases must fit their deadline plus a sustained window. Durations are
whole milliseconds from 1s to 5m, within the existing schedule limits.

This checks observed traffic behavior, not the application's internal breaker
state. Confirmation times are measured at the end of a complete observation
window. Sampled windows can be slightly longer than `window`, and timestamps
reflect receipt of the counter response; use budgets with measurement headroom.
Background dependency attempts count toward probe limits. Request/counter windows
crossing phase boundaries are excluded. Counter errors, resets, excessive sampling
gaps, insufficient incoming traffic, incomplete phases, probe storms, and late or
unstable recovery fail the run and CI.

JSON, terminal, HTML, and JUnit reports show confirmation times, budgets, and
per-window dependency/incoming counts. Existing resilience histories and fault
matrices retain these results independently for each run. Combine these assertions
with existing recovery SLO assertions when using resilience runs or fault matrices;
recovery p95 regression comparisons still apply to the existing SLO recovery
measurements. Workload fingerprints include configured circuit-breaker budgets.

### Bulkhead isolation with per-endpoint phase budgets

Use `step_thresholds` in a timed chaos phase to keep unrelated endpoints healthy
while a dependency is impaired:

```yaml
- name: cache-slow
  duration: 20s
  rules:
    - from: app
      to: cache
      kind: tcp_latency
      port: 6379
      percent: 100
      latency: 500ms
      min_matches: 1
  step_thresholds:
    health:
      p95: 200ms
      error_rate: 0.01
      min_samples: 20
    unrelated-search:
      p95: 300ms
      p99: 500ms
      success_rate: 0.99
      min_samples: 20
```

The keys name native HTTP requests or checks in the same staged load journey.
Budgets support `p95`, `p99`, `error_rate`, `success_rate`, and `min_samples`.
Each endpoint budget requires a positive `min_samples` and at least one latency
or rate limit. Unknown names and invalid budgets are rejected before deployment.
Budgets can be attached independently to baseline, fault, and recovery phases.
Use the fault rule's `min_matches` to require evidence that the dependency was
actually exercised under the fault.

Metrics use only requests started in that phase for that named HTTP step, even
when a response completes after the phase boundary. Rates follow existing load
semantics: failed HTTP assertions contribute to `error_rate`, and passed
assertions contribute to `success_rate`. No overall or other-phase budget is
inherited. Aggregate phase, whole-load, whole-load endpoint, and per-phase
endpoint budgets must all pass when configured. Adjust aggregate budgets to allow
the intended degradation of the affected request without relaxing the protected
endpoint's budgets.

A healthy aggregate cannot hide a protected endpoint's latency or errors. Empty
or undersampled endpoints fail their sample floor instead of appearing healthy.
Requests within a journey execute sequentially and stop when a request fails.
Place protected probes before an expected failing request, or give that request
an appropriate response expectation, so probes can still run. Use enough VUs and
suitable sample floors to exercise overlapping traffic. These assertions check observed
endpoint isolation; they do not create independent per-endpoint traffic streams
or identify a particular pool or worker as the cause of a failure.

Terminal, JSON, HTML, and JUnit reports include endpoint samples, metrics, budgets,
and outcomes. Failures name both the chaos phase and HTTP step and fail the run
and CI. Resilience histories and fault-matrix cases retain independent results.
Recovery-baseline fingerprints include configured `step_thresholds`; adding or
changing the budgets requires recording a compatible baseline.

### Independent concurrent load streams

Use `load.streams` to keep health probes running while checkout calls wait on a slow dependency:

```yaml
load:
  duration: 30s
  streams:
    - name: checkout-load
      requests: [checkout]
      rate: 20
      vus: 20
    - name: health-probes
      requests: [health]
      rate: 5
      vus: 2
  thresholds:
    steps:
      health:
        p95: 200ms
        error_rate: 0.01
        min_samples: 100
```

`requests` selects named native requests or checks from this load step, in execution order. Every request must belong to exactly one stream. A stream executes its own sequential journey with private captures and unique iteration IDs; failure stops that journey, while the other streams continue. Captures produced by load requests can only be referenced later in the same stream. Captures supplied by an earlier staged step remain available.

Each stream has its own arrival rate and VU admission limit. Rate counts journeys per second, so a journey containing multiple requests can generate more HTTP requests. A busy stream drops arrivals instead of borrowing another stream's capacity, and any dropped arrival fails the load run. Reports retain each stream's rate, capacity, scheduled arrivals, and drops, alongside the existing endpoint metrics and budgets.

Streams share one duration, fault schedule, phase clock, and global HTTP-request limit. Existing `thresholds.steps` and phase `step_thresholds` apply to their named endpoints, so health can have independent latency, error, and sample budgets throughout baseline, fault, and recovery. Aggregate recovery budgets continue to include all streams; streams do not change recovery semantics.

Declare 2–20 streams with positive rates and explicit VUs, totaling at most 50 VUs and 1000 journeys/s. The planned HTTP-request total must fit the 100,000 request limit. Streams require `duration`; top-level VUs, rate, iterations, pacing, and stages (and their CLI overrides) cannot be combined with streams. The duration override remains supported and must still agree with any fault schedule.

### Per-endpoint recovery assertions

Use `step_recovery` on a fault-free phase immediately after a fault to require each named request or check to recover independently. For example, in a 15-second recovery phase:

```yaml
- name: recovery
  duration: 15s
  step_recovery:
    checkout:
      within: 5s
      sustain: 10s
      window: 1s
      thresholds:
        p95: 200ms
        error_rate: 0.01
        min_samples: 10
    health:
      within: 2s
      sustain: 3s
      window: 1s
      thresholds:
        p95: 100ms
        success_rate: 0.99
        min_samples: 5
```

Every endpoint assertion must pass, along with any aggregate `recovery` assertion and other SLO budgets. Each has its own timing and thresholds; it does not inherit budgets from other endpoints or the aggregate. An endpoint needs a positive sample floor in **every rolling window**, plus a latency or rate budget. Healthy traffic from other streams cannot satisfy that floor or dilute endpoint errors. Use independent `load.streams` to keep protected probes running when another journey fails.

The rolling-window semantics match aggregate recovery: only requests started in this recovery phase are eligible, and they contribute upon completion with their full latency. Empty or undersampled windows, pending requests, late recovery, and failures during the sustain period cannot confirm recovery. Recovery time is the end of the first healthy window in the sustained sequence, measured from the actual fault-clear acknowledgement. `window` must fit `within`, and `within + sustain` must fit the phase duration. Timing values must be whole milliseconds between 100ms and 5m.

Each endpoint can also use the existing recovery `relative` budgets. Those compare only that endpoint's completed requests in the healthy phase immediately before the fault; overlapping requests and other endpoints cannot relax its baseline. Endpoint assertions alone qualify a scenario for repeated resilience runs and fault matrices. Existing matrix `recovery` overrides replace the aggregate assertion; endpoint assertions retain their declared budgets.

Terminal, HTML, JSON, and JUnit reports identify each endpoint's result and recovery time. Repeated-run summaries and recovery-regression baselines track each endpoint separately, with their own confirmed-sample requirements. Changing endpoint recovery budgets changes the recovery workload fingerprint, so record a matching baseline.

### Automatic recovery failure diagnostics

When a real-VM run misses an aggregate or endpoint recovery assertion, Gregale automatically collects a diagnostic bundle **before destroying the isolated workloads**. No extra YAML is required. Failed assertions reference the bundle by `diagnostics_ref`; HTML reports link directly to it. JSON and JUnit retain the same bundle, and terminal output lists its identifier, workload excerpts, and collection errors.

The bundle identifies the staged step, recovery phase, and endpoint, with request/failure counts and p95 latency for that recovery phase. It includes bounded fault-match evidence from the preceding phase. Log timestamps are restricted to a window around recovery deadlines (five seconds before and after, capped at the phase finish and collection time); multiple failed assertions share a bundle spanning their windows. The metrics summarize requests started in the recovery phase, rather than individual rolling-window requests.

Application logs are fetched as a finite page with follow disabled. Collection shares the existing 15-second diagnostic deadline, allows at most two seconds per workload, and visits at most eight workloads. Each page is limited to 64 KiB of input and 20 excerpts, with 1 KiB per log line and 16 KiB of serialized excerpts per workload. The complete bundle is limited to 256 KiB; truncation, missing retained log history, and unavailable telemetry are reported explicitly. Only retained logs within the requested window can be captured; collection does not enable debug telemetry or log retention.

Known operator, run, fixture, consumer, configured app-secret, and credential-header values are redacted. Credential- or request-body-labelled log lines and opaque credential-like values are redacted; request headers, bodies, and raw API error responses are excluded from the bundle. Redaction is best effort for arbitrary application-written logs, so applications should avoid logging secrets. Platform request metadata remains bounded, strips query strings, and excludes wake-trigger payloads.

Diagnostic failures never replace the original test error, alter pass/fail status, or prevent the normal cleanup path. Healthy recovery assertions do not trigger log collection. Every repeated resilience run collects its own evidence; the bundle stays with that run's receipt.

### Replaying a failed isolated run

New real-VM failures include a versioned `replay` manifest in their JSON receipt.
It identifies the scenario, lifecycle profile, matrix case, and SHA-256 of the
effective configuration, including load streams, fault schedule, and recovery
budgets. It stores no configuration or resolved credential values. Save the
original run with `--report failed.json`, then select the failed leaf's `run_id`
from the report (matrix cases contain resilience `history` entries):

```sh
gregale test replay --from failed.json --run-id RUN_ID --manifest gregale-test.yaml --validate
gregale test replay --from failed.json --run-id RUN_ID --manifest gregale-test.yaml --report replay.json --html replay.html
```

Replay verifies the original manifest's effective configuration before platform
access, selects the original fault-matrix variant, and executes exactly one run
in a fresh isolated environment. Secrets are resolved again by the normal runner;
repetitions, matrix expansion, and historical regression comparisons are omitted.
The replay receipt links to the original through `replay_of`. Normal cleanup and
failure diagnostics still apply. Input reports and manifests cannot be overwritten
by report outputs. Reports from before replay support cannot be replayed.

This reproduces the declared configuration, not timing or external state. Keep
application and service sources at the original revision: source contents,
dependency versions, generated credentials, and platform state are not pinned by this
manifest. Changed literal configuration (including literal secrets) is rejected;
generated `${run.secret}` values, consumer keys, and resource references are recreated at replay time.
Replay is currently available for failed real-VM leaf runs, not local/simulated
runs or group-only regression failures.

### Minimizing a recovery failure's fault rules

`gregale test minimize` searches for fewer fault rules that repeatedly fail one
selected recovery assertion. It accepts an original failed real-VM leaf run saved
with replay support, including a selected fault-matrix case. Select aggregate
recovery as `STEP/PHASE`, or a named endpoint as `STEP/PHASE/REQUEST`. The immediately
preceding fault phase must contain 2–20 rules. Only that phase's rules are reduced;
load streams, timing, thresholds, other phases, and application configuration stay
as declared.

```sh
gregale test minimize --from failed.json --run-id RUN_ID \
  --manifest gregale-test.yaml --assertion load/recovery/checkout \
  --max-runs 10 --max-workload-minutes 180 --validate

gregale test minimize --from failed.json --run-id RUN_ID \
  --manifest gregale-test.yaml --assertion load/recovery/checkout \
  --max-runs 10 --max-workload-minutes 180 \
  --report minimized.json --html minimized.html
```

The original configuration must fail the selected assertion in two fresh isolated
runs before reduction starts. Each candidate removes one rule and runs twice. Two
matching failures accept the removal; two passing runs retain the rule. The search
restarts after an accepted removal so earlier retained rules can be reconsidered.
Every run uses the normal provisioning, cleanup, and failure diagnostics paths.

The selected failure must have completed recovery windows and evidence that the
preceding fault matched traffic. Interrupted loads, unavailable evidence, unrelated
failures, mixed outcomes, or a cleanup failure stop the search as `inconclusive`.
A cleanup failure prevents further runs. `--max-runs` includes all confirmations
and is capped at 20. A positive `--max-workload-minutes` is mandatory; it reserves
the normal timeout-based workload estimate before each two-run candidate. If a
complete pair does not fit either remaining budget, the search stops without
starting that pair. These are estimated workload-minutes, not a billing guarantee.

Completed searches report `minimized` or `unchanged` and exit zero. Inconclusive
searches exit nonzero and preserve the last confirmed result, if one exists. This
is a diagnostic command: expected assertion failures in its trials do not make a
completed search fail. `--report` is required for execution; JUnit and HTML outputs
are optional. Reports include the target, original/remaining rule counts, budgets,
all trial receipts, removal indices, and a `best_run_id` for the confirmed replay.

```sh
gregale test replay --from minimized.json --run-id BEST_RUN_ID \
  --manifest gregale-test.yaml --validate

gregale test replay --from minimized.json --run-id BEST_RUN_ID \
  --manifest gregale-test.yaml --report reduced-replay.json
```

Reduced replay manifests use version 2. They reference removed rules by step,
phase, and zero-based index in the original rule list, and verify both the original
and reduced configuration hashes. They contain no rule bodies or secret values.
Use the original manifest; even changes to removed rules are rejected. Older
Gregale versions reject these manifests. A failure from an already reduced replay
cannot be used for another rule-removal search; severity and recovery modes accept reduced replays.

The result is minimal under valid individual removals within the selected phase,
with at least one rule retained. Removing the last rule would change schedule and
recovery semantics, so it is excluded. Invalid candidates are recorded without
provisioning. The result does not establish the globally smallest combination or
prove fault causality; two confirmations cannot eliminate all nondeterminism.
Application sources and external state have the same limits as failure replay.

### Minimizing HTTP or TCP latency

Use `--mode latency` to reduce one `latency` or `tcp_latency` rule in the fault
phase immediately before the selected recovery assertion. That phase may contain
1–20 rules. `--rule-index` is required and refers to the zero-based index in the
original manifest, including when earlier minimization removed other rules.
Other fault settings, rules, load, and recovery budgets are preserved.

```sh
gregale test minimize --from minimized.json --run-id BEST_RUN_ID \
  --manifest gregale-test.yaml --assertion load/recovery/checkout \
  --mode latency --rule-index 1 --resolution 1ms \
  --max-runs 20 --max-workload-minutes 180 --report latency.json --html latency.html
```

The search confirms the starting failure twice, tries 1ms twice, then bisects the
passing/failing interval until its width is at most `--resolution` (default 25ms).
Latency and resolution must be whole milliseconds between 1ms and 30s. Every run
must show that the selected rule matched traffic. The existing confirmation,
cleanup, interruption, and budget limits apply. Mixed outcomes or budget exhaustion
produce `inconclusive`, preserving the best confirmed replay.

Reports include starting and mildest confirmed tested latency, any tested passing
lower bound, resolution, and all candidate receipts. This search assumes latency
severity is useful to bisect; it does not establish monotonic behavior, a global
minimum, or that every smaller latency is safe. Replay manifests with latency
changes and optional removals use version 3; inherited bandwidth changes require
version 4, version 5 when percentage changes are inherited, or version 6 when
recovery deadline changes are inherited. They retain original rule indices and verify both original and reduced
configuration hashes. A replay supports at most
20 latency overrides; replacing an existing override uses no additional slot.
Use the original manifest and `best_run_id` with `gregale test replay`; older binaries reject version 3.

### Minimizing TCP bandwidth restrictions

Use `--mode bandwidth` to increase the allowed rate of one `tcp_bandwidth` rule
until the selected recovery failure no longer reproduces. Select its zero-based
index in the original manifest with `--rule-index`, including after earlier rule
removals. The immediately preceding fault phase may contain 1–20 rules. Other
rules, direction, traffic percentage, timing, load, and recovery budgets remain
unchanged; inherited latency and bandwidth reductions are preserved.

```sh
gregale test minimize --from failed.json --run-id RUN_ID \
  --manifest gregale-test.yaml --assertion load/recovery/checkout \
  --mode bandwidth --rule-index 1 --max-rate-kib-per-second 1024 \
  --rate-resolution-kib-per-second 16 \
  --max-runs 20 --max-workload-minutes 180 \
  --report bandwidth.json --html bandwidth.html
```

`--max-rate-kib-per-second` is required, must exceed the starting rate, and is
bounded at 1,000,000 KiB/s. `--rate-resolution-kib-per-second` defaults to 1 and
must be between 1 and 1,000,000. These are integer KiB/s (1 KiB = 1024 bytes);
`--resolution` applies only to latency mode. Add `--validate` instead of report
outputs to check configuration and budgets without credentials or provisioning.

The starting failure is confirmed twice. The search then tests the ceiling twice;
if it still fails, that is the highest confirmed tested rate within the requested
range. If it passes, the search bisects the failing/passing interval until its width
is at most the rate resolution. Each candidate requires two agreeing runs and
positive match evidence for the selected rule. The existing run and workload-minute
budgets include all confirmations. Flaky results, unrelated failures, unavailable
evidence, cancellation, cleanup failure, or exhausted budgets produce
`inconclusive` and preserve the best confirmed replay; cleanup failure prevents
further runs.

JSON, HTML, JUnit, and terminal reports include the highest confirmed tested rate,
starting rate, ceiling, resolution, and any tested passing upper bound. This is
bounded tested evidence; the search does not prove monotonic behavior, a global
boundary, or that every higher bandwidth is safe. Replay manifests containing
bandwidth changes use version 4, version 5 when percentage changes are inherited,
or version 6 when recovery deadline changes are inherited, and verify original and reduced hashes. Each
replay supports up to 20 bandwidth overrides alongside up to 20 latency overrides;
replacing an existing override consumes no extra slot. Use the original manifest
and `best_run_id` with `gregale test replay`; older binaries reject version 4.

### Minimizing a fault's selection percentage

Use `--mode percent` to lower one selected fault rule's configured percentage,
while preserving its kind, seed, severity, direction, load, and recovery budgets.
Select the zero-based index in the original manifest with `--rule-index`; for a
fault-matrix failure, use the selected case's rule order. Earlier rule removals and latency, bandwidth, or percentage reductions are preserved.
The immediately preceding fault phase may contain 1–20 rules. All supported HTTP,
TCP, and DNS fault kinds can use percentage minimization.

```sh
gregale test minimize --from failed.json --run-id RUN_ID \
  --manifest gregale-test.yaml --assertion load/recovery/checkout \
  --mode percent --rule-index 1 --percent-resolution 1 \
  --max-runs 20 --max-workload-minutes 180 \
  --report percent.json --html percent.html
```

`--percent-resolution` defaults to 1 and must be an integer from 1 to 100
percentage points. The search confirms the starting failure twice, tests 1% twice,
and then bisects a passing/failing interval until its width is at most the chosen
resolution. A starting rule already at 1% needs only the two confirmations and
reports `unchanged`. Zero percent is excluded because disabling the rule changes
fault participation. Only percentage-specific flags apply in this mode; latency
and bandwidth resolution flags are rejected. Use `--validate` instead of report
outputs for offline validation without provisioning.

Each candidate must complete two agreeing runs and show positive match evidence
for the selected rule. A candidate that matches no events is `inconclusive`, not
evidence that the application tolerates that percentage. The existing run and
workload-minute budgets reserve complete pairs and include the starting
confirmations. Mixed outcomes, unrelated failures, missing evidence, cancellation,
cleanup failure, or exhausted budgets stop the search as `inconclusive`, retaining
the last confirmed result. Cleanup failure prevents further runs.

Reports show starting and mildest confirmed tested percentage, resolution in
percentage points, any tested passing lower bound, and all trial receipts. The
configured percentage governs eligible HTTP requests, TCP connections or connection
attempts, or DNS queries according to the fault kind. It is not a measured fraction
of bytes or all application traffic; finite samples, pooling, and caching affect
observed matches. The selected seed is preserved, but fresh runs can still differ.
The result does not prove monotonic behavior, a global minimum, fault causality, or
that every smaller percentage is safe.

Replay manifests containing percentage changes use version 5, or version 6 when
recovery deadline changes are inherited. They retain original rule indices and other
reductions, and verify original and reduced configuration
hashes. Each replay supports up to 20 percentage overrides in addition to the
existing latency and bandwidth limits; replacing an override consumes no extra
slot. Use the original manifest and `best_run_id` with `gregale test replay`.
Older binaries reject version 5.

### Finding a passing recovery deadline

Use `--mode recovery` to find the shortest confirmed tested deadline for one
selected recovery assertion after a reproducible real-VM failure. Select aggregate
recovery with `STEP/PHASE`, or a named endpoint with `STEP/PHASE/REQUEST`. The
immediately preceding fault phase may contain 1–20 rules. All fault settings,
load, phase durations, sustain time, observation window, relative baselines,
thresholds, and other recovery assertions remain fixed. Earlier reductions are
preserved. This mode does not require `--rule-index`.

```sh
gregale test minimize --from failed.json --run-id RUN_ID \
  --manifest gregale-test.yaml --assertion load/recovery/checkout \
  --mode recovery --max-recovery-within 10s --recovery-resolution 100ms \
  --max-runs 20 --max-workload-minutes 180 \
  --report deadline.json --html deadline.html
```

`--max-recovery-within` is required and must exceed the starting failed deadline.
It must be whole milliseconds from 100ms to 5m, and the deadline plus existing
sustain time must fit inside the existing recovery phase. Gregale rejects ceilings
that would require longer load or phase durations. `--recovery-resolution` defaults
to 25ms and must be whole milliseconds from 1ms to 5m. Severity-specific flags are
rejected in this mode. Use `--validate` instead of report outputs to check selection
and budgets offline.

The starting deadline must fail in two fresh runs. Gregale then tests the ceiling
in two runs. If it passes twice, the search bisects the failing/passing interval
until its width is at most the resolution, keeping the shortest deadline that
passes twice. If the ceiling fails twice, no passing deadline has been established
and the search is `inconclusive`. Each run must complete the fault and recovery
phases with match evidence for the fixed fault configuration. Other failures,
mixed outcomes, missing or mismatched deadline evidence, cancellation, cleanup
failure, or exhausted budgets also produce `inconclusive`. Complete confirmation
pairs count toward the existing run and workload-minute limits. A confirmed
passing result survives a later inconclusive stop; cleanup failure prevents
further runs.

Reports include the starting failed deadline, shortest confirmed passing deadline
when found, tested failing bound and run ID, ceiling, resolution, and all trials.
`best_run_id` identifies a passing trial, so replay can verify the successful
recovery budget. Until a passing pair is confirmed, there is no best passing run
or replay descriptor. `recovery.failed_run_id` identifies a confirmed failed trial
that can also be replayed. Passing deadline trials carry replay descriptors;
ordinary passing scenario runs remain outside this replay selection.

```sh
gregale test replay --from deadline.json --run-id BEST_RUN_ID \
  --manifest gregale-test.yaml --validate
```

Deadline replay manifests use version 6, verify original and changed configuration
hashes, and support up to 20 deadline overrides keyed by step, phase, and optional
request. Endpoint edits copy their budget without changing aggregate or other
endpoint budgets. Use the original manifest; older binaries reject version 6.
Successful deadline replays retain their descriptor for subsequent replay.

`within` governs when a sustained healthy interval must begin according to the
configured observation windows; sustain time still has to complete afterward.
The result is a tested budget boundary within the requested range, not an exact
recovery duration or a guarantee for future runs. It does not prove monotonic
behavior or that every longer deadline will pass. Application sources and external
state have the existing replay limitations.

### Checking that recovery stays stable

Add optional `stability` to an aggregate `recovery` budget or a named
`step_recovery` budget to observe the application after its first sustained
recovery. For example, a clear phase immediately following a fault phase can use:

```yaml
- name: recovery
  duration: 25s
  recovery:
    within: 10s
    sustain: 2s
    stability: 10s
    window: 1s
    thresholds:
      p95: 200ms
      error_rate: 0.01
      min_samples: 10
```

Gregale first finds a healthy rolling window starting by `within` and confirms
that it stays healthy for `sustain`. It then checks the same absolute thresholds,
relative baseline limits, and minimum completed sample count for `stability`.
The first unhealthy window fails the assertion, even if traffic later becomes
healthy again. Named endpoint budgets observe only that endpoint's traffic;
healthy traffic to another endpoint cannot mask a relapse.

`stability` must be a whole-millisecond duration from 100ms to 5m.
`within + sustain + stability` must fit inside the phase duration. Deadline
minimization and replay overrides also enforce this bound. Omitting `stability`
preserves the previous behavior and replay fingerprint. Changing it changes the
configuration fingerprint and requires a matching manifest for replay.

JSON and JUnit evidence include `recovery.stability` (or the corresponding
endpoint recovery): requested `duration_ms`, observation `started_ms`,
`observed_ms`, `windows_checked`, `passed`, and optional `relapse_ms`.
Times are relative to the acknowledged start of the clear phase. Terminal and
HTML reports show the observation and relapse; failure diagnostics collect their
bounded log window around the detected relapse. An incomplete observation fails,
and initial recovery can remain recorded while the overall assertion fails.
A stability relapse is distinct from a missed recovery deadline and is not a
valid starting failure for deadline or fault minimization.

Checks use the existing rolling window cadence (window / 10, at least 100ms),
including the exact observation endpoint. This detects unhealthy sampled windows;
it does not guarantee that every brief event between checks will be observed.
Existing phase load and request budgets still apply; increase the clear phase
and overall load duration if the observation needs more time.

### Minimizing the observation needed to reproduce a relapse

Use `--mode stability` with a saved real-VM stability failure to find a shorter
observation period that still detects the selected relapse:

```sh
gregale test minimize --from failed.json --run-id RUN_ID \
  --manifest gregale-test.yaml --assertion load/recovery \
  --mode stability --stability-resolution 25ms \
  --max-runs 20 --max-workload-minutes 180 \
  --report stability-minimized.json --html stability-minimized.html
```

Use `STEP/PHASE/REQUEST` to select a named endpoint budget. The selected budget
must declare `stability`; the input must show completed initial recovery followed
by an unhealthy stability window. A missed recovery deadline, unavailable
baseline, incomplete observation, or unrelated failure cannot start this search.
`--validate` checks the manifest, saved evidence, sources and budgets without
provisioning. `--stability-resolution` defaults to 25ms and accepts whole
milliseconds from 1ms to 5m. Rule-index and other mode-specific flags are rejected.

Gregale confirms the original relapse in two fresh isolated runs, then tests the
100ms minimum observation in two runs. If the minimum passes twice, it bisects
between that passing interval and the confirmed failing interval until the gap
is within the requested resolution. Every candidate needs two matching outcomes.
A confirmed relapse replaces the best failing result; a confirmed healthy
observation updates the tested passing bound. If the original is already 100ms,
its confirmation completes the search without another candidate.

Fault rules, traffic, seed, deadlines, sustain times, windows, thresholds,
relative baselines and other recovery assertions remain fixed. The phase and
load durations also remain fixed: this minimizes the assertion's observation
period, not VM runtime or billing. Prior rule removals, severity edits and
recovery overrides are preserved. All trials use the existing fresh provisioning,
cleanup and diagnostics workflow and complete-pair run/workload reservations.

Mixed outcomes, interruption, cleanup failure, a different failure, unmatched
faults, inconsistent evidence or exhausted budgets make the search inconclusive.
Cleanup failure stops further runs. The report retains the most recent confirmed
relapse, including the original if no shorter interval was confirmed.
`minimization.stability` records `before_ms`, `best_ms`, `resolution_ms`, and the
optional tested `passed_below_ms`; `best_run_id` points to a confirmed failing
leaf. A completed search has a passing command result even though its reproducer
fails; an inconclusive search has a failing result.

Replay version 7 adds `stability` to target-based `recovery_overrides`; an entry
can preserve a deadline `within` and edit `stability` together. At most 20 distinct
recovery targets can be overridden. The original and reduced configuration
fingerprints are checked. Versions 1–6 remain supported. Failed trials and
passing recovery configuration trials are replayable from the saved report with
the original manifest; the best run identifies the confirmed relapse result.

The result is the shortest confirmed interval among the tested candidates within
this bounded search. It is not a guarantee of a global minimum, repeatability on
every future run, or monotonic behavior. The existing rolling-window cadence
still determines when a relapse is detected.

### Sweeping dependency degradation

Declare `resilience.fault_sweep` to repeat a scenario at a bounded list of settings
for one existing fault rule or the duration of a whole fault phase. For example, add this to a staged scenario with a
fault phase named `fault` in step `load`, immediately followed by aggregate or
endpoint recovery assertions:

```yaml
resilience:
  runs: 2
  min_pass_rate: 1
  fault_sweep:
    step: load
    phase: fault
    rule_index: 0
    mode: latency
    values: ["10ms", "100ms", "500ms", "1s"]
    max_runs: 8
    max_workload_minutes: 180
```

Run and save the results with the existing test command:

```sh
gregale test --manifest gregale-test.yaml --scenario checkout --profile cold \
  --max-workload-minutes 180 --report sweep.json --html sweep.html
```

`--validate` checks the manifest and sources without provisioning. `--preflight`
shows the full deployment and workload-minute estimates. `rule_index` is the
explicit zero-based index in the selected phase's original rule list for severity
modes. Omit it for `duration`.

Supported modes:

| Mode | Selected rule | Values |
| --- | --- | --- |
| `latency` | HTTP `latency` or `tcp_latency` | Whole-millisecond durations from 1ms to 30s |
| `bandwidth` | `tcp_bandwidth` | Integer allowed rates from 1 to 1000000 KiB/s, such as `["100", "10", "1"]` |
| `percent` | Any supported fault rule | Integer selection percentages from 1 to 100, such as `["10", "50", "100"]` |
| `duration` | Whole selected fault phase; omit `rule_index` | Whole-millisecond durations from 1s to 5m |

Provide 2–10 distinct values. Equivalent values such as `1s` and `1000ms` count
as duplicates and are rejected. Settings execute in the declared order; latency
and percentage can increase or decrease, and lower bandwidth rates impose
stronger restrictions. For severity modes, the selected rule's other fields, including seed and
match budgets, stay fixed. Other fault rules, traffic, phase durations, recovery
deadlines, sustain and stability intervals, endpoint budgets and relative
baselines stay fixed as well. Sweeps cannot be combined with `fault_matrix`.

To test how long the application tolerates an outage, keep the rules fixed and
sweep the fault phase duration:

```yaml
resilience:
  runs: 2
  min_pass_rate: 1
  fault_sweep:
    step: load
    phase: fault
    mode: duration
    values: ["1s", "5s", "15s", "30s"]
    max_runs: 8
    max_workload_minutes: 180
```

Duration mode changes all rules' exposure time together. It requires a load with
an explicit `duration` and no `stages`. The total load duration increases or
decreases by the change in fault duration; healthy and recovery phase durations,
traffic rate/concurrency, rule severity, seeds, recovery deadlines and budgets
stay fixed. Longer cases therefore generate more traffic. Every resulting load
must fit the existing five-minute load limit, request limits and scenario timeout.
Validation rejects the whole sweep if any case exceeds these limits. Duration
cases support the same recovery/stability evidence, saved-baseline comparisons
and version 8 replay as severity cases. Their JSON sweep metadata identifies the
whole phase using `rule.index: -1`.

Each setting uses `resilience.runs` fresh isolated runs and the same
`min_pass_rate`. The required passing count is rounded up. Every setting must
meet that count for the overall sweep to pass. Ordinary assertion failures do
not stop later settings; cleanup failure or cancellation stops further runs and
records remaining work as skipped. Reports retain the individual failures and
recovery/stability evidence even when the configured pass rate tolerates them.

Both sweep limits are required. `max_runs` must be 2–20 and cover
`number of values × resilience.runs`. `max_workload_minutes` must be positive
and cover the existing conservative estimate based on scenario timeout and all
workloads for the entire sweep. These manifest limits apply to one full sweep
for one lifecycle profile and outer attempt. Additional profiles, `--repeat`, or
suite entries each run another sweep. Use the command's
`--max-workload-minutes` guard to cap the combined command estimate.

Sweeps use the existing matrix report structure. Each generated case includes
`sweep` metadata with the selected rule, mode and normalized value, its pass rate,
recovery summaries and full repeated-run history. The root `fault_matrix.sweep`
records the declared limits and values. Terminal output lists settings and their
results; HTML includes a settings table; JSON and JUnit preserve detailed evidence.
The sweep reports each tested point and does not infer a monotonic or exact
failure boundary between them.

Replay version 8 records the sweep setting and generated case identity. Both
passing and failing isolated sweep runs can be selected by run ID from the saved
report and replayed once against the original manifest. Changing that setting,
its case identity, or the effective scenario configuration rejects replay.
Versions 1–7 remain supported. Existing minimization modes preserve the sweep
identity and can minimize a compatible sweep failure. Replay does not rerun the
whole sweep, and the existing source/environment replay limitations still apply.

### Comparing a sweep with a saved baseline

Add `resilience.sweep_regression` alongside `fault_sweep`:

```yaml
resilience:
  runs: 4
  min_pass_rate: 0.75
  fault_sweep:
    step: load
    phase: fault
    rule_index: 0
    mode: latency
    values: ["10ms", "100ms", "500ms"]
    max_runs: 12
    max_workload_minutes: 180
  sweep_regression:
    max_pass_rate_drop: 0.25
    recovery:
      p95_increase_percent: 20
      min_samples: 2
```

First run the sweep without a baseline flag and save its JSON report. Then
compare a later run with the same workload and settings:

```sh
gregale test --scenario checkout --profile cold --report before.json

gregale test --scenario checkout --profile cold --sweep-baseline before.json \
  --report after.json --html after.html --junit after.xml
```

`max_pass_rate_drop` is a fraction from 0 to 1; `0.25` permits a drop of 25
percentage points. Omit it to permit no drop. Each setting must also retain its
previous passing status under the unchanged `min_pass_rate`: an allowed drop
does not permit a formerly passing setting to become failing. Improvements pass
these comparisons. The overall command still fails when current sweep
assertions fail, even if the comparison finds no regression against a baseline
with failing settings.

The optional `recovery` block compares p95 recovery times for every aggregate
and named endpoint recovery assertion at each setting. The increase limit is
0–1000 percent and `min_samples` must be 2 through `resilience.runs`. Samples are
recomputed from individually confirmed passing recovery evidence, including
stability validation when configured. A shortage in the baseline rejects the
comparison before provisioning; a shortage in the current report fails the
comparison. Omitting `recovery` compares only setting outcomes and pass rates.

`--sweep-baseline` requires real-VM execution and a configured sweep regression
policy. It cannot be combined with `--baseline`, `--recovery-baseline`,
`--validate`, or `--preflight`. Normal validation still checks the policy without
reading a baseline. Suites support the same flag; every selected plan must be a
configured sweep. Repeated outer attempts and lifecycle profiles each require a
matching baseline row. The existing sweep and command workload budgets remain
in force; comparison itself provisions no additional runs.

A completed baseline may contain failing settings. It must have matching
scenario, engine, profile, outer attempt, setting identities, repeated-run count,
pass policy, request templates, fault rules, schedules, recovery budgets, TCP
ports and timeout. Application source changes are permitted. Settings cannot be
added, removed or reordered while comparing this baseline; record a matching
baseline when the workload changes.

The reader uses the existing bounded JSON baseline format (64 MiB maximum) and
rejects duplicate root identities, missing settings, incompatible workload
fingerprints, incomplete histories, duplicate attempts or run IDs, skipped runs,
cleanup failures, and summaries inconsistent with leaf outcomes. A baseline
containing an unsuccessful prior regression comparison is also rejected. Report
outputs cannot overwrite the baseline file.

`sweep_regression` evidence includes the baseline report digest, per-setting
before/after passing counts, drop and allowance, lost passing status, and optional
recovery comparisons with confirmed sample counts. Terminal, HTML, JSON and
JUnit reports expose the results. Failed comparisons produce a failing command
result while retaining current assertion errors and run/replay evidence. Plans
skipped by suite fail-fast carry `not_compared` evidence. These checks compare
observed repeated runs; they do not establish statistical significance or infer
a tolerance boundary between tested values.

### Minimizing the outage duration that reproduces a recovery failure

After a scenario or duration sweep fails its recovery deadline, use the isolated
run ID to search for a shorter outage that still reproduces that assertion:

```sh
gregale test minimize --from sweep.json --run-id RUN_ID \
  --manifest gregale-test.yaml --assertion load/recovery \
  --mode duration --duration-resolution 100ms \
  --max-runs 20 --max-workload-minutes 180 \
  --report duration-minimized.json --html duration-minimized.html
```

Use `STEP/RECOVERY_PHASE/REQUEST` for an endpoint recovery assertion. The selected
fault phase is the one immediately before that recovery phase. This mode targets
all its fault rules together and does not accept `--rule-index`. Add `--validate`
and omit report flags to check the selection, sources and budgets without
provisioning.

The load must have an explicit fixed `duration` and no `stages`. The search keeps
rule severity, selection percentages, seeds, other phases, traffic rate and
concurrency, recovery deadlines, sustain/stability intervals and endpoint budgets
fixed. It shortens the total load duration by the same amount as the selected
fault phase, so shorter outages generate less traffic. Candidate loads pass the
normal scenario, scheduling and request-limit validation.

The original failure must reproduce in two fresh isolated runs. The search then
tests the 1s floor and bisects between confirmed passing and failing durations
until their gap fits `--duration-resolution` (default 100ms; whole milliseconds
from 1ms to 5m). Each candidate needs two agreeing runs with completed, matched
fault and recovery evidence. This reports the shortest **confirmed tested**
outage, not a proven global minimum; application behavior can be nonmonotonic.

`--max-runs` includes the original confirmations and all candidate runs. A run
pair starts only if both runs fit the run and conservative workload-minute
budgets. Cleanup failure, cancellation, unrelated/incomplete evidence or disagreeing
runs stops the search. An inconclusive search preserves the best confirmed
failing run and replay configuration found so far.

Reports include `minimization.duration` with the step, fault phase, starting and
best duration, resolution and tested passing bound, plus every confirmation run.
Replay version 9 saves `duration_overrides` and fingerprints the adjusted phase
and load durations. Both passing and failing duration trials can be replayed
once by run ID using the original manifest. Earlier rule removals, severity and
recovery edits, and sweep case identity are preserved. Versions 1–8 remain
supported; existing source/environment replay limitations still apply.

### Repeating dependency outages in one run

Add `load.outage_cycles` to a staged load with a three-phase healthy → fault →
recovery schedule. The first healthy phase runs once; the fault and recovery
phases repeat within the same application deployment:

```yaml
# Inside a staged load step with a declared request named checkout:
load:
  vus: 4
  duration: 16s             # duration of the single-cycle template
  pacing: 100ms
  thresholds:
    error_rate: 1           # allow expected errors during injected faults
  outage_cycles:
    count: 5
    max_p95_increase_percent: 20       # optional, compared with cycle 1
    max_error_rate_increase: 0.02      # optional, absolute fraction (2 points)
    max_recovery_increase_percent: 25  # optional, each assertion vs cycle 1
  chaos_schedule:
    - name: healthy
      duration: 1s
    - name: fault
      duration: 5s
      rules:
        - from: app
          to: database
          kind: tcp_timeout
          port: 5432
          percent: 100
          min_matches: 1
    - name: recovery
      duration: 10s
      recovery:
        within: 5s
        sustain: 1s
        window: 1s
        thresholds:
          error_rate: 0
          min_samples: 5
```

This expands to 76 seconds of traffic: 1s healthy, followed by five pairs of
5s fault and 10s recovery. Set the scenario `timeout` to cover the expanded load
and all other staged load windows; for example, `2m` when the other steps fit.
`gregale test --validate` and `--preflight` check the expanded schedule before
provisioning. Existing run and workload-minute guards still apply.

`count` must be 2–9. The template needs an explicit fixed load `duration`, exactly
three phases, and aggregate or endpoint recovery assertions in the final phase.
Load `stages` are unsupported. Expanded schedules have at most 19 phases and must
fit the five-minute load limit, HTTP request limits and scenario timeout. A
malformed single-cycle template is rejected before expansion.

Every cycle reinstalls the same fault rules and retains phase SLOs, recovery
budgets, stability observations, endpoint assertions, retry amplification,
connection-attempt and circuit-breaker budgets. `min_matches` applies separately
to each fault installation. Seed values stay fixed. Traffic rate/concurrency
stays fixed, so more cycles produce more traffic. Each cycle's recovery time and
stability evidence remain in its recovery phase. Relative recovery uses the
immediately preceding fault-free phase as usual; after cycle 1, that phase is the
previous cycle's recovery phase.

Optional trend limits compare each later fault phase with cycle 1's fault phase,
and each later recovery phase with cycle 1's recovery phase. P95 increase is a
percentage from 0–1000; error-rate increase is an absolute fraction from 0–1.
Equality passes. Comparisons require request samples in both phases and latency
samples for p95. Missing samples fail the comparison. These whole-phase metrics
include the recovery transient and are separate from recovery's healthy-window
assertions. Omit the limits to collect metrics without imposing trend checks.

Ordinary recovery or SLO failures do not stop later cycles. Schedule failure or
cancellation stops execution, clears the active fault through the existing cleanup
path and reports unfinished cycles as incomplete. The load fails if any cycle
budget, trend check or schedule fails; existing whole-load and baseline assertions
also remain independent requirements.

Terminal and HTML reports show a cycle summary and the first failed or incomplete
cycle. JSON and JUnit retain `load.outage_cycles`, all phase metrics, recovery
results and optional trend checks. The first cycle keeps the template phase names;
later phases use names such as `fault-cycle-2` and `recovery-cycle-2`, with a
one-based `cycle` field in phase evidence.

Replay pins the expanded schedule, count, durations, rules and trend budgets and
reruns the complete isolated scenario. Use the failed run ID with the original
manifest; editing cycle settings rejects replay. Existing minimization can target
an assertion such as `load/recovery-cycle-2` while retaining the preceding cycles.
A fault matrix or sweep targeting `fault` edits that named phase in cycle 1;
other cycles retain their rules. Use template changes to vary all cycles together.

### Checking recovery degradation across outage cycles

Set `load.outage_cycles.max_recovery_increase_percent` to require that each
recovery assertion stays within a tolerance of its own cycle 1 recovery time:

```yaml
outage_cycles:
  count: 5
  max_recovery_increase_percent: 25
```

For a cycle 1 recovery of 200ms, a later recovery must complete in at most 250ms.
Equality passes. The tolerance is a finite percentage from 0–1000; `0` allows no
increase. Omit the setting to retain the existing per-cycle assertions without
recovery-time comparisons.

Aggregate recovery and every declared endpoint recovery are compared separately.
An endpoint uses that same endpoint in cycle 1, never the aggregate or another
endpoint. All later cycles keep cycle 1 as the reference; the baseline does not
move after a slower cycle. Comparisons use each isolated run's own measured
recovery times, rather than percentiles across repeated runs.

Cycle 1 must have confirmed recovery evidence. Missing, failed, nonfinite,
nonpositive or out-of-budget recovery times fail the comparison. Evidence must
match the declared deadline, sustain and window, and any configured stability
observation must have completed successfully. Incomplete or cancelled cycles
cannot pass. Existing recovery deadlines and other phase budgets still apply
independently; a tolerance does not relax those assertions.

Each cycle records `recovery_checks` with the endpoint name (empty for aggregate),
cycle 1 and current recovery times, allowed limit, tolerance and result. Unconfirmed
times are omitted and accompanied by an error. Terminal and HTML reports show
these values; JSON and JUnit retain the detailed evidence. The cycle summary's
`first_recovery_failed_cycle` identifies the first failed recovery comparison,
including cycle 1 when its baseline is unconfirmed. This is separate from
`first_failed_cycle`, which may identify a different SLO or fault-budget failure.

Replay preserves the tolerance and complete cycle schedule. Changing the
comparison setting rejects replay through the existing configuration fingerprint.

### Seeded fault campaigns

Use `load.fault_campaign` on a staged load step to sample a reproducible sequence
of TCP outages for one dependency. The healthy/fault/recovery template runs its
healthy baseline once, then repeats a sampled fault followed by the template's
recovery phase. Existing aggregate and endpoint SLOs, recovery, stability, and
fault-match budgets remain active for every phase.

```yaml
load:
  vus: 5
  duration: 3s                 # duration of the single-cycle template
  thresholds:
    error_rate: 1              # allow expected failures during outages
  fault_campaign:
    seed: 42                   # required; explicit zero is also valid
    count: 5                   # 2..9 outages in one isolated run
    max_duration: 30s          # required cap, including baseline and recoveries
    choices:                   # 2..16 bounded alternatives
      - duration: 1s
        rule: {to: cache, port: 6379, kind: tcp_latency, percent: 100, latency: 200ms, min_matches: 1}
      - duration: 2s
        rule: {to: cache, port: 6379, kind: tcp_timeout, percent: 100, min_matches: 1}
      - duration: 1s
        rule: {to: cache, port: 6379, kind: tcp_reset, percent: 100, min_matches: 1}
  chaos_schedule:
    - name: healthy
      duration: 1s
    - name: fault
      duration: 1s
      rules:
        - {to: cache, port: 6379, kind: tcp_latency, percent: 100, latency: 200ms, min_matches: 1}
    - name: recovery
      duration: 1s
      recovery:
        within: 500ms
        sustain: 200ms
        window: 200ms
        thresholds: {error_rate: 0, min_samples: 1}
```

All alternatives must use the template's same `from`, `to`, and TCP port. Supported
kinds are `tcp_latency`, `tcp_bandwidth`, `tcp_timeout`, `tcp_reset`,
`tcp_connect_timeout`, and `tcp_connect_refused`. Each choice must pass the usual
fault validation, even if it is not selected. Fault durations and `max_duration`
use whole milliseconds between 1s and 5m. A generated schedule exceeding the cap
is rejected before execution; it is never truncated. Existing scenario timeout,
request, and workload limits still apply. Campaigns require a fixed-duration
three-phase template with one fault rule and recovery assertions, and cannot be
combined with `outage_cycles` on that load step.

`splitmix64-v1` draws a choice with replacement for each outage, followed by a
separate rule seed for its percentage-based fault decisions. Choices can repeat;
a campaign does not guarantee coverage of every alternative. The generator
replaces any seed on the selected choice rule. The same campaign input recreates
the same ordered faults, durations, and rule seeds. This reproduces the fault
schedule; application timing and responses can still vary across runs.

The outage-cycle summary reports completion and the first failed or incomplete
cycle. Its `campaign` evidence includes the seed, algorithm, and complete planned
schedule, including each rule's severity and seed. JSON and JUnit retain this
schedule; terminal and HTML reports identify the campaign, and HTML lists its
phases. Replay regenerates the schedule from the source manifest and pins both
campaign inputs and the expanded schedule through the configuration fingerprint.
Changing the seed, choices, count, cap, or recovery assertions rejects replay.

To minimize the history leading to a failed later recovery, use the existing
`gregale test minimize --mode rules --assertion load/recovery-cycle-3` flow. For
campaigns it tries removing earlier fault events, retaining the fault immediately
before the selected recovery and all phase timing and assertions. Removed events
become fault-free windows, so replay preserves the original timeline without
redrawing the campaign. Candidates must satisfy validation and reproduce the
selected failure twice; cleanup failures, cancellation, or missing evidence stay
inconclusive. A completed search establishes constrained single-removal
minimality, rather than a globally shortest sequence. Existing duration and
severity minimization can reduce the retained triggering fault, using the same
run and workload-minute limits.

### Campaign coverage across multiple seeds

Add `resilience.campaign_coverage` to run an explicit seed list against an existing
staged `load.fault_campaign`. Each seed runs in its own isolated deployment. The
campaign's fault choices, recovery assertions, and workload stay fixed; the seed
changes the ordered fault choices, durations, and rule seeds.

```yaml
# Scenario-level settings, alongside the existing staged steps and services:
timeout: 1m
resilience:
  runs: 1                     # isolated runs per seed; 1..20 for coverage
  min_pass_rate: 1             # every run must pass for each seed
  campaign_coverage:
    step: load                # staged load step declaring fault_campaign
    seeds: [42, 43, 44]        # 2..10 distinct uint64 seeds; zero is valid
    max_runs: 3               # 2..20, covering seeds × runs × profiles × repeat
    max_workload_minutes: 12  # e.g. app + 3 services, 3 runs, 1-minute timeout
```

Run this example with `--profile cold --repeat 1`; its three-run cap would reject
`--profile all`. Both caps cover the selected scenario's profiles and outer
attempts in the command, including when it is a suite member. Workload-minute
estimates use the scenario timeout and app plus service count, rather than only
load duration. A command-level `--max-workload-minutes` can impose an additional
limit, including a combined suite budget. Validation checks every selected seed's
expanded schedule and campaign duration cap before any deployment. Coverage
cannot be combined with `fault_matrix` or `fault_sweep` on the same scenario.
The load's original `fault_campaign.seed` remains required as its template seed;
coverage executes the seeds in the explicit list, in declaration order.

Ordinary assertion failures do not stop later seeds. Cancellation and cleanup
failures stop further execution and mark remaining runs or seeds skipped. Each
seed must independently meet `resilience.min_pass_rate`; successful seeds do not
compensate for a failing seed. With `runs: 2` and `min_pass_rate: 0.5`, a seed can
pass despite one failed run, and that run's failure is still shown in the summary.
Use `min_pass_rate: 1` to require all runs to pass.

The fault-matrix report's `campaign_coverage` summary shows per-choice and
per-seed results in JSON, JUnit, terminal, and HTML output. Choices use zero-based
indices from the source list, so even duplicate alternatives remain distinct.
Coverage counts fault events across the planned isolated runs:

- `planned`: events selected by the deterministic schedules, including skipped
  runs and phases that were never reached.
- `installed`: observed fault windows with start/end timestamps, the expected
  cycle identity, and one installed rule matching the exact generated rule.
- `exercised`: installed events with a positive match count for that same rule.

An installed fault with no traffic matches does not count as exercised. Missing
or mismatched evidence and skipped runs contribute no observed coverage. A failed
or subsequently cancelled run can retain coverage from fault windows already
observed and closed. These counts describe which choices ran against traffic;
they are not a coverage percentage of all possible fault orders and do not imply
that every choice must be exercised for the scenario to pass unless coverage
requirements are declared.

Each seed row includes pass/fail/skip counts and isolated run IDs. Failed runs
show the first failed or incomplete outage cycle and recorded aggregate or
endpoint recovery failures. Full cycle evidence and generated schedules remain
in the nested run receipts.

Replay a failed leaf run with the existing `gregale test replay --from REPORT.json
--run-id RUN_ID --manifest MANIFEST.yaml` command. Replay selects the named
`campaign-seed-N` case, reproduces its exact schedule, and runs that seed once.
Removing that seed or changing the selected campaign configuration rejects
replay. Other seeds' membership, order, and run policy do not affect the isolated
replay fingerprint. Existing minimization modes work on that leaf run; sequence
minimization retains the seed case identity and does not redraw removed faults.


### Requiring fault-choice coverage

Set `resilience.campaign_coverage.min_exercised` to require a minimum number of
exercised fault events for specific choices. Keys are zero-based indices in the
load's `fault_campaign.choices` list; values are positive integer event counts.

```yaml
resilience:
  runs: 1
  min_pass_rate: 1
  campaign_coverage:
    step: load
    seeds: [42, 43, 44]
    max_runs: 3
    max_workload_minutes: 12
    min_exercised:
      0: 2   # choice 0 must match traffic in at least two fault windows
      1: 2   # choice 1 must also match traffic in at least two fault windows
```

A coverage requirement applies to the complete seed batch for one lifecycle
profile and one outer attempt. It combines that batch's seeds and repeated runs;
another profile or outer attempt cannot compensate for missing coverage. A choice
counts once per isolated run's fault window when the expected rule was installed
and its match evidence confirms traffic. Multiple traffic matches in one window
still count as one event. Missing, mismatched, duplicated, or skipped event
records cannot satisfy a target. Counts can include closed fault windows from a
run whose later SLO or cleanup failed; those failures still fail the scenario
independently.

Equality passes. Choices without an entry remain optional. An installed rule with
zero matches, or a required choice never selected by the seed schedules, fails
its requirement. The campaign fails when any declared target is missed, even if
every seed passes its SLO policy or `min_pass_rate` tolerates individual failures.
The command returns a failing exit status for CI. Requirements do not add runs
or redraw schedules, and existing run and workload caps remain in force.

Validation rejects unknown or negative choice indices, nonpositive minima, and
minima greater than the batch's total fault slots (`count × seeds × runs`). A
minimum can exceed the number of times a particular choice was actually selected;
that produces an explicit coverage failure after execution so the report shows
which targets were missed. Omit `min_exercised`, or use an empty mapping, to retain
count-only coverage and existing replay fingerprints.

Each configured choice records `min_exercised` and `requirement_status` beside
its planned, installed, and exercised counts. The batch adds
`requirements_status` and `requirements_error`. Terminal, HTML, JSON, and JUnit
reports identify every missed choice and its actual and required event counts.
Seed pass/fail counts continue to describe SLO results, so a batch can show all
seeds passed while its coverage requirement and overall status are failed.
Existing SLO, cancellation, and cleanup errors remain in the report.

A failed batch with requirements gets its own campaign run ID and replay manifest
version 10 (`campaign_coverage: true`). Select that campaign ID with the existing
`gregale test replay` command to rerun the whole seed batch once for its original
profile. The replay fingerprints the complete campaign configuration, including
seed order, repeated runs, SLO policy, coverage targets, and resource limits.
Changing or removing a target, altering seeds or run policy, or supplying partial
fault edits rejects this replay. The batch replay descriptor is kept separate
from each isolated run's descriptor.

Failed leaf run IDs remain available for isolated SLO replay and minimization.
Their fingerprints and generated campaign evidence retain the inherited coverage
requirements, so changing the targets also rejects leaf replay. An isolated leaf
replay runs that seed once and does not reevaluate the aggregate coverage gate;
use the campaign ID to reproduce a coverage failure. Existing replay versions
1–9 remain supported for isolated configurations.

### Automatic seed selection for campaign coverage

Replace an explicit `seeds` list with a bounded `seed_search` to select reproducible
campaigns that meet planned choice targets:

```yaml
resilience:
  runs: 1
  min_pass_rate: 1
  campaign_coverage:
    step: load
    seed_search:
      start_seed: 42
      max_candidates: 100
      max_seeds: 4
    min_exercised:
      0: 2
      1: 2
    max_runs: 4
    max_workload_minutes: 16
```

Keep the staged `load.fault_campaign` template and its explicit template seed as
in the examples above. `seed_search` and explicit `seeds` are mutually exclusive;
search requires nonempty `min_exercised`. Candidates are consecutive unsigned
64-bit seeds beginning at `start_seed`. The range must not overflow.
`max_candidates` is 2..10000; `max_seeds` is 2..10. `max_runs` must cover
`max_seeds * runs`, and the existing workload cap applies to the selected batch
across profiles and attempts.

Selection uses the existing SplitMix64 campaign generator. It keeps the first two
valid candidates, then candidates that add coverage for a still unmet target.
Invalid schedules are skipped. Repeated runs contribute to planned event counts.
The search stops when all targets have enough planned events and at least two
seeds are selected. If the candidate or seed limit is reached first, validation
fails before provisioning. This bounded greedy search does not guarantee the
smallest seed list or that another combination within the range would fail.

Run `gregale test --validate --scenario NAME` to preview the selected seeds,
number of candidates examined, and planned event counts by zero-based choice
index. JSON validation includes `campaign_seed_selection`. Execution reports
include the same preview under `fault_matrix.campaign_coverage.seed_selection`,
with terminal and HTML summaries. Planned coverage does not prove traffic matched
any fault: the existing installed/exercised evidence and `min_exercised` gate
still determine the outcome.

Aggregate version-10 replay pins the search configuration, selected seeds and
planned counts along with the rest of the campaign policy. Replay deterministically
recreates the same selection and rejects changed search settings. Isolated seed
replay continues to pin that seed's exact schedule and coverage requirements.

### Selecting additional seeds from a previous campaign report

Add `from_report` and an explicit root `run_id` to `seed_search` to target gaps
observed in one prior campaign batch:

```yaml
resilience:
  runs: 1
  min_pass_rate: 1
  campaign_coverage:
    step: load
    seed_search:
      start_seed: 42
      max_candidates: 100
      max_seeds: 4
      from_report: reports/previous-campaign.json
      run_id: "ROOT_RUN_ID_FROM_PREVIOUS_REPORT"
    min_exercised:
      0: 2
      1: 2
    max_runs: 4
    max_workload_minutes: 16
```

The report path is relative to the manifest directory. Use a JSON report from
`--report` and select a campaign root run ID, rather than an isolated seed run.
Run `gregale test --scenario NAME --validate` to preview prior observed event
counts, remaining targets, excluded prior seeds, and selected additional seeds.
When executing, choose the same profile as the prior run, for example
`--profile cold`. A guided batch applies to that one profile; `--profile all`
is rejected. Suite members must also use the matching profile.

Gregale recomputes observed coverage from the root's complete, uniquely identified
leaf history. The prior load steps, TCP ports and timeout must match the current
seed-specific workload fingerprint; any present leaf replay descriptor must also
match the current configuration. Choices, durations, rules, seed identity and
summary counts are checked. Older passing leaves without replay descriptors can
be used when their group workload fingerprint and seed metadata match. New
campaign reports preserve replay descriptors for passing seed leaves too.
Reports with skipped or missing leaves, duplicate identities, cleanup failures,
changed workloads or inconsistent summaries are rejected before provisioning.
An ordinary SLO failure or a coverage-gate failure may still supply useful
observed coverage if all leaves completed and cleanup succeeded.

For each requested choice, the new batch must exercise
`max(0, requested minimum - prior observed events)` fault windows. Previously
satisfied choices are optional in the new batch. Original targets and prior
counts remain visible in guidance metadata; the new batch's installed/exercised
counts describe only its own runs. This uses one selected prior batch and does
not recursively add observations from earlier reports referenced by that batch.
If every target is already satisfied, validation reports that no additional
coverage is needed and does not provision workloads.

Seeds from the selected prior batch are excluded. Excluded seeds still consume
candidate attempts, so the search remains bounded. Existing candidate, seed,
run and workload limits apply to the additional batch; insufficient search space
or capacity fails validation. Planned coverage still does not guarantee that
traffic reaches the selected dependency faults.

Terminal, JSON, HTML and JUnit evidence retain the guidance run ID, report SHA256,
prior observed counts, original targets and remaining targets. Aggregate replay
pins this guidance and the deterministic selection, and requires the original
report with identical bytes. Changing or removing the report invalidates replay.
New report outputs cannot overwrite the guidance file, including through a
hard-link alias.

### Cumulative coverage across campaign batches

Use `seed_search.reports` to select 2..10 compatible prior campaign roots and
close remaining coverage gaps across their combined observed events:

```yaml
resilience:
  runs: 1
  min_pass_rate: 1
  campaign_coverage:
    step: load
    seed_search:
      start_seed: 42
      max_candidates: 200
      max_seeds: 4
      reports:
        - from_report: reports/batch-1.json
          run_id: "FIRST_ROOT_RUN_ID"
        - from_report: reports/batch-2.json
          run_id: "SECOND_ROOT_RUN_ID"
    min_exercised:
      0: 6
      1: 4
    max_runs: 4
    max_workload_minutes: 16
```

Each report path is relative to the manifest directory. The existing single-batch
`from_report` / `run_id` form remains supported; do not combine it with `reports`.
An empty or one-element `reports` list is rejected. You may select distinct root
run IDs from the same JSON file.

Every selected batch must have the same scenario and profile, compatible
seed-specific workloads, complete leaf history and clean cleanup, using the
same evidence checks as single-report guidance. Root and leaf run IDs must be
unique across all selected batches. Repeating a root, including a copied report
under another filename, or overlapping leaf identities fails validation before
provisioning. Separate real executions of the same seed with distinct run IDs
are independent observations and can contribute coverage.

Coverage is recomputed from each selected batch's own leaves and summed by choice.
Nested guidance metadata never contributes additional events. To include earlier
batches, list their roots explicitly. The next batch must exercise
`max(0, requested minimum - cumulative observed events)` windows for each choice.
Its installed and exercised counts still describe only that new batch; prior
observations do not replace its traffic-match gate. If all targets are already
met, validation stops without provisioning.

Previously tried seeds are excluded using a sorted, unique union from every
selected batch. Excluded candidates still consume search attempts. The existing
candidate, seed, run and workload limits bound the follow-up batch. Execute with
the shared profile, such as `--profile cold`; mixed profiles and `--profile all`
are rejected.

`gregale test --scenario NAME --validate` previews cumulative coverage, remaining
targets and each contributing batch's run ID, observed counts, seeds and report
SHA256. JSON validation and execution evidence include these records in
`seed_selection.guidance.contributions`. Terminal, HTML and JUnit reports retain
the same provenance, showing which batch exercised each fault choice.

Aggregate version-10 replay pins the ordered input list, every report fingerprint,
combined observations, remaining targets and selected seeds. Keep every original
report with identical bytes; changing, removing or reordering an input invalidates
aggregate replay. Output reports cannot overwrite any contributing input,
including hard-link aliases.

### Campaign coverage regression checks for CI

Add `campaign_coverage.regression` to compare the current batch with a completed
baseline campaign and detect choices that silently stop matching traffic:

```yaml
resilience:
  runs: 1
  min_pass_rate: 1
  campaign_coverage:
    step: load
    seeds: [42, 43, 44]
    max_runs: 3
    max_workload_minutes: 12
    regression:
      from_report: reports/coverage-baseline.json
      run_id: "BASELINE_ROOT_RUN_ID"
      min_exercised_rate: 0.8
```

Use a JSON `--report` file and an explicit campaign root run ID. The path is
relative to the manifest directory. `gregale test --scenario NAME --validate`
checks the baseline locally and previews its run ID, profile, fingerprint and
minimum rate. Execute with the same profile, such as `--profile cold`.

The baseline must have identical ordered seeds, repetitions per seed, choice
definitions and compatible seed-specific workloads. Gregale applies the existing
guidance evidence checks: complete uniquely identified leaf history, matching
workload fingerprints and any leaf replay descriptors, clean cleanup, and summary
counts that agree with recomputed installed/exercised evidence. Skipped,
interrupted, duplicate, incompatible or corrupt histories fail validation before
provisioning. A baseline whose SLOs failed can still be used when its history
completed and cleanup succeeded. Application code can change between runs;
configuration compatibility remains required.

Each choice's exercised-event rate is `exercised fault windows / planned fault
windows`, across the batch's seeds and repetitions. This measures how often
scheduled faults actually matched dependency traffic, rather than the number of
individual TCP matches or the campaign's SLO pass rate. A choice that had positive
baseline exercise and now has zero fails the regression check. Every planned
choice must also meet `min_exercised_rate`, a finite value from 0 to 1. Equality
passes. The default zero rate still detects complete loss of a previously
exercised choice. Choices with no planned events are reported as `not_planned`.
A partial drop above the configured absolute floor passes unless a per-choice
coverage-drop tolerance is configured as described below.

Regression checks can run without `min_exercised`, or alongside it. A regression
fails the root campaign and CI exit status even if every seed passes its SLOs.
Passing the comparison never clears SLO, cancellation, cleanup or coverage-target
failures. Follow-up guidance usually selects different seeds, so it is suitable
for closing gaps; regression comparisons require a matching campaign schedule.

Terminal, HTML, JSON and JUnit reports retain the baseline run ID and SHA256,
per-choice planned counts, baseline/current exercised counts and rates, complete
coverage-loss flags, and failure reasons. JSON evidence is under
`fault_matrix.campaign_coverage.regression`; JSON validation includes
`campaign_coverage_regression`.

Failed regression campaigns receive a root run ID and a full version-10 campaign
replay descriptor, including when no `min_exercised` gate is configured. Aggregate
replay pins the regression policy and baseline fingerprint and reruns the complete
campaign. Changing or removing the policy or baseline invalidates replay. Keep
the original baseline report with identical bytes. New report outputs cannot
overwrite the baseline file or a hard-link alias. Isolated seed replay remains
available for seed-level failures and does not evaluate the aggregate comparison.

### Per-choice coverage-drop tolerances

Extend a campaign regression policy with `max_drop_percentage_points` and
`min_planned_events` to detect partial deterioration above the absolute rate floor:

```yaml
regression:
  from_report: reports/coverage-baseline.json
  run_id: "BASELINE_ROOT_RUN_ID"
  min_exercised_rate: 0.5
  max_drop_percentage_points:
    0: 20
    1: 10
  min_planned_events: 5
```

The map uses zero-based fault-choice indices and finite allowances from 0 to 100.
A drop from 90% exercised to 60% is **30 percentage points**. A choice with a
20-point allowance fails that comparison even though its 60% rate remains above
the 50% absolute floor. Equality with the allowance passes. Zero permits no drop;
improvements have a negative reported drop and pass the tolerance check.
Allowances apply only to explicitly listed choices. Omitting the map preserves the
existing regression policy and replay fingerprints.

`min_planned_events` is required when drop allowances are configured and must be
2..180. Each listed choice must have at least that many planned fault windows in
the baseline and current campaign. Existing compatibility checks require identical
ordered seeds and repetition counts, so planned counts match. Too few events,
including an unplanned listed choice, fails validation before provisioning.
Declaring a sample minimum without any allowances is invalid. This is a minimum
count of scheduled fault windows, not TCP matches, requests, or a statistical
confidence guarantee. Increase the number of seeds or repetitions within the
existing run and workload caps when necessary, and create a compatible baseline.

The drop is calculated directly from event counts as
`100 * (baseline exercised - current exercised) / planned`, which avoids
subtracting two rounded display rates. Complete coverage loss and the absolute
minimum rate still fail independently; a generous drop tolerance cannot disable
those checks. A canceled or incomplete campaign still fails through its existing
execution checks.

Validation previews show per-choice allowances and the sample minimum. Terminal,
HTML, JSON and JUnit evidence show baseline and current rates, signed drop,
allowance and the drop check result. Configured zero allowances remain visible.
JSON choice evidence adds `drop_percentage_points`,
`max_drop_percentage_points` and `drop_status`; the regression summary adds
`min_planned_events`.

A failed tolerance check fails the root campaign and CI exit status even when
seed SLOs and the absolute rate floor pass. Version-10 aggregate replay pins both
the allowance map and sample minimum together with the existing baseline
fingerprint. Changing, removing or relaxing either setting invalidates replay.

### Offline campaign coverage trends

Render exercised-event rates across application versions without logging in or
provisioning workloads:

```sh
gregale test coverage-trend --manifest gregale-test.yaml --scenario checkout \
  --config coverage-trend.yaml --html coverage-trend.html
```

The configuration explicitly orders two to ten campaign roots. Report paths are
relative to this configuration file. Labels can identify application versions;
Gregale does not infer versions from report timestamps.

```yaml
reports:
  - from_report: reports/v1.json
    run_id: V1_CAMPAIGN_ROOT_ID
    label: v1.0
  - from_report: reports/v2.json
    run_id: V2_CAMPAIGN_ROOT_ID
    label: v1.1
  - from_report: reports/v3.json
    run_id: V3_CAMPAIGN_ROOT_ID
    label: v1.2
min_exercised_rate: 0.5
max_drop_percentage_points:
  0: 20
min_planned_events: 5
```

All points must have the same scenario, profile, workload, fault choices, ordered
seeds, and repetitions. Use the matching campaign manifest. Complete raw history
with clean cleanup is required; skipped, incomplete, overlapping, and inconsistent
history is rejected. Separate executions of the same seeds count independently.
Each report is fingerprinted, and each chart point links to its run ID, report
SHA256, counts, and findings. Labels are escaped when rendered.

Every point compares with the **first point**, so gradual drops stay visible.
Previously exercised choices losing all matches are marked as failures. Optional
minimum rates and per-choice drop allowances follow the campaign regression
rules, including equality passing, zero allowances, and minimum planned-event
counts. An unplanned choice has no rate or chart point. Rates measure exercised
fault events, not application SLO success or statistical confidence.

This command returns zero when it successfully renders a valid trend, even when
points show tolerance breaches. Invalid inputs or output errors return nonzero.
Use campaign regression gates for CI enforcement. The output cannot overwrite
an input report, manifest, or trend configuration, including through hard links.

### Offline recovery performance trends

Compare recovery behavior across application versions using compatible campaign
reports, without logging in or provisioning workloads:

```sh
gregale test recovery-trend --manifest gregale-test.yaml --scenario checkout \
  --config recovery-trend.yaml --html recovery-trend.html
```

Use the same ordered `reports` entries as coverage trends, with optional recovery
settings:

```yaml
reports:
  - from_report: reports/v1.json
    run_id: V1_CAMPAIGN_ROOT_ID
    label: v1.0
  - from_report: reports/v2.json
    run_id: V2_CAMPAIGN_ROOT_ID
    label: v1.1
recovery:
  min_samples: 10
  max_recovery_increase_percent: 20
  max_p95_increase_percent: 15
  max_error_rate_increase: 0.02
```

The command applies the coverage trend compatibility, raw-history, fingerprint,
input ordering, and overwrite protections. It creates three charts per fault
choice and configured recovery target: confirmed recovery time in milliseconds,
recovery-phase p95 latency in milliseconds, and recovery-phase error rate. Each
point shows the **maximum** across all planned events for that choice and target.
Phase p95 values are not combined into a pooled percentile. Aggregate recovery
and named endpoint recovery use their own metrics and confirmation evidence.

Configured tolerances compare each point with the **first point**. Recovery and
p95 tolerances are relative percentages from 0 to 1000; error-rate tolerance is
an absolute increase from 0 to 1 (`0.02` means two percentage points). Equality
passes, improvements pass, and explicit zero permits no increase. A zero baseline
permits no positive increase for relative tolerances. Omitted tolerances disable
that metric's comparison; omitted `recovery` settings default to one minimum
sample and no tolerance comparisons. If `recovery` is supplied, `min_samples`
is required and must be from 1 to Gregale's per-run request limit.

Every planned fault event must be exercised. Missing exercise invalidates that
choice's measurements, rather than silently selecting only exercised events.
Unconfirmed recovery, including failed stability observation, produces a recovery
time gap. Missing or inconsistent sample counts and rates produce metric gaps.
Each recovery phase or endpoint must meet `min_samples`; p95 also requires that
many latency samples. Missing evidence remains a visible failure finding even
without configured tolerances. Unplanned choices have no chart point. These
charts describe reported observations and do not establish statistical confidence.

Chart points link to run IDs, report SHA256 fingerprints, findings, and leaf-run
recovery phases. Rendering a valid report returns zero even when it contains
regressions; malformed history and output errors return nonzero. Use execution
regression gates when CI must fail on a regression. Recovery settings are accepted
by `recovery-trend`; `coverage-trend` rejects them.

### Campaign recovery regression gates for CI

Configure `resilience.campaign_coverage.recovery_regression` to fail campaign
execution when recovery deteriorates against a saved campaign baseline:

```yaml
resilience:
  runs: 1
  min_pass_rate: 1
  campaign_coverage:
    step: load
    seeds: [42, 43, 44]
    max_runs: 3
    max_workload_minutes: 12
    recovery_regression:
      from_report: reports/recovery-baseline.json
      run_id: BASELINE_CAMPAIGN_ROOT_ID
      checks:
        - choice: 0
          limits:
            min_samples: 5
            max_recovery_increase_percent: 20
            max_p95_increase_percent: 15
            max_error_rate_increase: 0.02
        - choice: 1
          request: checkout
          limits:
            min_samples: 5
            max_recovery_increase_percent: 0
```

Choice indices are zero-based. Omit `request` for aggregate recovery, or name an
endpoint with a configured `step_recovery` assertion. Configure one to 32 unique
choice/target checks, each with at least one tolerance and a required
`min_samples` from 1 to Gregale's per-run request limit. Report paths are relative
to the manifest. Baselines must match the scenario, profile, workload, fault
choices, ordered seeds, and repetitions. Baseline history is recomputed and
validated before provisioning, and cannot be overwritten by report outputs.

For every selected choice/target, the baseline must exercise every planned fault
event and provide confirmed recovery (including configured stability), valid
recovery-phase p95 samples, and consistent error-rate samples. Every event must
meet its check's sample minimum. Unplanned choices and unusable baselines are
rejected before execution. Endpoint checks use their own confirmation and
metrics; aggregate metrics cannot replace missing endpoint evidence.

Execution uses the same worst-observed metrics as recovery trends: maximum
confirmed recovery time, maximum recovery-phase p95, and maximum recovery-phase
error rate across all planned events for each selected choice/target. It does
not pool p95 values. Recovery and p95 tolerances allow relative increases from
0 to 1000 percent; error-rate tolerance allows an absolute increase from 0 to 1.
Equality and improvements pass; explicit zero permits no increase. Omitted
metric tolerances disable that metric's increase comparison. Complete confirmed
recovery and valid samples for all three metrics are still required for selected
targets. Missing exercise, confirmation, samples, or complete campaign history
fails the gate instead of being treated as an improvement.

A failed gate makes the campaign root fail and returns a nonzero test exit code,
even when every seed group passes its load SLO. Existing SLO, coverage, cleanup,
and cancellation failures remain independent. JSON, JUnit, terminal, and HTML
reports include baseline identity, SHA256, limits, baseline/current measurements,
findings, and affected leaf-run recovery phases. `--validate` previews the pinned
baseline and checks without provisioning.

Failed aggregate replay retains all seeds and repetitions and pins the complete
gate policy, baseline fingerprint, and validated baseline measurements. Changing
or removing the gate, its limits, sample minima, or baseline report bytes requires
fresh validation and cannot silently change a saved replay. Isolated leaf replay
continues to replay one seed; aggregate gates are evaluated by the aggregate run.

### Pending requests during recovery

Aggregate `recovery` and endpoint `step_recovery` assertions accept an optional
`max_pending_age`. For example:

```yaml
recovery:
  within: 5s
  sustain: 2s
  window: 1s
  max_pending_age: 500ms
  thresholds: {p95: 200ms, error_rate: 0.01, min_samples: 5}
```

A rolling window is unhealthy if any HTTP request started in this load
step is still pending longer than this budget, even when other completed
requests meet every latency and error-rate threshold. A completed request
ceases to count as pending; its full latency and result enter the usual
completion window. Endpoint assertions count only that endpoint's requests.
The age budget must be a whole number of milliseconds between 1ms and 5m.
Requests still in flight from earlier phases count against the pending age
budget; their completions do not enter recovery latency or error-rate windows.
This is an HTTP client observation, not a count of background jobs inside the app.

JSON receipts and JUnit system-out include `max_pending_age_ms`,
`overdue_requests`, and the evaluated window's `thresholds`. HTML displays
these measurements. These are the last evaluated window, rather than an
aggregate across the phase; sustained recovery and optional stability still
require every checked window in their intervals to be healthy. Omitting
`max_pending_age` preserves the existing completion-based behavior.


The HTTP write starter also provides `gregale-dependency-restart-test.yaml` and
its `http-dependency-restart` suite. It replaces the payment service with a
Postgres-backed pending or committed write while the primary caller stays alive.
An externally migrated, disposable Postgres runtime connection is required;
VM-local storage is not used for the durable ledger. Its cleanup removes only
the run namespace. See the starter README for role separation, reachability,
reconciliation assertions, and the real Postgres portable acceptance target.

Workload `secrets` may use `${env.NAME}` with an uppercase environment variable
name. `--validate` checks reference syntax without reading the value. Preflight
checks that each required environment reference exists and is nonempty; results
contain only workload/variable names. Execution resolves it from the runner's
environment and uses the existing sealed-secret API. A missing/empty reference
fails rather than falling back to a literal or another credential. Keep runtime
credentials separate from migration credentials and supply them securely.

### Reusable route assertion profiles

Declare `assertion_profiles` at the manifest root and reference their names with
`assertions` on a load chaos phase. Profiles can be reused across phases and
scenarios in the same manifest:

```yaml
version: 1
assertion_profiles:
  inventory-stays-healthy:
    healthy_routes:
      inventory-read: {p95: 750ms, error_rate: 0, min_samples: 3}
  payment-recovers:
    recovery_deadlines:
      payment-read:
        within: 4s
        sustain: 1s
        window: 1s
        thresholds: {p95: 750ms, error_rate: 0, min_samples: 1}
# Within a staged step's load:
# chaos_schedule:
#   - name: payment-outage
#     duration: 4s
#     assertions: [inventory-stays-healthy]
#     rules: [{to: payment, kind: http_status, status_code: 503, percent: 100}]
#   - name: recovery
#     duration: 6s
#     assertions: [inventory-stays-healthy, payment-recovers]
```

Route keys are the names of HTTP requests declared in that load step, rather
than URL paths. A healthy-route profile requires a latency limit (`p95` or
`p99`), an explicit success/error rate budget, and positive `min_samples`.
These budgets apply independently to each route in each referenced phase;
healthy traffic cannot hide behind aggregate measurements of other routes.

Recovery profiles use the existing endpoint recovery evaluator. They require a
fault-free phase immediately following a fault, and `within + sustain + stability`
(if supplied) must fit its duration. `min_samples` is evaluated per recovery
window. Recovery must meet the request expectations and configured window
budgets; an empty window cannot pass. Existing `relative`, `max_pending_age`,
and `stability` settings remain available in each recovery budget.

Unknown or repeated profile references, undeclared request names, and overlapping
budgets from inline `step_thresholds`/`step_recovery` or another profile fail
validation. Profiles do not silently override one another. Up to 100 profiles
may be declared, with up to 100 route budgets of each kind per profile.

JSON and JUnit's embedded receipt retain each phase's `assertions` names alongside
per-route measured samples, percentiles, threshold verdicts and recovery timing.
HTML and terminal reports show the profile names and existing measured results.
Replay fingerprints bind the expanded limits of profiles used by the scenario.
The [complete reference manifest](../examples/scenario-http-write/gregale-assertions-test.yaml)
uses the two-dependency fixture; the same profile syntax works with your own
application and HTTP request names. Its sealed native fixture requirements are
listed in the [native acceptance guide](scenario-restart-native-acceptance.md).

### Reusable dependency-attempt limits

An assertion profile can also limit the average number of dependency attempts
per named incoming request during one fault phase:

```yaml
assertion_profiles:
  payment-retry-limit:
    dependency_attempts:
      request: payment-read
      max_attempts_per_request: 2
      min_requests: 3
  connection-limit:
    connection_attempts:
      request: dependency-probe
      max_attempts_per_request: 2
      min_requests: 3
```

Reference `payment-retry-limit` in a phase's `assertions` alongside its healthy
route profile. HTTP `dependency_attempts` expands into `retry_amplification` and
requires exactly one 100% `latency` or `http_status` rule with an explicit `from`
workload. Its rule-match counter measures all HTTP calls from that workload to
the rule's dependency, including retries. Select a named incoming request that
accounts for that dependency traffic; other incoming routes do not increase its
denominator. Background calls or other routes using the same dependency still
contribute to the dependency counter.

TCP `connection_attempts` requires exactly one 100% TCP rule, an explicit `from`
and a valid declared port. It uses the dedicated connection-attempt counter,
including connections that fail before a data-plane fault matches. Ordinary TCP
fault-match counts cannot substitute for connection attempts. Gateways without
this counter fail the assertion with an evidence-unavailable error.

These are phase-wide ratios, not individually traced per-request retry limits.
`min_requests` must be positive, and `max_attempts_per_request` must be finite,
greater than zero and at most 1000. Empty incoming samples and missing or
incomplete captures fail rather than reporting zero amplification as a pass.
A profile cannot combine HTTP and TCP attempt limits, because they need different
rules. Profiles also cannot overwrite inline `retry_amplification` or
`connection_attempts`, or another profile's attempt budget.

Reports retain the profile name, named request, dependency source/target, TCP
port when applicable, incoming request count, observed attempts, ratio, limit,
and verdict. Replay fingerprints include the effective attempt budget. The
[HTTP reference](../examples/scenario-http-write/gregale-assertions-test.yaml)
combines retry limits with isolation and recovery checks; the
[TCP reference](../examples/scenario-checkout/gregale-connection-assertions-test.yaml)
uses the existing echo/probe fixture and also checks responsiveness and recovery.

### Per-request retry limits

Opt into strict HTTP attribution when an average can hide retry outliers:

```yaml
assertion_profiles:
  strict-retry-limit:
    dependency_attempts:
      request: dependency-read
      per_request: true
      max_attempts_per_request: 2
      min_requests: 3
```

Nine requests making one attempt and one making eleven have an average of two.
The average budget passes; this strict profile fails with one violating request.
Reports include the attempt distribution, maximum, correlated/request counts,
uncorrelated attempts, overflow and up to 100 violating request IDs.

Each selected request receives `X-Gregale-Test-Request-ID`. Your application must
forward that header on every selected dependency call, including retries, and
return `X-Gregale-Test-Dependency-Attempts` with its final attempt count. Count
attempted calls even if they fail, and settle all selected outgoing work before
sending the completion receipt. For example, in a Node HTTP handler:

```js
const id = request.headers["x-gregale-test-request-id"];
let attempts = 0;
const callDependency = async () => {
  attempts++;
  return fetch(dependencyURL, {
    headers: id ? { "x-gregale-test-request-id": id } : {},
  });
};
// Await calls and retries through callDependency, then finish the response:
if (id) response.setHeader("x-gregale-test-dependency-attempts", String(attempts));
```

The receipt alone cannot pass the assertion: Gregale verifies each count against
independent gateway records and verifies their total against the rule's HTTP
matches. Missing propagation, stale IDs, absent or invalid receipts, missing or
late counters and unexplained calls fail. Requests acknowledging zero attempts
are covered when the phase also has independent gateway correlation evidence;
a wholly inactive dependency phase cannot establish support.

Strict limits are positive integers and apply to one fully counted HTTP
latency/status rule with an explicit source. TCP `connection_attempts` remains
an average assertion. There is a fixed capacity of 1,024 selected incoming
requests per strict phase; overflow fails instead of silently sampling.
Fault-plan changes pause dispatch and drain in-flight requests to preserve
phase attribution. Actual phase timings remain recorded. IDs are run/phase
scoped; production tracing headers and Prometheus labels remain unchanged.

The [complete strict example](../examples/scenario-http-write/gregale-per-request-attempts-test.yaml)
uses the instrumented retry fixture. SDK chaos-rule models also expose the
optional `correlation_scope` wire field. Ordinary manifests do not enable it.
Native execution requires a gateway with correlation-counter support; portable
HTTP tests prove that outliers fail even while the average passes, and that
missing completion or propagation evidence cannot pass.

### TCP faults in declaration order

Use `load.fault_campaign.order: declared` to execute every choice exactly once
in the order listed. Set `count` to the number of choices (2–9); retain an explicit
`seed` and `max_duration`. Omitting `order` keeps seeded choice selection. All
choices target the same source, dependency and declared TCP port.

The [TCP campaign example](../examples/scenario-checkout/gregale-tcp-campaign-test.yaml)
runs latency, bandwidth, timeout and reset in sequence. Its three-phase template
expands into a 26-second campaign: two seconds of baseline, then four cycles of
two-second faults and four-second recovery windows. Each fault applies a reusable
connection-attempt limit and healthy-route budget; each recovery requires the
named dependency probe to recover within one second and remain healthy for one
second. The 16 KiB echo fixture makes a 1 KiB/s bandwidth restriction exceed the
probe's 250 ms deadline.

```sh
gregale test --manifest examples/scenario-checkout/gregale-tcp-campaign-test.yaml \
  --suite tcp-campaign --validate
gregale test --manifest examples/scenario-checkout/gregale-tcp-campaign-test.yaml \
  --suite tcp-campaign --report results.json --junit results.xml --html results.html
```

Reports include `declared-splitmix64-v1`, the expanded schedule, per-cycle
`fault_kind`, connection ratios, health budgets, recovery verdicts, and the first
failed cycle. A connection-budget failure identifies its phase and cause. Missing
gateway attempt counters fail the test. JSON and JUnit preserve measured evidence;
HTML and terminal output show each fault alongside its cycle verdict. Replay binds
the declared order and effective assertions and retains minimized schedules.

These connection limits are phase-wide ratios. Use the same template with your
application and database, Redis or another declared TCP service; the echo fixture
does not certify any database protocol or client library. The platform performs
normal isolated workload cleanup and records its result. Local tests do not replace
native acceptance on the designated KVM host.

### Detect retained TCP connections

A staged TCP schedule can add `load.connection_leaks`:

```yaml
connection_leaks:
  path: /connections
  within: 1s
  sustain: 1s
  max_extra: 2
```

The side-effect-free application endpoint must return HTTP 200 and one JSON
object, for example:

```json
{"instance_id":"01234567-89ab-4cde-8fab-0123456789ab","active_connections":0}
```

Report an integer count of client connections in the tested dependency scope,
including retained idle sockets. The UUID identifies the observed application
instance and must change when it is replaced. You may specify `as` to use an
existing declared consumer credential. Gregale does not export credentials or
observation response bodies.

Warm legitimate connection pools before the load step. Gregale uses the minimum
observed count in the initial baseline, then compares every recovery with that
same value. `max_extra` allows expected in-flight connections during recovery;
for example, two dependency VUs can use an allowance of two. Every recovery must
return within budget by `within`, sustain that state for `sustain`, and remain
within budget through the end of the sampled window. A later cycle cannot replace
the original baseline with a higher count.

After load workers and fault-plan cleanup finish, a separate check requires the
original baseline with **no extra allowance**. It also runs after cancellation,
using an independent timeout bounded by `within + sustain + 500ms`. The interrupted
campaign remains failed; reports distinguish its post-drain cleanup result.
Retained sockets fail this check even when they fit the recovery allowance.

The observation interval is 100 ms. Requests time out after 250 ms, responses are
limited to 4 KiB, and at most 4096 observations are retained. `within` and `sustain`
are whole-millisecond durations from 300 ms to five seconds. Baseline must last
at least 500 ms; every recovery must fit both budgets plus two polling intervals.
Missing fields, negative/fractional counts, missing observations, sparse windows,
changed instance identities and renewed leaks fail the assertion.

Reports include the application source, stable identity, original baseline,
sampled peak, recovery limits and deadlines, remaining connections, first failed
resource cycle, and post-drain result. These are application-reported samples;
a peak between polls can be missed. Use one stable instance or an observation
endpoint that aggregates all relevant instances under an appropriate stable
identity. The [TCP campaign example](../examples/scenario-checkout/gregale-tcp-campaign-test.yaml)
tracks actual outgoing sockets until their close events and cancels probes when
incoming clients disconnect. Native VM qualification remains a separate gate.

### Exercise connection-pool exhaustion

Use `load.pool_exhaustion` alongside healthy-route and dependency recovery
assertions in a staged TCP fault/recovery schedule:

```yaml
pool_exhaustion:
  path: /pool-status
  capacity: 2
  max_queue: 2
  max_wait: 300ms
  min_queued_requests: 3
  within: 1s
  sustain: 1s
  window: 1s
  min_completions: 5
```

The [pool exhaustion example](../examples/scenario-checkout/gregale-pool-exhaustion-test.yaml)
stalls the dependency while eight caller VUs compete for two permits and two
queued slots. It checks responsive health, bounded connection attempts, socket
release, and successful throughput after recovery. The fixture uses real TCP
connections and returns each permit after an attempt; it does not model idle
connection reuse or a particular database driver.

The side-effect-free observation endpoint must return HTTP 200 and one JSON
object containing these fields:

```json
{
  "instance_id": "01234567-89ab-4cde-8fab-0123456789ab",
  "capacity": 2,
  "active": 0,
  "queued": 0,
  "peak_active": 2,
  "peak_queued": 2,
  "waiters_created": 20,
  "waiters_finished": 20,
  "waiters_cancelled": 2,
  "waiters_timed_out": 10,
  "max_wait_ms": 205,
  "completed": 100
}
```

`active` counts held pool permits; `queued` counts callers waiting for a permit.
All numeric fields are required nonnegative integers. `waiters_finished` includes
acquired, cancelled and timed-out waiters, so `waiters_created - waiters_finished`
must equal `queued`. Peaks, waiter counters, maximum wait and completion counts
are cumulative for the same pool instance and must not reset. `completed` counts
successful dependency operations only. The UUID must change when that observed
instance is replaced. You may specify `as` for a declared consumer credential.

Every fault must show at least `min_queued_requests` new waiters between its first
and last observations, and a full-capacity peak. A quiet pool cannot pass as an
exhausted pool. Queue depth and completed wait times must remain within budget;
cumulative high-water marks preserve violations that occur between polls.

Every recovery must restore `min_completions` successful operations per `window`
within the deadline and sustain that throughput, without a later failed window.
Windows slightly wider than requested require proportionally more completions.
Existing named-route recovery assertions independently check HTTP success and
latency. Missing fields, inconsistent accounting, sparse samples, identity changes,
and decreasing counters fail the test.

After load or cancellation, an independent bounded check requires all active
permits and queued waiters to drain. Optional `min_cancelled_waiters` requires
that many additional cancelled waiters compared with the initial baseline. This
is useful when a scenario deliberately cancels queued callers; omit it for runs
that do not arrange that cancellation. The interrupted campaign remains failed,
with its post-drain result reported separately.

Reports retain the budget, baseline, peaks, maximum wait, actual completion
windows, first failed pool cycle and post-drain waiter accounting. These counts
are application-reported and must describe the same dependency scope. Local
fixture and real-stream tests do not qualify routing on the native VM platform.

### Bound the complete application response

Declare `request_deadlines` in a timed phase or reusable assertion profile:

```yaml
assertion_profiles:
  application-timeout:
    request_deadlines:
      dependency-probe:
        within: 750ms
        min_samples: 3
        expect:
          status: 504
          content_type: application/json
          json: {'/error': deadline_exceeded, '/retryable': true}
```

Each name must select exactly one HTTP request in that staged load. `within` is a
whole-millisecond duration from 1 ms to 30 seconds. It covers request preparation,
HTTP dispatch and complete response-body consumption, including network overhead.
Every selected response must meet the deadline and its response contract; a good
p95 cannot hide an outlier. Requests aborted by Gregale's client fail the contract,
even if they received the expected status before their body stalled.

An optional phase-local `expect` replaces that request's status/content-type/JSON
contract only in that phase. Omit it to retain the original success expectation.
Deadline profiles cannot overwrite another profile or inline budget for the same
route. Referenced profiles and effective budgets are retained in replay evidence.

With request-deadline assertions enabled, the runner drains admitted load HTTP
requests before transitioning phases. This prevents clearing a dependency fault
while its expected timeout response is still in flight. The bounded drain can
shorten the following scheduled recovery window; actual boundaries are reported.
Allow enough time for the deadline, recovery and sustained healthy observations.

The [application deadline example](../examples/scenario-checkout/gregale-request-deadlines-test.yaml)
uses a single 500 ms application timer across connection acquisition and dependency
I/O, with a 750 ms client-observed budget. It verifies a complete HTTP 504 timeout
response during a TCP stall, responsive health, successful responses after recovery,
and a return to the original socket baseline after workers finish. Client
cancellation closes that request's dependency socket and retires its timers.

Reports include the measured maximum, deadline, named request sample count,
response failures, expected status and verdict. Use the existing connection-leak
or pool-drain checks to independently observe downstream resources after load or
cancellation. These checks use application-reported counters and do not replace
native VM qualification or certify arbitrary multi-service workflow deadlines.
