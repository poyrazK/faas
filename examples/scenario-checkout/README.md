# Checkout resilience starter

Prove a business outcome through a dependency outage: checkout returns a
controlled error, catalog stays available, and retrying after recovery creates
exactly one order.

The sample accepts `{customer, sku, quantity}` order payloads. Empty-body requests
retain the original default demo item. Its stateful private TCP payment fixture commits
one simulated charge per idempotency key **before** acknowledging it. Checkout
persists intent before contacting payment and marks the order confirmed only
after an acknowledgement. When acknowledgement is lost, a 503 reports
`payment_outcome: unknown`; retrying with the same key reconciles that outcome.
No real payments are processed. `/stats` is test-only.

The Gregale manifest stores orders in the surviving payment fixture through
`ORDER_LEDGER_URL`, authenticated with a run-scoped `ORDER_LEDGER_TOKEN`. Checkout
replacement can then discard its local filesystem without losing order intent.
The fixture has separate order and charge ledgers; combining them in one fixture
keeps this example small rather than prescribing a production architecture.

For portable process tests, checkout can instead use its own local file ledger.
`ORDER_LEDGER_PATH` and `PAYMENT_LEDGER_PATH` select files; defaults are
`/tmp/checkout-orders.json`, `/tmp/payment-orders.json` in the fixture, and
`/tmp/payment-charges.json`. Each version-3 ledger uses atomic replacement,
file fsync, directory fsync, and a persisted identity. Invalid or older-format
ledgers fail startup. This single-writer demo does not provide replicated
storage, support multiple writers, or preserve files after the fixture's VM is
replaced. Use transactional shared storage and the real provider's idempotency
contract in production. Both deployable source directories include the ledger
module so they can be packaged independently.

## Run on Gregale

Requires Node 22+, a logged-in Gregale CLI, capacity for two developer workloads,
and compute nodes with private TCP routing and TCP faults enabled. Run from this
directory:

```sh
gregale test --suite checkout --validate
gregale test --suite checkout --preflight
gregale test --suite checkout --max-workload-minutes 20 \
  --report results.json --html results.html --junit results.xml
```

The scenario starts warm, then cold-replaces checkout through five stages:

1. **Baseline:** concurrent checkout requests with the same idempotency key
   produce one confirmed order and one payment attempt. Native HTTP load checks
   status, order ID, confirmation, and a two-second p95 budget.
2. **Payment outage:** a 100% downstream `tcp_timeout` suppresses payment
   acknowledgements. Checkout must return JSON `payment_unavailable` with HTTP
   503 within two seconds, make exactly two payment attempts, and confirm no
   order. The payment fixture must have committed just one additional charge
   despite those retries; the application retains its pending intent. Each
   attempt has a 350 ms deadline including connection establishment.
   While checkout is pending, catalog must still succeed within 500 ms.
   `min_matches: 1` requires evidence that traffic exercised the fault.
3. **Restart checkout:** the native `restart` step submits one fresh restart,
   waits for its completion receipt and catalog readiness, and verifies the
   captured ledger identity, baseline order count, and pending failed order
   both before and after replacement. Its one-minute timeout covers the entire
   restart stage. Lost storage fails the stage; payment stays running.
4. **Restore payment:** Gregale explicitly clears the fault.
5. **Recovery:** checkout must succeed within ten seconds. Concurrent replays of
   the failed checkout key must return the same order without further payment
   attempts or charges. Native load then checks confirmation, exactly two total orders,
   zero pending operations, and a two-second p95 budget.

The repository-owned commands assert timing and application counters using
Node's assertions. A failed command fails its Gregale stage. Native JSON
assertions and load budgets appear in terminal, JSON, HTML, and JUnit reports;
commands print outage and recovery observations. Commands run from `app`, so
their paths deliberately use `../test/acceptance.mjs`.

Timing budgets assume a responsive test environment. The fault lease expires
after two minutes as a backstop, and Gregale cleans up faults and workloads after
failed assertions or cancellation. The ten-second command budget covers the
recovery probe after clear; it is not an end-to-end platform recovery metric.

## Concurrent customer batch

The separate batch manifest exercises six distinct orders with three concurrent
requests per idempotency key. It verifies each customer's payload in both the
order and charge ledgers, not just total counts. Keys are bound atomically to a
canonical payload fingerprint before payment. Changing customer, SKU, or
quantity returns HTTP 409 during a pending outage and after recovery/restart.
Reordering JSON fields retains the same identity. Invalid fields, quantities,
and oversized payloads are rejected before payment.

```sh
gregale test --manifest gregale-batch-test.yaml --suite checkout-batch --validate
gregale test --manifest gregale-batch-test.yaml --suite checkout-batch --preflight
gregale test --manifest gregale-batch-test.yaml --suite checkout-batch \
  --max-workload-minutes 20 --report batch.json --html batch.html --junit batch.xml
```

The batch starts with one baseline order, then loses acknowledgements for six
new customers. It expects twelve payment attempts across eighteen simultaneous
HTTP calls, one charge per key, and six persistent pending intents. It verifies
conflicting payloads create no additional payment attempts. Native restart
checks preserve the ledger identity and all intents. Recovery confirms seven
total orders and charges, validates each payload, and verifies cached replay
makes no payment attempt. Use an acceptance account whose HTTP concurrency
capacity and latency budget accommodate the eighteen-request burst.

Portable tests separately SIGKILL checkout while all six charged operations
are still in flight, recover from an empty local filesystem through the external
ledger, then restart both processes and verify their persisted payload bindings.
The native batch uses managed fresh replacement after the timeout responses;
it does not claim a timed in-flight SIGKILL. The dedicated native acceptance
command continues to qualify the original single-order scenario. The batch
manifest requires its own native execution and is not certified by portable
tests. Customer strings are demo payload fields, not authenticated identities.

