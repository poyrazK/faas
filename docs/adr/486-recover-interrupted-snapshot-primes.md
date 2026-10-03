# ADR-486: Recover interrupted snapshot primes

Status: accepted

Date: 2026-10-03

## Context

`dispatchPrime` marks a deployment failed whenever `Prime` returns an error,
including when schedd's daemon context is cancelled during shutdown. The terminal
deployment write deliberately uses a detached context, so a normal daemon
restart can turn an otherwise recoverable snapshot preparation into a customer
visible failure. Cancellation can also leave the prime instance in an active
state. The prime recovery sweep treats that row as work still in progress and
will not start another VM.

## Decision

Treat cancellation of the daemon context as an interrupted handoff. Do not mark
the deployment failed; leave its snapshot preparation stage available to the
existing prime recovery sweep.

When `Prime` has created an instance and its daemon context is cancelled before
the prime completes, use a detached context bounded to two VM destroy timeouts
to destroy that specific VM. Once vmmd confirms destruction, release its local
resource reservation and transition the row to `stopped`. If vmmd cannot
confirm destruction, leave an active row in place so recovery does not create a
duplicate VM. If a cancelled boot already moved its row to `failed`, attempt to
restore `cold_booting` as the active fence. Already parked or stopped instances
need no further teardown. Genuine prime failures with a live daemon context
retain their existing terminal behavior.

On loop shutdown, stop accepting work and wait up to the same cleanup budget
plus five seconds for accepted work to finish. This keeps the database pool
available while interrupted Prime calls persist cleanup state. If the bounded
drain expires, log the timeout and continue shutdown. Process crashes remain
outside this graceful-shutdown cleanup. The existing active-instance guard
continues to prevent recovery from creating a duplicate VM after an ungraceful
process death.

At the daemon boundary, wait up to thirty seconds for the scheduler loop to
return before closing the shared database pool. If vmmd cannot confirm teardown
after a cancellation already moved the instance to `failed`, the scheduler
attempts to restore its `cold_booting` state as an active fence.

## Consequences

A graceful schedd restart during snapshot preparation no longer fails the
candidate solely because the daemon stopped. Successful cleanup leaves a
`stopped` instance that the existing recovery sweep can replace. Failed teardown
keeps an active row as a duplicate-VM fence. No schema migration or customer
API change is required; shutdown waits at most thirty seconds for accepted loop
work before closing the database pool.

## Verification

Go formatting was applied to the changed Go files. Runtime and VM-lifecycle
checks remain to be run, including the repository's required metal and leak
acceptance for VM lifecycle changes.
