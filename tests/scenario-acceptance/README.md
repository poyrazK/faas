# Scenario test acceptance fixture

The `delivery-smoke` fixture checks one app and Gregale's delivery sink.
The `customer-export` fixture checks an authenticated gateway, a worker,
an isolated object bucket, and a notification sink. It asserts customer
ownership, forbidden cross-customer reads, duplicate suppression, and a
503 followed by a successful delivery. The real VM runner also checks warm,
cold, and restored evidence for every workload.

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
  --report customer-export-report.json
```

Each report should contain three passed runs (`warm`, `cold`, `restored`),
`real-vm` as the engine, `[503, 200]` delivery statuses in each run, and
service hot or boot-method evidence. The customer export report should also
contain one object under the run's `reports/` prefix. The runner destroys each
expiring test environment after its profile. The real VM path requires
source-build, VM, object-storage and request-telemetry services.
