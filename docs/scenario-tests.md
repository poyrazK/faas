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
model: throughput depends on the app's response speed. It does not promise a
fixed arrival rate. Captures start empty for every journey. `${run.id}` becomes
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
    error_rate: 0.01
    steps:
      submit: {p95: 200ms, error_rate: 0}
      read: {p95: 100ms}
```

`thresholds.steps` keys must name declared HTTP requests or checks. Step budgets
add to the aggregate limits; they do not replace them. Only explicitly supplied
step limits apply. A step with a budget and no samples fails, including a check
skipped because an earlier request failed. This prevents an aggregate percentile
or an allowed error rate from hiding a problem in a critical step. JSON reports
include evaluated budgets under each step, terminal summaries identify them by
step name, and failed budgets fail the JUnit case and command.

This optional block configures `--load`; it does not enable load for a normal
test. Replace `iterations` with `duration: 30s` to choose a timed run. CLI flags
override the manifest; selecting `--duration` clears its iteration setting and
stages, and selecting `--iterations` clears its duration and stages. Without
configuration, `--load`
uses one user and 100 journeys. `--vus`, `--iterations`, and `--duration` require
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
including after a failed journey. It applies to all load modes and defaults to
zero. It adds no pause after the final journey and does not count toward HTTP
step latency. The allowed range is 0 seconds to 1 minute; `--pacing 0s` disables
manifest pacing. This remains a concurrency model, not a fixed arrival-rate
promise.

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
fraction from 0 to 1. An expected 403 passes. The default error threshold is zero;
there is no default latency threshold. Configured limits are inclusive. Crossing
either threshold fails the receipt and exits with status 1. No samples, canceled
runs, time limits, incomplete runs caused by the request budget, and failed
cleanup also fail, regardless of the error tolerance.

The terminal summary shows throughput, p95 latency, failure counts, thresholds,
and the first error for up to ten failed steps. The JSON `load` object reports
started, completed, failed, and interrupted journeys, peak concurrency,
HTTP-step throughput, response status counts, error rate, and
min/mean/p50/p95/p99/max latency. Each step gets its own metrics and up to five
distinct error examples. Status `0` means no HTTP response
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
such as `http://worker.svc.gregale:10080` inside the platform. Local commands receive
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
