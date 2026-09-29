# Scenario test acceptance fixture

The `delivery-smoke` fixture checks one app and Gregale's delivery sink.
The `customer-export` fixture checks an authenticated gateway, an async
worker invocation, an isolated object bucket, and a notification sink. Its
trigger records the worker invocation ID; the runner waits for that exact
invocation to complete before application assertions. It asserts customer
ownership, forbidden cross-customer reads, duplicate suppression, and a
503 followed by a successful delivery. The worker deliberately returns 503 on
its first attempt and persists a marker in the isolated bucket; the platform
must retry the same invocation before it can complete. Assertions reject a
direct worker request without the per-run shared secret. The real VM runner
also checks warm, cold, and restored evidence for every workload.
The simulation and real VM assertion command call the same export contract in
`export-api/test/contract.mjs`; the simulation additionally checks its local
queue attempt count and object count.

Run the fast local simulation from the repository root. It starts the app and
delivery sink as local HTTP servers and checks the retry and recorded payload:

```sh
gregale test --manifest tests/scenario-acceptance/gregale-test.yaml \
  --scenario delivery-smoke --engine simulated
gregale test --manifest tests/scenario-acceptance/gregale-test.yaml \
  --scenario customer-export --engine simulated
```

On a live Gregale installation, run the real VM acceptance. `delivery-smoke`
needs a Hobby or higher account; `customer-export` needs Pro or higher for its
three workloads, consumer keys, and managed bucket:

```sh
gregale test --manifest tests/scenario-acceptance/gregale-test.yaml \
  --scenario delivery-smoke --engine real-vm \
  --report scenario-acceptance-report.json
gregale test --manifest tests/scenario-acceptance/gregale-test.yaml \
  --scenario customer-export --engine real-vm \
  --report customer-export-report.json --junit customer-export-report.xml
```

Each report should contain three passed runs (`warm`, `cold`, `restored`),
`real-vm` as the engine, at least two attempts on the worker invocation,
`[503, 200]` delivery statuses in each run, and
service hot or boot-method evidence. The customer export report should also
contain one object under the run's `reports/` prefix. The runner destroys each
expiring test environment after its profile. The real VM path requires
source-build, VM, object-storage and request-telemetry services.

Use `--validate` locally before running the scenario. `--preflight` checks
account limits and estimates deployment count without provisioning. A repeat
run such as `--profile restored --repeat 3` creates three independent
environments and records one JUnit case for each attempt. The manual
`scenario real VM acceptance` GitHub workflow accepts a profile and up to
three repeats. It requires the `scenario-acceptance` environment, a live
`GREGALE_ACCEPTANCE_API_URL` variable, and a Pro or higher
`GREGALE_ACCEPTANCE_TOKEN` secret.
