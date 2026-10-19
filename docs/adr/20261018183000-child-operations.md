# Child Operations

- **Status:** accepted for implementation
- **Date:** 2026-10-18
- **Owner:** Operations maintainers
- **Related:** [ADR-521](521-customer-operations.md),
  [ADR-666](666-operation-execution-preview-admission.md),
  [ADR-1196](1196-operation-deadlines-and-budgets.md),
  [ADR-20261018161833](20261018161833-operation-schedules.md)

## Context

One customer request often becomes many units of work: an import splits into
one Operation per file, a bulk refund into one per order. Today the running
handler either does all of it inside one Operation, so one slow item holds the
whole attempt and a retry repeats finished items, or it calls the customer API
itself with a stored customer token, so the platform cannot tie the pieces
together, cancel them together or show the customer one tree.

## Decision

A running Operation may start **child Operations**. A child is an ordinary
Operation of the same customer, admitted through the same path as
`POST /v1/platform-tenant-self/customer-operations`, that also records its
parent and the parent's key for it.

**Who starts a child.** Only the parent's current execution. An HTTP parent
uses `POST /v1/runtime/operations/{id}/children` with the same workload
assertion and attempt capability as its progress reports; a job parent uses
`POST /v1/runtime/job-operations/{id}/children` with its job run proof (run,
instance, capability, generation). Customer and account credentials cannot
start a child, and a stale attempt is refused. Workflow parents do not start
children yet: a workflow step that waits on children is a later step.

**What it may start.** An Operation the parent's deployment defines, named in
the request (`operation`), for the parent's customer, with an input that
matches that definition's schema. The child may be an HTTP, job or workflow
Operation. Children cannot start children: depth is one.

**Exactly once per key.** The request carries a `key` chosen by the parent
(for example the file name). The child is admitted with idempotency key
`child:<parent id>:<key>`, so a retried parent attempt replays the child it
already started instead of starting another; a different input under the same
key conflicts.

**Lifecycle.**

- Children run independently. A child's outcome does not change its parent.
- Cancelling the parent cancels every active child. The parent's cancellation
  commits first, under the same lock a child start takes, so no child starts
  after it; apid then cancels each active child, and repeating the parent's
  cancel finishes a cascade an apid restart interrupted.
- A parent that settles leaves running children running; they settle on their
  own deadlines and budgets.
- The parent reads its children's state with
  `GET /v1/runtime/operations/{id}/children` (or the job-operations route) and
  decides when it is done (fan-in by polling; a workflow wait on children is a
  later step).

**Locks.** A child start takes the workflow admission lock when the child is a
workflow, then the account, then the parent's execution (its invocation, or its
job run and task, the lock its cancellation takes), then admits the child
in the same transaction: the account-then-execution order every other
Operation write already uses.

**Visibility.** The customer and the account list a parent's children at
`GET /v1/platform-tenant-self/customer-operations/{id}/children` and
`GET /v1/apps/{slug}/operations/{id}/children`; each entry is the child
Operation with its `key`. Until launch the ordinary Operation response, events
and timeline stay unchanged, so nothing outside the gated routes shows the
link.

**Limits** (`pkg/api/limits.go`): children per Operation by plan (Hobby 10,
Pro 100, Scale 1,000). Children count toward the existing pending-Operations
limit.

**Gated.** `FAAS_OPERATION_CHILDREN_ACCOUNTS` (apid) names the accounts that
may start children; empty, the default, admits none, and every child route is
indistinguishable from an absent one. The routes stay out of the OpenAPI
document and SDKs until launch.

## Consequences

- Fan-out work is retried, cancelled and shown per item, and a retried parent
  never duplicates a child.
- The parent needs no customer credentials to start work for its customer.
- Each child is billed and limited like any Operation; a parent cannot exceed
  the account's pending limit by fanning out.

## Rejected alternatives

- **Unbounded depth.** A child that starts children makes runaway fan-out and
  cascading cancellation hard to bound. One level covers split-and-gather.
- **Parent settles only when children settle.** It ties the parent's deadline
  and budget to work it does not run, and hides a finished parent behind a slow
  child. The parent decides, using the children route.
- **Children through the customer API with a stored token.** The platform
  could not tie or cancel them, and the handler would hold a long-lived
  customer credential.
