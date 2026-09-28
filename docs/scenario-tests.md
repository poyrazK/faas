# Application scenario tests

`gregale test` deploys source into an expiring developer environment and runs
the repository's own assertions through its public Gregale URL. Each lifecycle
profile gets a distinct app and, when requested, a distinct managed PostgreSQL
database. The CLI destroys the environment after the test, including after an
assertion failure. The server lease is a backstop if the CLI process disappears.

Create `gregale-test.yaml` at the repository root:

```yaml
version: 1
scenarios:
  customer-export:
    project: export-api
    source: .
    postgres: true
    buckets:
      - name: exports
        prefix: EXPORT_STORAGE
        permission: read_write
    timeout: 15m
    setup:
      - [node, test/fixtures/seed.mjs]
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
argument arrays, not shell strings.

For each declared bucket, Gregale creates an isolated bucket and a compute
binding before deploying the app. It exposes the bucket name to local commands
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

The assertion command owns application-specific identities and expectations.
For an export test it should create two customers, submit and retry the same
export request, wait for the application-visible completion condition, inspect
the produced object through both customers' credentials, and check notification
delivery. Gregale's test runner currently provisions one HTTP app and optional
PostgreSQL and buckets; queue bindings, notification failure controls, multi-workload
profiles, and simulated execution are still being added. Test reports label
this path `real-vm`; no simulated run is silently accepted as lifecycle proof.

`--profile cold` relies on the preview-only `fresh=true` form of
`POST /v1/apps/{slug}/park`. The API rejects that option for a production app.
This keeps the test's destructive snapshot invalidation inside its expiring
environment.
