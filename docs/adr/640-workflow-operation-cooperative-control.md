# ADR-640: Cooperative control for workflow Operation handlers

## Status

Accepted — 2026-10-06. Production admission remains disabled.

## Context

ADR-638 fences interrupted native attempts and cancels scheduler HTTP calls.
ADR-639 retains verified result copies for approved recovery. A disconnected
HTTP caller does not stop guest computation or the guest's storage requests.
Workflow handlers need the same cooperative cancellation support as ordinary
HTTP Operations without borrowing an invocation claim or renewing native leases.

## Decision

Add a read-only workflow control endpoint available to every linear HTTP action
of an Operation. Require a fresh Operations workload assertion, pinned running
instance and current native run/step/generation/attempt/capability. Enforce native
account, app and tenant-link eligibility. Response identity names the native run
and action; it contains no invocation ID, input, output, owner or capability.

The deadline is the current append-only attempt's started_at plus its immutable
definition's timeout, using the shared 30-second HTTP action default when omitted.
The compact step's first started_at survives recovery and must not supply this
budget. The usable interval ends at the earlier deadline or native lease. Reads
do not write lifecycle rows, renew leases, consume reports or emit events.
PostgreSQL reads lock run then operation and use only the same transaction.

Scheduler dispatch uses the same fixed deadline and rejects late responses.
State-store success commits and artifact preflight/reserve/prepare reject expired
attempts even when the native lease remains live. Failed dispatched work retains
the existing requires_reconciliation contract; expiry never grants automatic
retry. Approved native resume supplies a fresh attempt budget while preserving
confirmed prefixes, fixed inputs, release pins and retained verified copies.

GregaleWorkflowOperations.runCancellableRequest performs an initial control
read, polls within existing bounds and checks control before returning success.
It shares conservative duration accounting with the ordinary HTTP scope while
validating the distinct workflow response identity. Local expiry aborts pending
I/O even when subsequent reads cannot finish. A stale, interrupted or cancelled
native proof stops the scope. Native cancellation atomically closes the attempt,
so handlers may observe lost authority rather than a cancellation boolean.

Prepared artifact writers receive the scope's optional AbortSignal. Identity,
receipt lookup, prepare and the example's storage signing/PUT requests observe
cancellation. Request cleanup fences detached callbacks. Handler cooperation
requires checking the signal/checkpoints and yielding during long CPU work.
Stopping I/O cannot undo an external effect. An uncertain write is not retried;
a copy verified before interruption stays private and retained for reconciliation
or approved reuse under ADR-639.

## Validation and rollout

Memory and PostgreSQL acceptance verify all-action control, read-only state,
substituted/stale proof rejection, fresh resumed attempt budgets, cancellation
with retained private copies, live-lease deadline expiry and rejected late
success. HTTP acceptance covers workload identity, private native response,
no-store caching and approved recovery. SDK tests cover signal propagation,
blocked uploads, local expiry with a stalled control read and verified-copy
reuse after interruption. Ordinary HTTP scope tests remain regression coverage.

These portable checks do not open admission. Native execution qualification and
existing operational rollout gates remain required before activation.
