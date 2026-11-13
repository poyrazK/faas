# ADR-20261104135500: Account mutation concurrency and recovery errors

- Status: accepted
- Date: 2026-11-04
- Extends: [account billing identity](20261103131742-billing-identity-plan-fit-and-egress-ceiling.md), [organization admission](20261103214500-account-proof-and-organization-admission.md), and [account recovery](021-account-export-and-staged-deletion.md)

## Problem

Billing detail PATCH handlers filled omitted fields from the account snapshot
loaded during authentication, then overwrote every billing field. Concurrent
requests editing different fields could silently undo each other. Ownership
transfers did not check the recipient's Free-organization limit. Account restore
also described every storage failure as an expired grace period, including
temporary database failures while restoration was still possible.

## Decision

Add a partial billing update store interface while retaining the existing full
replacement method for internal callers. Nil fields mean omitted; non-nil empty
strings mean explicit clears. SQL updates only supplied fields in one statement;
memory storage merges under its mutex. Both personal and organization subjects
use this contract. Validation, normalization, provider synchronization and
field-name-only audit events keep their existing behavior.

Both ownership transfer paths check the recipient's Free-organization count
before changing either role or publishing activity. PostgreSQL shares creation's
recipient account lock and locks the incoming organization against plan changes
before checking capacity and committing the transfer. Membership validation still
precedes quota disclosure. Memory storage uses the same count under its mutex.
Paid organizations consume no Free slot. Provider-driven downgrades keep their
existing behavior; this change governs admission of a new Free ownership.

A refused transfer returns `403 org_free_limit_exceeded`, identifying the new
owner's count. Making room allows the same transfer to be retried. Unknown
restore failures return a retryable capacity error; only conflict/not-found
outcomes retain `409 account_not_restorable`. A failed restore preserves the
pending deletion and can be retried after service recovery.

No migration, new quota or wire request field is introduced.

## Validation

Memory and PostgreSQL regressions cover concurrent disjoint billing edits,
explicit clears, both transfer paths at capacity, paid transfers, and contention
between transfers and creation. HTTP tests reuse stale account snapshots, check
the transfer refusal and subsequent success after capacity is freed, and inject
temporary restore failures followed by successful retries.
