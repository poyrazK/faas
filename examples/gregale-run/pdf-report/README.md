# PDF to a durable customer report

This preview example turns a PDF into validated JSON and an HTML report using
Poppler in a fresh Run. A published task pins the environment and resource
contract. The task SDK stages the input, invokes it, waits and delivers the
report into your application's existing private upload route.

Use an installation that has the Run, task, environment and artifact-delivery
capabilities enabled. Its Python 3.13 installer must support Wolfi packages.
Select a package version available in that installer's repositories; this
example uses `poppler-utils=26.08.0-r0` from the public Wolfi index.

```sh
gregale sandbox environment publish --runtime python313 \
  --system-package poppler-utils=26.08.0-r0 --wait --json

export REPORT_ENVIRONMENT_ID=ENVIRONMENT_UUID
python3 examples/gregale-run/pdf-report/make_task.py "$REPORT_ENVIRONMENT_ID" /tmp/pdf-report-task.json
python3 examples/gregale-run/pdf-report/make_sample.py /tmp/input.pdf
```

Before publishing, run the offline manifest, fixture, and generated binding checks.

```sh
bash examples/gregale-run/pdf-report/check.sh
```

For the real smoke test, set `FAAS_API` and `FAAS_TOKEN` for a test account,
`REPORT_ENVIRONMENT_ID` to the ready environment UUID from the build, and
`REPORT_TEST_KEY` to a stable key for your CI attempt. The check expects one
page, five words, and the sample text in the downloaded HTML.

```sh
bash examples/gregale-run/pdf-report/check.sh --remote
gregale run tasks publish --manifest /tmp/pdf-report-task.json
```

Configure a private `reports` upload route allowing `text/html` using the
[artifact-delivery guide](../../../docs/run-artifact-delivery.md). Give the
application's credential the required Run, app and bucket authority. Set
`FAAS_API`, `FAAS_TOKEN`, `REPORT_APP`, `REPORT_ROUTE` and a stable
`REPORT_REQUEST_ID`, then run:

```sh
python3 examples/gregale-run/pdf-report/run.py /tmp/input.pdf
```

The [TypeScript equivalent](run.ts) uses the same task and destination. Both
drivers use the bindings generated from the task's JSON schemas and require the
SDK built from this preview source. Use Node 22.10 or later for TypeScript.

```sh
npm --prefix sdk/node run build
npm --prefix examples/gregale-run/pdf-report install --ignore-scripts --package-lock=false
npm --prefix examples/gregale-run/pdf-report start -- /tmp/input.pdf
```

The response contains
the Run ID, validated result and authenticated delivery receipt. Persist the
delivery reference with its customer owner. Your application can issue a
short-lived download URL after authenticating that customer.

Reuse the same request ID and unchanged input when recovering an uncertain
submission. For recovery across process restarts, persist the SDK's `onPrepared`
intent in private application storage and replay it with `task.submit(intent)`
or Python's `task.submit`/`task.asubmit`. Waiting polls receipts; stopping a wait
leaves the Run active. `TaskRun.cancel()` requests explicit cancellation.

The utility has a 30-second extraction timeout. The example bounds extracted
text to 1 MiB, includes at most 16,000 characters in escaped HTML, and declares
the report as a stored output. Scanned/image-only PDFs need an OCR task;
this example extracts an existing text layer.
