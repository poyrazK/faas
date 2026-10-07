# Customer Workflow Operations native qualification

The fixture and blocking CI lane are implemented. Native execution and leak
qualification are **pending**: this implementation workspace has no `/dev/kvm`.
Production workflow admission remains closed under ADR-647. See
[ADR-652](../adr/652-native-customer-workflow-operation-qualification.md).

Run the manual `e2e-native` workflow with
`lane: customer-workflow-operations-only` on the designated native x86_64 Linux
KVM acceptance host. Use an exact committed source archive and the pinned
toolchain. The existing runner stages guest-init, probes PostgreSQL, verifies
the host designation, takes the acceptance lock, quiesces services and restores
them on exit. It requires KVM, root and enabled PostgreSQL tests, and runs
preflight/final native leak checks. The dedicated lane has a 35-minute Go test
budget inside a 40-minute systemd budget.

| Scenario | Required evidence |
|---|---|
| `TestCustomerWorkflowOperationResultMetal` | Equivalent customer submissions converge and changed input conflicts. A real guest executes collect/transform/finish, with durable final-step progress and typed output. A lost upload acknowledgement resolves through lookup after private retention, with one byte transfer. Pending files are not downloadable. Confirmed success exposes the original bytes only to their customer; a signed webhook's recorded 503 and subsequent delivery retry leave business success and native attempt counts unchanged. Closed proof rejects lookup and upload. |
| `TestCustomerWorkflowOperationRecoveryMetal` | Real guest process death after private retention and before its final reply enters reconciliation. Restarting apid and schedd preserves that ledger and receipt. A newer default deployment and closed new-work admission do not change accepted code or prevent evidenced recovery. Replaying the same inspection-fenced recovery ID returns the same decision. Generation 2 retains the same workflow run, original version and artifact ID, executes only finish again and performs no second byte transfer. Old and closed resumed proofs are rejected. |
| `TestCustomerWorkflowOperationCancellationMetal` | Cancellation while schedd is stopped dispatches no queued business work. Cancellation while finish holds a private file stops the native action, leaves its uncertainty requiring reconciliation, and publishes no result. The retained file remains private and closed proof rejects file work. |
| `TestCustomerWorkflowOperationDeadlineMetal` | A holding final action observes its fixed native deadline and stops without a release or cancellation injection. The interrupted outcome requires reconciliation, no result is published, and closed proof rejects file work. |
| `TestCustomerWorkflowOperationOwnerRevocationMetal` | Suspending the customer while finish holds a private file interrupts native work. Account inspection retains the uncertain outcome, customer access is denied, and no artifact is published. Old native proof cannot look up or upload files. |

The full native suite also selects these scenarios in its blocking wake phase.
Every top-level test in the scenario file is required by the dedicated lane;
a skip, failure or absent PASS receipt fails its runner and final CI verdict.
Portable script tests verify selection grows automatically when another scenario
is added and reject each selected scenario's missing/skipped/failed receipt.

The fixture uses real app deployment, VM lifecycle and workflow dispatch. The
guest receives native proof from dispatch and obtains its workload JWT from
guest-init's loopback identity endpoint, backed by vmmd's signing key. Neither
the guest nor the relay mints authority. Its **test-only per-instance vsock relay**
forwards requests to loopback apid and injects the lost response only after the
real API retains the file. The private barrier records action entry and controls
fault timing; it never records successful steps or settles an operation.

Customer intent and healthy object-storage accounting are seeded; no managed
source bucket or provider writer is used. Private result bytes use the acceptance
host's configured artifact backend, preserving access to staged base images and
scan sidecars. The recovery prefix is pure. Reusing it does not establish that an
arbitrary external effect is safe to repeat. Daemon restart is tested after the
uncertain action is retained, rather than during a live transfer. Public DNS/TLS,
the production guest-to-API network path, provider failure behavior and fleet
activation/rollback remain separate qualification gates.

Each scenario deletes its app through the API, waits for native instances to
drain, closes relays and stops the real daemon owners before deleting its own
retained result keys. It runs native leakcheck; the enclosing runner additionally
checks namespaces/devices, jail directories, cgroups, Firecracker processes,
native mounts and loop attachments. Retain the exact source SHA, daemon/guest-init
binary hashes, Go/Firecracker versions, migration state, all five PASS receipts,
combined log, preflight/final leak receipts and service-restoration result.
Inspect failures before rerunning. Compilation or a skipped metal test is not
native acceptance.

Portable checks:

```sh
go test -race -count=1 ./pkg/e2etest/testdata/jobfixture
go test -race -count=1 ./pkg/e2etest -run 'Test(JobImage|CustomerWorkflowImage)'
go test -count=1 ./cmd/e2e -run 'Test(EveryPhase|SourceBuildingPhases|JobTimeout|SmokeLane|FullLane)'
bash scripts/ci/native-e2e-phases_test.sh
bash scripts/ci/run-native-e2e_test.sh
```

The Operations SDK acceptance CI job explicitly runs guest behavior tests under
`testdata`. Compile the native package on a sufficiently provisioned development
machine with `go test -tags metal ./cmd/e2e -run '^$'`; this executes no scenarios.
