# ADR-1339: Disabled internal CI execution service

- Status: implemented behind default-off flags; native/provider qualification pending
- Date: 2026-10-15
- Extends: ADR-1573, ADR-1574, ADR-1336, ADR-1337 and ADR-1338

## Decision

Wire fresh internal Jobs execution admission and an apid-owned bounded dispatcher.
This completes the dormant adapter's service path. Existing blocked run receipts,
GitHub-trigger admission and qualification executions retain their own behavior;
none is promoted into native work. No public product surface or fleet enablement
is introduced. schedd owns native admission and instances; vmmd remains the only
native actor. Repository steps run only inside the existing Jobs VM boundary.

`POST /v1/internal/ci/executions` requires internal account policy, GitHub and
dispatch opt-in, the new exact-opt-in `FAAS_CI_WORKER_ENABLED`, transport opt-in,
a configured HTTPS origin and managed private storage. It checks current GitHub
installation/repository ownership, the immutable revision/SHA, and account-owned
materialized Jobs templates through existing `AdmitJobs`. A stable idempotency
key names these inputs. Admission is account-serialized and globally budgeted:
at most four active executions/account and sixteen globally, with the existing
32 retained Jobs executions/account ceiling. Replays precede capacity checks.
Limits live in `pkg/api/limits.go`.

Read/list, cancellation and bounded attempt log routes use the same account,
admin, MFA and HTTPS boundary. They remain accessible with new dispatch disabled
while internal access and the origin remain configured. Status omits raw logs and
private compute provenance; logs have a separate byte cursor and explicit
completion/truncation. Qualification IDs return 404 and lists contain Jobs mode
only. Artifact downloads retain the existing sealed-output and stop checks.
Operator CLI commands use the ordinary authenticated operator session. Artifact
files are published privately, atomically and without overwriting an existing
path only after size/SHA-256 verification against execution metadata.

## Dispatcher ownership and recovery

An append-only migration adds a short global accounting lock, a due timestamp
and a bounded failure counter. Four apid worker goroutines select due Jobs rows
with `FOR UPDATE SKIP LOCKED`. Under the accounting lock, no more than four
unexpired controller leases exist globally and two per account. Each claim
persists the next due time before external calls and uses the engine's existing
30-second fenced lease. Selection by due time rotates serviced work behind other
due executions. The lock is never held across provider I/O or native work.

A normal reconciliation schedules the next pass after two seconds. Errors use
persisted exponential backoff capped at sixty seconds; counters saturate at
sixteen. Cancellation makes a delayed row due immediately, while respecting an
existing controller lease. A crashed controller retains its lease until expiry;
the replacement reuses the attempt and native run ID. Stale fences cannot save
observations or replace a newer owner's retry schedule. User commands still have
one attempt and Jobs retry remains zero.

The worker flag is independent of new-admission policy. Keep it and storage
configured when disabling dispatch: the worker selects existing Jobs intent,
cancels executions removed from policy, and drains stopped tasks and sealed
output. Disabling transport as well prevents guests from finalizing; output then
follows the existing incomplete-result contract. Turning the worker off pauses
reconciliation and does not cancel compute. Graceful apid shutdown cancels and
joins workers before closing storage. Errors use stable classifications without
provider URLs or credentials. Existing deadlines and schedd's default-deny
launch gate remain authoritative.

The GitHub source RPC reports final metadata after its stream closes. The adapter
withholds EOF until the final exact SHA and truncation/error receipt validate,
so an incomplete source stream cannot be published as a verified archive.

## Qualification

Portable tests cover admission capacity and idempotency, concurrent global/account
leases, persisted retry timing, restart identity, policy removal, authenticated
HTTPS service admission/cancellation, source trailer validation, log cursors and
artifact publication. Synthetic source/provider/native receipts execute no steps.

The ADR-1338 native harness now uses HTTPS revision/binding/execution admission,
status, logs, cancellation and artifact routes. A subprocess apid worker alone
advances normal execution. A marker-scoped synthetic githubd gRPC fixture supplies
the archive; actual GitHub App authorization remains a separate gate. Provider
retirement, native stop, leak checks, metering and real GCS still require actual
receipts. Recovery cleanup can use the explicit engine after an interrupted run.
The native verdict now requires HTTP/worker evidence for every scenario.

All shipped CI flags, including the worker, remain zero. Account, bucket and
origin defaults remain empty, and public compute/dispatch capabilities remain
false. Run the dedicated native KVM/GCS gate and review its evidence before any
internal compute pilot. No deployment, image publication or provider mutation is
part of this change. Roll back readers before dropping dispatcher columns; its
migration refuses rollback while active Jobs executions remain.
