# ADR-608: Frozen runtime verification participants and durable recovery

Status: accepted · 2026-10-06

## Context

ADR-607 verifies current evidence for an explicitly reviewed gateway process set.
The caller can currently replace that set on the next observation, and a worker
restart loses verification progress. Activation completion alone must never be
reported as verification success or authorize predecessor cleanup.

## Decision

Add a private account-scoped verification journal, separate from the immutable
activation operation. `Controls.StartVerification` enrolls an already completed,
matching cutover and freezes its canonical sorted unique gateway UUIDs (1–64).
An exact replay returns the original journal, including terminal history. A
different set conflicts, even if smaller or composed of restarted processes.
For enrolled operations, `Controls.Verify` also requires this exact set. No
enrollment route, public Apply, automatic topology discovery or deployed worker
service is added. Reviewed identities are not an authoritative fleet registry.

Keep the cutover timestamp and a fixed deadline of cutover plus 30 minutes,
matching the existing gateway repair horizon. Enrollment, retries and process
restarts never move it. Late enrollment is claimable for expiry without reading
health. Persist pending reasons, reviewed and confirmed process counts,
observation/health times and conservative evidence validity in a bounded safe
JSON observation (8 KiB maximum). Keep lease tokens and internal scheduling
clocks out of private status and JSON. Clone treats the journal as operational.

The existing opt-in private apid worker polls both activation and verification.
An activation error does not starve verification. Each iteration claims at most
one row of each kind, each bounded by the existing 30-second lease/context.
Liveness allows both bounded steps plus existing polling/backoff. Claim uses
`SKIP LOCKED`, a fresh token and database time; a crashed worker leaves its lease
to expire. Pending work gets a fresh observation on the next due claim, with
the existing five-second poll and capped failure backoff. No notification or
in-memory checkpoint is required for recovery. Shutdown cancels either step.

Advance locks operation, verification journal, then the existing environment,
app, deployment/workload and target qualification fences. Evaluate the ADR-607
evidence in a repeatable-read transaction. Concurrent conflicting publications
produce a retryable transaction failure, not success. Gateway receipts and
health observations come from the same snapshot; evaluate freshness after reads.
Checkpoint rechecks the lease and evidence expiry with database wall-clock time.
An expired lease/evidence publishes nothing. Deadline expiry is checked again
at checkpoint, so lock waits cannot extend verification.

Missing/stale receipts, insufficient traffic and unavailable/unhealthy telemetry
stay `pending`. Changed activation/baseline/qualification and unsupported health
scope become `blocked`. The fixed horizon becomes `expired`. Only the full
fresh ADR-607 predicate can produce `verified`. Terminal journal evidence is
immutable and historical: subsequent rollback, revocation or receipt expiry
can invalidate fresh `Verify` while retaining the past checkpoint. It never
changes traffic or activation history, starts requests/VMs, rolls back, drains
connections, deletes artifacts or authorizes cleanup. Another deployment may
supersede activation; the pending journal then blocks on the existing fences.

The request-rate consistency lower bound follows integer truncation exactly:
`errors / (requests + 1) * 100`; preserve the existing upper bound for fractional
Prometheus increases.

## Consequences

Operators can review persistent progress and recover verification after process
failure without changing the reviewed identities or deadline. A restarted
gateway cannot be substituted into this operation; topology changes require a
subsequent explicitly designed membership/review workflow. Gateway fleet
membership authority, connection drain, predecessor retention and cleanup are
still separate work. Public `execution_available` remains false. Dedicated
Linux amd64 KVM end-to-end acceptance, `test-metal` and final `leakcheck` remain
required before customer enablement; synthetic local fixtures prove no VM behavior.

## Validation

Memory/PostgreSQL contracts cover account isolation, enrollment replay and set
immutability, restart recovery, exclusive claims, stale leases, persistent
pending progress, rollback blocking, fixed expiry and immutable verified history.
PostgreSQL contracts exercise success with synthetic scoped post-cutover health,
lease expiry while waiting on the app fence and database intent/terminal guards.
Worker contracts cover independent verification during activation failures,
lost advance responses, bounded contexts, cancellation and safe status.
