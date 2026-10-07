# Customer Job Operations native qualification

The code and dedicated lane are implemented. Native execution and leak
qualification are **pending**: the implementation workspace has no `/dev/kvm`.
This guide does not authorize a production cohort. ADR-602, ADR-603 and
ADR-604 retain closed admission until the required evidence is available.

Run the manual `e2e-native` workflow with
`lane: customer-job-operations-only` on the designated native x86_64 Linux KVM
acceptance host. Use an exact committed source archive and the workflow's
pinned toolchain. The existing runner stages guest-init, probes PostgreSQL,
checks the host designation, takes the acceptance lock, quiesces and restores
services, and runs preflight/final leak checks. It refuses an unavailable KVM
device or disabled PostgreSQL tests. The lane has a 35-minute Go test budget
inside a 40-minute systemd budget.

| Scenario | Required evidence |
|---|---|
| `TestCustomerJobOperationDirectUploadMetal` | A real guest uploads bytes directly with its live task capability. Lost acknowledgement resolves through its durable receipt without a managed source read. Files stay private before native exit and download only for their customer after success; notification failure leaves the result succeeded. Closed task proofs are rejected and native instances are removed. |
| `TestCustomerJobOperationResultMetal` | Scoped duplicate submissions converge; changed input conflicts. A real guest reports progress and prepares a typed result/private file. Losing the file acknowledgement and removing its source still permits receipt lookup. Customers cannot download the prepared file before native exit. After success, its private copy remains downloadable only by its owner. A signed completion webhook fails, retries and succeeds without another business execution. Completed native proofs are rejected. |
| `TestCustomerJobOperationRestartMetal` | apid and schedd die while a real guest holds its prepared result. Restart preserves the operation/progress/private file and one native attempt. Removing the Job admission grant leaves accepted work able to complete. |
| `TestCustomerJobOperationRecoveryMetal` | The guest prepares a private result and exits unsuccessfully. Business state requires reconciliation; private output is unpublished and no automatic retry occurs. Evidence-backed, inspection-fenced recovery creates exactly one new run under generation 2, including replay of the same recovery ID. Editing the Job command does not change the retained retry snapshot. Closing new admission does not block recovery; generation 1 proofs stay invalid and the uncertain file is replaced with a freshly prepared generation 2 copy. |
| `TestCustomerJobOperationCancellationMetal` | Queued cancellation starts no task. After native progress, cooperative cancellation stops the guest and leaves the started outcome requiring reconciliation. Neither path leaks an active Job instance. |

Every scenario stops its real daemon owners, removes its own retained result
files and runs the native leak checker. The enclosing runner also checks
network namespaces/devices, jail directories, cgroups, Firecracker processes,
native mounts and loop attachments. A skipped, failing or absent selected test
fails the lane; adding a scenario to the file automatically adds a requirement.
The full suite also includes these tests in its existing Jobs phase.

The guest-to-apid HTTP leg uses a **test-only per-instance vsock relay**.
Production routes validate the actual scheduler proof against the current task
lease. The relay only transports requests and injects a lost acknowledgement;
it never reports native task completion. Customer intent is seeded and the
source CSV comes from a local S3 HTTP fixture. Platform private result storage
uses the acceptance host's configured artifact backend. This covers neither
public DNS/TLS ingress nor a real provider's upload and failure behavior. Those
checks and fleet rollback qualification remain separate activation requirements.

Retain the exact source SHA, daemon/guest-init binary hashes, Go/Firecracker
versions, applied migration state, all five PASS receipts, combined test log,
preflight/final leak receipts and service-restoration result. Inspect a failure
before rerunning; a metal build or an ordinary `go test` skip is not acceptance.

Portable checks, also suitable for development without KVM:

```sh
go test -race -count=1 ./pkg/e2etest/testdata/jobfixture
go test -race -count=1 ./pkg/e2etest -run 'Test(HarnessAPIDConfig|JobImage)'
bash scripts/ci/native-e2e-phases_test.sh
bash scripts/ci/run-native-e2e_test.sh
```

The guest fixture lives under `testdata`, so ordinary `go test ./...` does not
execute its behavior tests. The Operations SDK acceptance CI job runs them
explicitly. Compile the metal package on a sufficiently provisioned development
machine with `go test -tags metal ./cmd/e2e -run '^$'`; that command deliberately
executes no native scenarios.
