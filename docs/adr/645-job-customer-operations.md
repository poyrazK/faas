# ADR-645: Single-task batch Jobs as Customer Operations

Status: implemented in closed-admission preview; native qualification pending.

An application contract can name an account-owned batch Job using `job`. HTTP ingress, ownership, submission receipts, progress reads and completion delivery retain the Customer Operations contract. The native Job ledger remains authoritative for execution. `job`, `workflow` and `transaction_receipt` are mutually exclusive. Job definitions require POST and reconciliation recovery.

Admission commits one operation, one manually triggered run with task zero, an immutable execution association and the scoped idempotency receipt together. Input, image reference/digest/storage key, command, effective environment, RAM and timeout are captured. Each generation has one execution attempt and zero automatic retries. Configuration edits apply to later submissions. The image remains protected from imaged cleanup while the retained operation exists. Ordinary Jobs retain their existing active-task edit restrictions.

Jobs have no application workload JWT. Schedd derives a domain-separated runtime capability from the unpredictable current task lease, run and instance. The guest receives that capability, never the host lease token. Dedicated runtime routes require run, instance, generation and attempt; every store mutation rechecks the current unexpired task claim. The API additionally checks the active owner and Job instance. HTTP/workflow proofs and account bearer tokens cannot substitute. Capabilities are absent from customer responses and recovery inspection and redact under formatting/structured logging.

Progress uses the existing immutable schema/stage, monotonicity, report rate and count limits. A typed result receipt is private and immutable until a current lease-fenced successful exit commits business success and the completion outbox together. A successful exit without a receipt, task failure, expired claim or interrupted claimed task enters reconciliation. Notification failure never changes business success.

Queued cancellation closes the task and operation. Running cancellation records cooperative intent: the SDK stops before business entry or another unit of work, and cannot prepare a new result afterward. Previously prepared results can still be confirmed by successful exit. Unknown external effects remain subject to reconciliation.

Account recovery requires evidence, the generation/inspection fence and an immutable recovery ID. `safe_to_retry` creates a fresh single-task run using the original snapshot and input, preserving the operation identity. Direct native retry/replay/requeue cannot reopen these runs. Explicit success requires a valid output result; it does not create work. Native Job attempts and recovery decisions remain inspectable with all generations retained. Host outcome settlement remains available after app tombstoning; native Jobs retain their image snapshot independently of app-code pins. Permanent app/account purge closes queued tasks and waits for leased execution to settle before removing the operation association. Busy task locks defer the purge, preserving the normal run/task/operation lock order.

Canonical Job input is capped at 32 KiB by the existing guest environment value protocol. Customer environment is capped at 240 entries to leave room for trusted scheduler fields. The fixed control deadline is claim start plus the captured task timeout and the existing 90-second boot/cleanup envelope; the current lease can shorten this budget. Control reads never renew it.

Typed result references and the verified private file receipts in ADR-646 use the Operation download contract. Native output-manifest artifacts remain separate Job artifacts. Native KVM execution, process cancellation and leakage acceptance must pass before admitting production Job Operations.

The native harness and blocking `customer-job-operations-only` lane are defined
in [ADR-648](648-customer-job-operations-native-qualification.md) and the
[qualification guide](../ops/customer-job-operations-native.md). Their dedicated
KVM execution receipts remain pending; implementing the lane does not enable
production admission.
