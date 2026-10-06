# Events & Delivery recovery acceptance

The repeatable gate publishes one event to two application consumers. One
returns 200; the other returns 503 until its normal handler retry budget is
exhausted. It follows the failed recipient's receipt action to replay only that
consumer, then proves recovery without another request to the successful
sibling. No invocation state is manually changed.

## Process gate

Run on Linux against a dedicated, reachable PostgreSQL 16 cluster. Daemon
capability checks require Linux `/proc`; the command refuses other hosts:

```bash
export DATABASE_URL='postgres://faas@127.0.0.1:5432/faas?sslmode=disable'
make event-delivery-acceptance
```

The command refuses missing/unreachable PostgreSQL, explicit skips, skipped
tests and missing test results. To retain the full Go JSON evidence, run
`bash scripts/ci/run-event-delivery-acceptance.sh /tmp/event-delivery.jsonl`.
The ordinary CI E2E sharder also discovers both tests automatically.

Both whole-receipt routing and independent recipient adoption run through real
apid, schedd and gateway processes and PostgreSQL. A VMMD protocol fixture
forwards requests to actual HTTP consumer applications; this gate does not
qualify Firecracker, guest boot or the native network transport.

The process scenarios check:

- accepted recipients and consumer aggregates appear in backlog while schedd
  is stopped;
- one sibling completes while the other has a retained, scheduled handler retry;
- a scheduler process crash/restart preserves retry state, including when
  independent adoption is disabled for the replacement scheduler;
- retry exhaustion creates an event-subscription dead letter through the normal
  handler failure path;
- the API process restarts before selective recovery;
- retrying the same replay request with the same idempotency key and publishing
  the same event again preserve invocation identities and successful sibling
  evidence;
- paginated attempt history retains the pre-replay failures and the successful
  new generation, receipt state converges, and settled recipients leave backlog;
- another account cannot read receipt or attempt evidence.

The HTTP fixture counts every delivery, failure and successful response. Its
in-memory effect deduplicates by source/event identity. These controlled counts
do not change Gregale's at-least-once execution or non-FIFO contract.

## Staging gate

Use a dedicated staging account and two fresh acceptance fixture apps. Build
from the repository root using
`examples/event-delivery-acceptance/Dockerfile` through Gregale's normal source
deployment flow. Reconcile the
`examples/event-delivery-acceptance/gregale.yaml` subscriptions into **both** apps.
The manifest must be selected explicitly because it lives below the build root.
Require exactly these two enabled consumers for this source/type; the gate
rejects unexpected captured recipients. The apps must allow the gate's HTTP
requests and keep one fixture process alive throughout the run: set
`min_instances=1`, `max_concurrency=1` and a long idle timeout, and avoid deploying
or restarting the fixture apps during a gate. The fixture ledger intentionally
lives in memory; only the platform API and scheduler are restarted.

Set the same fresh `GREGALE_EVENT_GATE_CONTROL_TOKEN` secret on both fixture apps
and in the operator shell. The fixture's `/__gate/events` and `/__gate/recover`
controls require that token. Never reuse the fixture as a business handler.
Control preparation rejects a reused event identity and caps its test ledger;
start fresh fixture processes for a new qualification session.

The operator API token needs permission to read apps, subscriptions, receipts,
history and backlog, publish events and replay DLQ deliveries. Supply tokens
through the environment, not command-line arguments:

```bash
# Export GREGALE_API_TOKEN and GREGALE_EVENT_GATE_CONTROL_TOKEN securely first.
go run ./scripts/ops/eventdelivery-gate \
  --api-url https://api.staging.example \
  --healthy-app delivery-good --failing-app delivery-bad \
  --healthy-url https://delivery-good.apps.staging.example \
  --failing-url https://delivery-bad.apps.staging.example \
  --routing-mode recipient --attempts 3 --timeout 3m \
  --restart-checkpoint > /tmp/event-delivery-staging.json
```

`--attempts` must match the effective handler budget for the staging apps. Hobby
uses three attempts by default; use the deployment's actual budget for other
plans or policies. Allow enough timeout for all retry delays plus the restart.
Each invocation generates a fresh event ID. URLs returned by receipts must stay
relative to the configured API, and the runner refuses HTTP redirects.

At the first retained pending handler retry, the runner writes a checkpoint to
stderr and waits. Restart the staging scheduler through its usual service
manager, then type `restarted` and press Enter. The receipt must remain readable
and both handlers must subsequently reach their expected states. A successful
JSON report declares `gate=pass` and `scheduler_restart=operator_confirmed`;
without the checkpoint flag it reports `scheduler_restart=not_exercised` and
does not qualify restart behavior. Record the running binary revisions and
service-manager restart evidence alongside this report.

First run with adoption disabled and `--routing-mode event`. After all API
writers and schedulers support recipient ownership, enable adoption **in
staging** and run with `--routing-mode recipient`. The process gate automatically
tests disabling adoption during recovery; repeat that operational step at the
checkpoint if qualifying a staging rollback. Neither runner changes flags or
deploys binaries. This is execution/recovery evidence for
[ADR-606](../adr/606-independent-event-recipient-routing.md); staging retention,
lease-expiry, concurrent operator and native runtime qualification remain
separate gates before production enablement.
