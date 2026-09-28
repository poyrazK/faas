# Application scenario tests

`gregale test` deploys source into expiring developer sessions and runs the
repository's own assertions through its public Gregale URL. Each lifecycle
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
    consumer_auth_mode: required
    consumers:
      - name: customer-a
      - name: customer-b
        scopes: [read]
    services:
      worker:
        source: ./worker
        secrets:
          NOTIFICATION_URL: ${service.notifications.url}/deliver
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
      queue_idle: true
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

Run all three profiles, or select one:

```sh
gregale test --scenario customer-export --report test-results.json
gregale test --scenario customer-export --profile restored
gregale test --scenario customer-export --engine simulated
```

`real-vm` is the default engine. `--engine simulated` runs the separate
`simulation` command locally, once, without platform provisioning or login.
It receives `GREGALE_TEST_ENGINE=simulated`,
`GREGALE_TEST_PROFILE=simulated`, and `GREGALE_TEST_SCENARIO`. The report omits
VM wake evidence and labels its engine `simulated`. VM lifecycle profiles only
apply to `real-vm`, and passing `--profile` with `--engine simulated` is an
error. A simulation can test application logic quickly, while the real-VM runs
prove the platform lifecycle path.

The assertion command receives `GREGALE_TEST_URL`,
`GREGALE_TEST_APP_SLUG`, `GREGALE_TEST_RUN_ID`, `GREGALE_TEST_PROFILE`, and
`GREGALE_TEST_ENGINE=real-vm`. Send application requests to
`GREGALE_TEST_URL`; it is a local pass-through proxy for the isolated app.
This lets the CLI record the first request's `X-Faas-Wake` and
`X-Faas-Wake-ID` without changing the application's request or response.
The setup and cleanup commands receive the same variables. Commands are
argument arrays, not shell strings. Additional services receive logical names
such as `worker.svc` inside the platform. Local commands receive
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
The runner sets per-workload `secrets` on these expiring apps before deploying.
Values can reference `${service.NAME.url}`, `${service.NAME.slug}`,
`${bucket.NAME.name}`, or `${run.id}`. The example gives the worker a test
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
`GREGALE_TEST_URL`. When `wait_for` is present, Gregale polls the isolated
workloads' queues until each has zero depth and in-flight work on two consecutive
polls, and polls declared
bucket prefixes until the minimum object count and total size are present.
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
first response status, wake headers, completed method, and cleanup outcome.
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
