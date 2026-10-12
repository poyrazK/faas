# Checkout resilience starter

Prove a business outcome through a dependency outage: checkout returns a
controlled error, catalog stays available, and retrying after recovery creates
exactly one order.

The sample sells one fixed item. Its private TCP echo service stands in for a
payment acknowledgement; it does not process payments. Orders and in-flight
idempotency keys live in memory. This demonstrates same-process resilience,
not durable idempotency across restarts, payment-side deduplication, or recovery
from an ambiguous real charge. A production integration needs a persistent
ledger and the payment provider's idempotency contract. `/stats` is test-only.

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

The warm scenario keeps the same application process through four stages:

1. **Baseline:** concurrent checkout requests with the same idempotency key
   produce one confirmed order and one payment attempt. Native HTTP load checks
   status, order ID, confirmation, and a two-second p95 budget.
2. **Payment outage:** a 100% downstream `tcp_timeout` suppresses payment
   acknowledgements. Checkout must return JSON `payment_unavailable` with HTTP
   503 within two seconds, make exactly two payment attempts, and create no
   order. Each attempt has a 350 ms deadline including connection establishment.
   While checkout is pending, catalog must still succeed within 500 ms.
   `min_matches: 1` requires evidence that traffic exercised the fault.
3. **Restore payment:** Gregale explicitly clears the fault.
4. **Recovery:** checkout must succeed within ten seconds. Concurrent replays of
   the failed checkout key must return the same order without further payment
   attempts. Native load then checks confirmation, exactly two total orders,
   zero pending operations, and a two-second p95 budget.

The repository-owned commands assert timing and application counters using
Node's assertions. A failed command fails its Gregale stage. Native JSON
assertions and load budgets appear in terminal, JSON, HTML, and JUnit reports;
commands print outage and recovery observations. Commands run from `app`, so
their paths deliberately use `../test/acceptance.mjs`.

Timing budgets assume a responsive test environment. The fault lease expires
after one minute as a backstop, and Gregale cleans up faults and workloads after
failed assertions or cancellation. The ten-second command budget covers the
recovery probe after clear; it is not an end-to-end platform recovery metric.

## Portable verification

```sh
node --test test/checkout.test.mjs
```

This starts the sample and a loopback TCP dependency, suppresses downstream
responses, and runs the exact assertion commands from their deployment working
directory. It also proves that the outage assertion rejects an unapplied fault.
It requires no account or KVM. It verifies application behavior and the commands;
the `real-vm` scenario separately verifies Gregale's proxy, fault-match evidence,
and workload cleanup.

To adapt it, replace the echo exchange with your dependency client, persist the
order and idempotency ledger, and change the JSON expectations to match your
business contract. Keep the fault-match requirement and test an independent
endpoint while checkout is in flight.
