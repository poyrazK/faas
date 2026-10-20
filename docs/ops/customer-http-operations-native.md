# Customer HTTP Operations native qualification

Ordinary HTTP Operations are the default execution kind and the first one a
preview cohort admits. This lane qualifies them on a real guest: deployment,
VM lifecycle, scheduler claim and renewal, guest workload identity, private
file retention, settlement, recovery and cleanup. Production admission remains
closed until its PASS receipts and the separate gates below are recorded. See
[ADR-521](../adr/521-customer-operations.md#native-http-qualification-and-pilot-window--2026-10-13).

Run the manual `e2e-native` workflow with `lane: customer-http-operations-only`
on the designated native x86_64 Linux KVM acceptance host, or run the same
selection through `scripts/ci/run-native-e2e.sh` with
`FAAS_E2E_PHASE=customer-http-operations-only`. Use an exact committed source
archive and the pinned toolchain. The runner stages guest-init, probes
PostgreSQL, verifies the host designation, takes the acceptance lock, quiesces
services and restores them on exit. It requires KVM, root and enabled
PostgreSQL tests, and runs preflight/final native leak checks. The lane has a
35-minute Go test budget inside a 40-minute systemd budget.

| Scenario | Required evidence |
|---|---|
| `TestCustomerHTTPOperationResultMetal` | Equivalent submissions converge and changed input conflicts. A real guest reads its claim control, reports two ordered progress stages and uploads a private file. A lost upload acknowledgement resolves through receipt lookup with one byte transfer. The pending file is not downloadable. The ordered history shows admission, claim, progress and the attachment. Confirmed success exposes the typed result and original bytes only to their customer. A signed webhook's recorded 503 and retry leave business success unchanged and never re-enter the handler. The old claim cannot upload, look up or report after settlement. |
| `TestCustomerHTTPOperationRecoveryMetal` | Real guest process death after private retention and before the reply records `requires_reconciliation` with `execution_outcome_unknown`. Killing and restarting apid and schedd preserves the ledger, invocation and retained copy. A newer default deployment and closed new-work admission do not change the pinned code or block evidenced recovery. Replaying the same recovery ID returns the same decision. Generation 2 runs a fresh invocation on the original deployment and publishes its own file; the uncertain generation-1 copy is never published. Old and generation-2 claims are fenced afterwards. |
| `TestCustomerHTTPOperationCancellationMetal` | Cancellation while schedd is stopped dispatches no business work. Cancellation while the handler holds a private file is observed through claim control; the uncertain outcome requires reconciliation, publishes no result and fences the old claim. |
| `TestCustomerHTTPOperationOwnerRevocationMetal` | Suspending the customer while the handler holds a private file immediately denies customer reads, new submissions and downloads. The admitted handler is not interrupted: its settled outcome and bytes remain available to the account. |
| `TestCustomerHTTPOperationWebhookAdmissionMetal` | A Stripe-signed provider event on an operation-bound endpoint (ADR-1193) admits one Operation for the bound customer. The real guest receives the webhook envelope as input and the Operation succeeds with its typed result. The provider's retry returns the original receipt as a duplicate and never re-enters the handler. |
| `TestCustomerHTTPOperationDeadlineCutMetal` | A definition with `deadline_seconds: 20` (ADR-1196) cuts the real guest's running attempt at the deadline; the dispatched attempt needs reconciliation. A `safe_to_retry` recovery is refused with 409, and recording an outcome still works. The ADR-20261016123221 telemetry store query and schedd collector read the reconciliation from the real run. |

HTTP and native workflow semantics differ deliberately in two places. A
generation-2 HTTP recovery is a new invocation, so its file receipt is fresh and
its bytes are transferred again; workflow recovery reuses the final-step receipt.
Customer suspension revokes HTTP customer access but does not interrupt an
admitted HTTP handler, while it interrupts native workflow attempts.

The full native suite also selects these scenarios in its wake phase. Every
top-level test in the `customer_http_operations*_metal_test.go` files is
required by the dedicated lane (ADR-20261017102231 Stage 1); a skip,
failure or absent PASS receipt fails its runner and final CI verdict. Portable
script tests verify that the selection grows when a scenario is added.

The fixture uses real app deployment, VM lifecycle and the scheduler's durable
invocation drain. The guest receives its claim from the synthetic adapter and
obtains its workload JWT from guest-init's loopback identity endpoint, backed by
vmmd's signing key. Neither the guest nor the relay mints authority. A
**test-only per-instance vsock relay** forwards the four HTTP runtime routes to
loopback apid and injects the lost upload response only after the real API
retains the file. Its barrier records handler entry and holds, releases or
crashes the handler; it never records success or settles an operation.

Customer intent and healthy object-storage accounting are seeded; no managed
source bucket or provider writer is used. Private result bytes use the
acceptance host's configured artifact backend. The fixture's handler has no
external effect, so its `safe_to_retry` evidence does not establish that an
arbitrary handler is safe to repeat.

## Not covered by this lane

- Plan deadline expiry. The admitted HTTP deadline is the plan's async
  invocation deadline (one hour on Hobby, six on Pro); the portable
  `TestPgOperationExpiredDeadlineFencesRuntime` covers its fence. A definition
  deadline is covered by `TestCustomerHTTPOperationDeadlineCutMetal`.
- The schedd and apid `/metrics` listeners: the harness binds them to a random
  port, so the deadline scenario reads the same Postgres snapshot and collector
  that schedd serves rather than scraping it.
- `transaction_receipt: postgres_v1` replay through a guest database
  connection. Definitions that use receipts still need
  `make test-customer-operation-sdk` and a separate native receipt scenario.
- Public DNS/TLS and gateway ingress of declared operation routes, store
  outages, and fleet activation and rollback.

Each scenario deletes its app through the API, waits for native instances to
drain, closes relays and stops the real daemon owners before deleting every
result copy the ledger recorded for its account, including superseded
generations. It runs native leakcheck; the enclosing runner additionally checks
namespaces/devices, jail directories, cgroups, Firecracker processes, native
mounts and loop attachments. Retain the exact source SHA, daemon/guest-init
binary hashes, Go/Firecracker versions, migration state, all four PASS
receipts, the combined log, preflight/final leak receipts and the
service-restoration result. Inspect failures before rerunning. Compilation or a
skipped metal test is not native acceptance.

Portable checks:

```sh
go test -race -count=1 ./pkg/e2etest/testdata/jobfixture
go test -count=1 ./pkg/e2etest -run 'Test(JobImage|CustomerWorkflowImage|CustomerHTTPImage)'
go test -count=1 ./cmd/e2e -run 'Test(EveryPhase|SourceBuildingPhases|JobTimeout|SmokeLane|FullLane)'
bash scripts/ci/native-e2e-phases_test.sh
bash scripts/ci/run-native-e2e_test.sh
```

Compile the native package with `GOOS=linux go vet -tags metal ./cmd/e2e`; this
executes no scenarios.
