# Gregale scenario test example

This small local API example exercises authenticated submission, idempotency,
owner access, and cross-customer isolation. Gregale starts a fresh Node process
for each run, waits for `/health`, runs the declared requests and checks, and
shuts the process down. It needs no Gregale account or platform resources.

## Run it locally

Install Node.js 20 or newer and the Gregale CLI, then run these commands from
the repository root:

```sh
export GREGALE_TEST_CONSUMER_CUSTOMER_A_KEY=example-customer-a
export GREGALE_TEST_CONSUMER_CUSTOMER_B_KEY=example-customer-b

gregale test --manifest examples/scenario-tests/gregale-test.yaml \
  --suite smoke --report test-results.json --junit test-results.xml \
  --html test-results.html
```

The keys above are throwaway local fixture values. The sample server reads the
same values from its environment and does not contact Gregale. Open
`test-results.html` in a browser for run status, HTTP steps, timing, cleanup,
and the full structured evidence.

## Compare saved runs

Run the suite twice and save each report, then compare them without starting the
application or contacting the platform:

```sh
gregale test --manifest examples/scenario-tests/gregale-test.yaml \
  --suite smoke --report before.json
gregale test --manifest examples/scenario-tests/gregale-test.yaml \
  --suite smoke --report after.json
gregale test compare before.json after.json --html comparison.html
```

The comparison matches runs by scenario, engine, profile, case, and attempt. It
shows added or removed runs, status changes, phase and HTTP step timings, and
load metrics when present. This command is a review diff; use `--baseline` on a
load run when you want configured regression budgets to gate the run.

## GitHub Actions

Copy [`ci/github-actions.yml`](ci/github-actions.yml) to
`.github/workflows/gregale-test.yml` in your application repository. It installs
the checksum-verified CLI, runs the local suite, and uploads JSON, JUnit, and
HTML reports even when a check fails. The CLI release must include the `--html`
and `test compare` options shown here.

The more complete multi-workload fixture in
[`../../tests/scenario-acceptance/README.md`](../../tests/scenario-acceptance/README.md)
also covers worker invocation, object storage, delivery retry, and the real VM
lifecycle profiles.
