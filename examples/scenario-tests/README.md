# Gregale scenario test example

This local API example exercises authenticated submission, idempotency, owner
access, cross-customer isolation, and a bounded load check for `/health`.
Gregale starts a fresh Node process for each run, waits for readiness, runs the
declared requests and checks, then shuts the process down. It needs no Gregale
account or platform resources.

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
and the full structured evidence. The separate `performance` suite has a small
local `/health` workload that needs no credentials:

```sh
gregale test --manifest examples/scenario-tests/gregale-test.yaml \
  --suite performance --load --report load-results.json --html load-results.html
```

## Compare saved runs

Run the suite twice and save each report, then compare them without starting the
application or contacting the platform:

```sh
gregale test --manifest examples/scenario-tests/gregale-test.yaml \
  --suite performance --load --repeat 3 --report before.json
gregale test --manifest examples/scenario-tests/gregale-test.yaml \
  --suite performance --load --repeat 3 --report after.json
gregale test compare before.json after.json \
  --budget examples/scenario-tests/ci/test-budget.yaml \
  --html comparison.html --markdown comparison.md
```

The report diff matches runs by scenario, engine, profile, case, and attempt.
With the sample budget, Gregale groups repeated attempts, compares median
durations and load percentiles, and checks that their spread stays within the
configured limits. It still checks each later run's outcome and load workload
identity. An exceeded budget or an inconclusive comparison returns a nonzero
exit code while still writing the requested reports. In GitHub Actions, add
`--github-summary` to append the same concise Markdown summary to the job page.
The checked-in CI budgets are permissive starter values for this tiny loopback
fixture and shared runners; tighten them using your application's baseline.

## GitHub Actions

For a starter workflow that runs one suite, use `gregale test ci init` from
the repository root:

```sh
gregale test ci init --manifest examples/scenario-tests/gregale-test.yaml --suite smoke
```

It creates `.github/workflows/gregale-test.yml` and a starter budget at
`gregale-test-budget.yaml`. Add dependency-install steps to the workflow if
your app needs them.

For the repository's platform-backed acceptance suite, generate a real-VM
profile workflow with an explicit workload cap:

```sh
gregale test ci init \
  --manifest tests/scenario-acceptance/gregale-test.yaml \
  --suite real-vm --engine real-vm \
  --profiles warm,cold,restored --max-workload-minutes 135 \
  --environment scenario-acceptance
```

This produces a manual GitHub Actions workflow with serial, per-profile jobs.
Configure its GitHub environment with `GREGALE_TEST_API_URL` and
`GREGALE_TEST_TOKEN`; see the main [scenario testing guide](../../docs/scenario-tests.md#generate-a-github-actions-workflow)
for credential, cost-guard, and report details.

To run both suites, use the full example workflow instead. Copy this
`examples/scenario-tests` directory into your repository, then copy
[`ci/github-actions.yml`](ci/github-actions.yml) to
`.github/workflows/gregale-test.yml`. The workflow runs the functional and load
suites on `main` and uploads both JSON reports as a 30-day baseline artifact.
On pull requests, it downloads the latest successful `main` baseline, runs the
same suites, applies the outcome and load budgets, and uploads JSON, JUnit,
HTML, and Markdown comparison reports. It also adds both comparisons to the
GitHub Actions job summary. The workflow needs `actions: read` to download
the baseline artifact. PR comparison skips only before the first successful
`main` run; afterward, a missing, expired, or older than 30-day baseline fails
the gate. Successful pushes to `main` refresh the artifact; use **Run
workflow** on `main` to create or refresh it without a code change.

The more complete multi-workload fixture in
[`../../tests/scenario-acceptance/README.md`](../../tests/scenario-acceptance/README.md)
also covers worker invocation, object storage, delivery retry, and the real VM
lifecycle profiles.
