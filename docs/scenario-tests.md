# Application scenario tests

`gregale test` deploys source into expiring developer sessions and runs the
repository's own assertions through its public Gregale URL. Each lifecycle
profile gets a distinct run and, when requested, isolated managed PostgreSQL
databases. The CLI destroys the sessions after the test, including after an
assertion failure. The server lease is a backstop if the CLI process disappears.
Each run registers a private service namespace. A service call from a test app
can only resolve a workload in that run; a missing name cannot reach a
production app.

Create `gregale-test.yaml` at the repository root:

```yaml
version: 1
scenarios:
  customer-export:
    project: export-api
    source: ./gateway
    services:
      worker:
        source: ./worker
        secrets:
          NOTIFICATION_URL: ${service.notifications.url}/deliver
      notifications:
        source: ./test/notification-sink
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
      objects:
        - bucket: exports
          prefix: reports/${GREGALE_TEST_RUN_ID}/
          min_count: 1
          min_total_bytes: 1
    command: [node, --test, test/customer-export.test.mjs]
    cleanup:
      - [node, test/fixtures/cleanup.mjs]
```

Run all three profiles, or select one:

```sh
gregale test --scenario customer-export --report test-results.json
gregale test --scenario customer-export --profile restored
```

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
Each workload's normal `gregale.yaml` is applied during deployment. A worker
can declare `queue_bindings` and retry policy there; the runner waits for
queues on all test workloads when `wait_for.queue_idle` is enabled. Queue
binding plan gates still apply.
The runner sets per-workload `secrets` on these expiring apps before deploying.
Values can reference `${service.NAME.url}`, `${service.NAME.slug}`,
`${bucket.NAME.name}`, or `${run.id}`. The example gives the worker a test
notification endpoint. Its app can return a failure for the first delivery,
then record the retries for the assertion command to inspect through
`GREGALE_TEST_SERVICE_NOTIFICATIONS_URL`. Secret values are sent to Gregale's
sealed secret API and are not included in the test report.

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
| `warm` | Park and explicitly wake the app, then wait for a running instance. | `X-Faas-Wake: hot`, with no wake ID. |
| `cold` | Drain the preview and invalidate its snapshots. | A wake ID whose completed boot method is `cold_boot`. |
| `restored` | Drain the preview while retaining its deployment snapshot. | A wake ID whose completed boot method is `restore`. |

The report fails a profile when the assertion command fails, sends no request
through the proxy, or when the completed wake method differs from the requested
profile. A restore that falls back to cold boot is recorded as cold boot and
fails the restored profile. Reports include the run ID, app slug, deployment ID,
first response status, wake headers, completed method, and cleanup outcome.

The assertion commands own application-specific identities and expectations.
For an export test they should create two customers, submit and retry the same
export request, inspect the produced object through both customers' credentials,
and check notification delivery. A declared notification sink can return
failures to exercise the application's retry policy. Built-in fault controls,
per-workload lifecycle evidence, and simulated execution are still being added. Test reports label
this path `real-vm`; no simulated run is silently accepted as lifecycle proof.

`--profile cold` relies on the preview-only `fresh=true` form of
`POST /v1/apps/{slug}/park`. The API rejects that option for a production app.
This keeps the test's destructive snapshot invalidation inside its expiring
environment.