## Portable verification

For actual VM qualification, run the
[native restart acceptance command](../../docs/scenario-restart-native-acceptance.md)
from the repository root. It checks success, failure, cancellation, matching
reports, and API cleanup on a designated native fleet; it is not claimed by the
portable tests below.

```sh
node --test test/checkout.test.mjs test/durable.test.mjs test/batch.test.mjs test/native-probe.test.mjs
```

This starts the sample and a loopback TCP dependency, suppresses downstream
responses, and runs the exact assertion commands from their deployment working
directory. It also proves that the outage assertion rejects an unapplied fault.
The durable tests also launch the actual checkout and payment processes with
temporary ledger files. A TCP proxy drops acknowledgements after payment has
committed. Tests kill checkout and payment with SIGKILL, restart them with those
files, and verify concurrent retries create exactly one charge and one order.
Another test kills checkout while the charged request is still in flight; a
confirmed-order restart verifies that later duplicates never contact payment.
Corrupt ledgers must fail startup rather than silently lose idempotency state.

Portable tests require no account or KVM. They verify process crash recovery,
application behavior, and commands. The external-ledger test also starts each
replacement checkout with a different, empty local ledger path and checks the
same persisted identity and pending intent. This models discarding local state.
The `real-vm` manifest uses Gregale's actual fresh restart operation and records
its receipt, readiness, storage checks, proxy evidence, and cleanup. Native VM
acceptance still requires running that manifest on a configured Gregale fleet.

To adapt it, replace the fixture exchange with your dependency client, use a
transactional order and idempotency ledger, and change JSON expectations to match your
business contract. Keep the fault-match requirement and test an independent
endpoint while checkout is in flight.

## Native acceptance gate

From the repository root, `make native-scenario-restart-acceptance` runs eight
cases: the original checkout success/failure/cancellation, the corresponding
six-customer batch cases, and healthy/stuck pending-request recovery controls.
Every executed case must prove cleanup using the API. The native batch records
per-customer payload and state assertions in the reports before and after
replacement and recovery.

The separate `gregale-pending-test.yaml` uses `native-probe` for a healthy
control. The native harness generates its stalled variant with a 15-second
delay and requires a failure specifically caused by overdue HTTP requests.
Neither fixture adds test routes to the checkout application.

See [native acceptance prerequisites and evidence](../../docs/scenario-restart-native-acceptance.md).
`make test-scenario-restart-acceptance` runs portable harness contracts, Node
fixtures, and Gregale manifest validation; it does not qualify a native fleet.

### Reusable TCP attempt assertions

`gregale-connection-assertions-test.yaml` uses the existing probe and echo
fixtures to assert a maximum average of two TCP connection attempts per named
incoming request during an outage. It also reuses healthy-route latency and
sustained recovery profiles. The TCP budget requires the gateway's dedicated
attempt counter; ordinary fault matches cannot replace it.

```sh
gregale test --manifest examples/scenario-checkout/gregale-connection-assertions-test.yaml --suite connection-assertions --validate
```

Execution requires a native test environment with TCP attempt evidence support.
See [reusable dependency-attempt limits](../../docs/scenario-tests.md#reusable-dependency-attempt-limits)
for sample requirements, attribution and report details.

`gregale-tcp-campaign-test.yaml` executes latency, bandwidth, timeout and reset
in declaration order against the bounded echo dependency. It combines connection
attempt limits, healthy-route checks and timed recovery after every fault. Run
`gregale test --manifest examples/scenario-checkout/gregale-tcp-campaign-test.yaml
--suite tcp-campaign --validate` from the repository root before a real-VM run.
The opt-in 16 KiB payload makes bandwidth faults meaningful; ordinary probes keep
their smaller payload. Reports identify each fault and the first failed cycle.

The TCP campaign now checks `/connections` during every recovery and again after
load or cancellation. It permits two in-flight connections during recovery, then
requires the original baseline after workers drain. The endpoint reports a stable
instance UUID and tracks actual outgoing TCP sockets until their close events;
cancelled incoming requests close their dependency probes. This observation
endpoint supplies application counts rather than a platform-wide socket census.

`gregale-pool-exhaustion-test.yaml` exercises a fixed two-permit TCP connection
pool with two queued slots. During a dependency stall, queue waits expire after
200 ms while health remains responsive. Recovery checks successful throughput;
post-drain checks require all permits, sockets, waiters and timers to retire.
Run `gregale test --manifest examples/scenario-checkout/gregale-pool-exhaustion-test.yaml
--suite pool-exhaustion --validate` from the repository root. This fixture returns
connections after each attempt and does not simulate idle reuse or a database
transaction protocol.

`gregale-request-deadlines-test.yaml` checks a complete application timeout
contract, rather than treating a client abort as a passing timeout. The sealed
fixture has one 500 ms application timer spanning pool acquisition and dependency
I/O, joins its socket before returning 504, and is observed with a 750 ms client
budget. Healthy routes and final socket cleanup are checked separately.


`gregale-pool-cancel-test.yaml` is a native-gate fixture for cancelling four live
dependency requests with two permits and two queued callers. It requires the
harness nonce, private proof path and live checkpoint reinspection; run it via
`make native-scenario-restart-acceptance` on the designated KVM host. Its fixed
10-second queue and 30-second attempt deadlines are acceptance controls. Cleanup
must prove disconnect-driven retirement before VM teardown, not natural expiry.
