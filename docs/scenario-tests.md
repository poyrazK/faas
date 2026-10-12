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
returns a synthetic 5xx response; `--latency` delays selected calls and then
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
The sample uses an echo acknowledgement and an in-memory ledger; its README
explains the limits of those substitutes for a production payment integration.

Use `steps` when one scenario needs to compare baseline behavior, behavior under
a fault, and recovery in the same application process. The first step establishes
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
Without `--budget`, the comparison is informational. Pass a versioned YAML
budget file to enforce run outcomes and optional performance limits:

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
