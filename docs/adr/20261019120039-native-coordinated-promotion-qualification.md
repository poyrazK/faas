# ADR-20261019120039: Native coordinated promotion qualification

## Status

Implemented locally. Native KVM execution and host leakcheck remain pending;
production admission remains disabled.

## Context

Portable checks establish the coordinated promotion and replacement-resolution
state machines, but cannot prove SQL execution, real queue delivery, private
frontend/API routing, executor teardown or Firecracker cleanup together. Those
behaviours need executable qualification bound to the platform source revision.

## Decision

Add optional `qualify_coordinated` to the existing explicit promotion workflow.
It retains the six base partitions, includes their optional service-graph
prerequisite, and adds a serialized `promotion-coordinated` partition with a
330-minute test budget, 335-minute systemd envelope and 360-minute job envelope.
Use the guarded isolated SQL proxy and private platform database, real builder
VMs, ordinary daemon-owned evidence, and the existing final host leakcheck.

The exact inventory is fixture metadata plus three native scenarios:

- A frontend/API/worker promotion expands destination SQL exactly once, keeps
  the candidate worker stopped behind the command-result barrier, then proves
  private HTTPS routing and destination secrets, startup recovery, full graph
  publication, fresh delivery windows and a baseline retry across cutover.
- The same graph restores all application members and destination configuration
  after actual worker delivery failures. The expanded schema and original task
  receipt remain intact; retained retries keep their first-admission versions.
- Loss of a running migration owner leaves a real uncertain SQL result. Build
  corrected frontend/API/worker artifacts without a migration, verify the repair,
  request explicit operator resolution and confirm exact executor teardown,
  SQL-session disappearance, fresh worker ledger windows and immutable recovery
  receipts across restart/replay while new-admission flags are disabled.

The API alone owns the release command and delivered SQL credentials. Read-only
critical/dependency probes check customer data without running migrations. Faults
target owned guest/process behaviour; tests never seed runtime proofs, delivery
samples, task outcomes, controller timestamps or promotion publications.

The source/log-bound supplemental report uses scope
`project-environment-promotion-only/promotion-coordinated`. Its parser requires
all four attributed tests, distinct admissions/tasks/SQL runs, exact SQL counts,
private graph/runtime identities, retained job pins, real verification windows,
replacement evidence and clean final completion. Missing/skipped/duplicate tests,
wrong source, mixed versions or failed cleanup cannot qualify the report.

Worker and coordinated approvals require this report at
`capability_reports.coordinated`. Bind `worker-replacement-resolve` and
`coordinated-migration-workers` receipts to its log digest; the operator signer
requires `--coordinated-log` and validates its bytes. Worker-only admission needs
the report because it exposes worker replacement recovery, but this evidence does
not add migration admission to its approved scope. Basic HTTP and independent
migration approval requirements remain unchanged. No approval, key or production
flag is generated or installed by CI.

## Validation and limits

Portable checks cover fixture scanning, tagged compilation, guest dependency SQL
refusal, strict negative report cases, source/log signing and scoped readiness,
partition selection, guarded SQL setup, budgets, aggregation and artifact wiring.
These do not establish a native pass. The remaining variants in the native
runbooks still require actual reviewed evidence; this suite does not turn their
unexecuted cases into passed receipts. The designated KVM run and leakcheck are
required before approval or production enablement.
