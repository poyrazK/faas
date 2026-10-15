# HTTP rate-limit recovery

This example proves that a caller respects `Retry-After`, stops after three
attempts, and succeeds once a dependency fault is cleared. It calls inventory
through `inventory.svc.gregale:10080`, so the scenario's internal HTTP fault
applies to the dependency call. A public service URL would bypass this fault.

From this directory, with an updated Gregale CLI, API and gateway deployed:

```sh
gregale test --suite http-retry --validate
gregale test --suite http-retry --preflight
gregale test --suite http-retry --max-workload-minutes 20 \
  --report results.json --html results.html --junit results.xml
```

The baseline succeeds in one attempt. A 100% `http_status` fault returns 429
with `Retry-After: 1`. The example records dependency statuses and actual waits,
requires two waits of at least one second, and returns a controlled 503 after
three failed attempts. Positive match evidence proves the fault was exercised.
After explicit clear, the first attempt must succeed again. Reports record the
fault parameter and HTTP attempt-count assertions; command logs contain timing
observations. Faults and developer workloads are cleaned up on failure or cancel.

The caller has a five-second total deadline, including delays and response
reading, and stops waiting when its HTTP client disconnects. It supports only
bounded delta-seconds headers, and returns a controlled error for missing or
invalid `Retry-After` values. This is an example retry policy; applications should
choose attempt and deadline budgets suitable for their own operations.

Portable checks, without a fleet:

```sh
node --test test/retry.test.mjs
```

These use an actual HTTP dependency and test retry timing, bounded attempts,
success during retries, explicit recovery, invalid headers, and deadlines.
They do not qualify the deployed gateway; native execution requires a fleet
running the new HTTP fault contract.

## Malformed and truncated response bodies

The separate body-fault suite exercises HTTP 200 responses that cannot be used:

```sh
gregale test --manifest gregale-body-test.yaml --suite http-body --validate
gregale test --manifest gregale-body-test.yaml --suite http-body --preflight
gregale test --manifest gregale-body-test.yaml --suite http-body \
  --max-workload-minutes 20 --report bodies.json --html bodies.html --junit bodies.xml
```

`http_malformed_json` returns an incomplete JSON prefix as a complete entity.
`http_truncated_body` declares the length of complete JSON but ends the entity
early, causing a body-read error. Both use fixed small bodies, synthesize 200,
and bypass inventory execution. They do not inject bodies into HEAD responses
or count HEAD requests as fault matches. Updated API and gateway deployments
are required.

The caller retries read-only GET requests with a short backoff, stops after
three attempts, and returns `invalid_dependency_response` with HTTP 503. Timing
and the five-second deadline include body reading; a response that never ends
must be canceled without starting another attempt. Each suite case checks
positive fault matches and first-attempt success after explicit clear. The
portable suite also checks success during retries and both error types.

These synthetic faults do not execute an upstream write, so they do not prove
write idempotency or reproduce a committed write with a lost response. Use an
idempotency contract before retrying writes; the separate checkout lost-ack
example verifies that business outcome. The `body_errors` observations report
JSON parse failures as `malformed_json` and transport failures while reading as
`body_read_failed`; the latter is not limited to truncation.
