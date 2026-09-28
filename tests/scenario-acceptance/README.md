# Scenario test acceptance fixture

This fixture drives the `gregale test` command through one app and Gregale's
built-in delivery sink. The app sends a notification, receives a 503, retries,
and receives a 200. The runner checks warm, cold, and restored evidence for
both workloads and reports the delivery attempts.

Run the fast local simulation from the repository root. It starts the app and
delivery sink as local HTTP servers and checks the retry and recorded payload:

```sh
gregale test --manifest tests/scenario-acceptance/gregale-test.yaml \
  --scenario delivery-smoke --engine simulated
```

On a live Gregale installation with a Hobby-or-higher test account, run the
real VM acceptance:

```sh
gregale test --manifest tests/scenario-acceptance/gregale-test.yaml \
  --scenario delivery-smoke --engine real-vm \
  --report scenario-acceptance-report.json
```

The report should contain three passed runs (`warm`, `cold`, `restored`),
`real-vm` as the engine, `[503, 200]` delivery statuses in each run, and
service hot or boot-method evidence. The runner destroys each expiring test
environment after its profile. This fixture needs source-build and VM services,
plus request telemetry for the warm service check.
