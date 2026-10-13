# ADR-1153: durable managed PostgreSQL instance-catalog drain plans

Status: accepted for internal scheduler/controller plumbing; customer activation disabled.

## Context

ADR-993 retires explicitly selected native VM incarnations. Its recovery worker
cannot discover the membership that a controller intended to drain. A partial
list, terminal lifecycle rows or an empty catalog cannot establish writer safety.

## Decision

An explicit scheduler request captures the entire app instance catalog across all
deployments, modes and scopes under the already installed admission fence. The
immutable plan pins app ownership/revision and fence identity. Capture, native
member reservations and unsupported-member records commit in one transaction.
The existing bounded receipt-recovery worker resumes supported native members;
plan creation and status inspection do not call a VM or a database provider.
Prepare/Verify never install a fence or create a drain plan.

`pkg/api/limits.go` caps an atomic capture at 512 members. One extra row detects
overflow. An oversized or empty catalog persists a blocking plan and queues no
partial work. This is an explicit operational limit, not a customer plan quota.
Parked, undispatched, migrated, private qualification and incomplete-identity
members remain visible blockers with their original instance identity. Existing
terminal rows still require a matching native receipt; lifecycle state alone is
not evidence. Supported members may retire while other members remain blocked.

While the captured fence/owner epoch is current, database triggers reject new
instance membership, identity changes, new migration state and unconfirmed
deletion, including parked/terminal insertion and TRUNCATE. A trigger locks the
app even before the plan is visible, closing the capture/late-insert race.
Fenced membership guards require read-committed isolation: a locked app tuple
alone cannot refresh a repeatable-read snapshot after a plan-only insert.
The table-wide TRUNCATE guard requires read-committed isolation before inspecting
any app, because an older snapshot can hide both a new app and its fenced plan.
Confirmed STOPPED members can be retained or deleted without erasing receipts.
Cancellation releases the ordinary admission fence after credential revocation;
retained plans do not keep the former catalog locked after that release.

Read-only status recomputes each member against its exact receipt and current
incarnation. Missing/extra rows, ownership changes, cancellation, unsupported
states, malformed receipts and incomplete membership block the verdict. A plan
cannot be rewritten or silently recaptured on retry. Old evidence remains visible
after parent retention; a new owner epoch requires a new explicit request.

`catalog_retired` means the captured database catalog has matching native
retirement receipts. It is an observation, not an activation token. A future
publication transaction must reacquire current authority and require SQL-session
isolation, routing convergence, target verification and publication/restart
prerequisites. This plan does not cover orphan host processes, clients outside
the app catalog, standalone jobs with copied connection strings, source writes
after the restore point or rollback semantics. Empty-host inventory provenance
and additional lifecycle retirement authorities remain separate work.

## Validation and limits

PostgreSQL regressions exercise full capture, immutable retry, bounded overflow,
unsupported members, terminal rows without native evidence, atomic rollback,
late insertion/identity/retention fences, stale ownership, cancellation and lost
reply recovery. Scheduler fault injection checks capability gates and that
planning itself has no physical effects. These are control-plane and fixture
observations. Physical native x86_64 Linux KVM `test-metal` and `leakcheck` remain
required before customer activation. Production provisioning stays disabled.
