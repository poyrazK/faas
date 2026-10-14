# ADR-1280: Automatic policy polling failure isolation

Date: 2026-10-13

## Context

ADR-1278 separates manual and automatic polling. ADR-1279 defers deterministic
manual observation errors. Automatic execution still ends its pass on the oldest
faulty policy, allowing that policy to delay other apps indefinitely.

## Decision

Keep the existing account-before-policy claim and execution authority checks.
After claiming a policy, run automatic initial and retry work in a PostgreSQL
savepoint. Recognized deterministic data or policy-authority errors roll back
execution work before atomically storing a safe polling failure and advancing the
policy's existing execution clock. Successful deferral counts within the existing
batch allowance. Failure persistence and commit errors still stop the pass.

Add nullable checked `automatic_poll_failure` separately from policy intent and
execution failure counters. Expose it as `automatic_execution.polling_failure`
on policy and queue responses, with code, attempts, failure time and retry time.
Codes are `invalid_policy_authority`, `invalid_polling_data`, or
`invalid_recovery_identity`; raw database error messages are never stored.
Centralized exponential backoff starts at one minute, caps at 30 minutes, and
saturates attempts at 32. A successful poll, policy update or operator resume
clears the polling failure. Reads do not wake work or mutate this metadata.

Only explicit invalid authority/data/identity errors, JSON decoding errors, and
PostgreSQL check/invalid-text/date errors are deferred. Cancellation, deadline,
connection, locking, serialization, resource and unknown errors end the pass.
Outstanding retry inspection errors are propagated through the same boundary
rather than swallowed. Missing retained action proof is a deterministic data
error; missing or unverified execution evidence remains an unresolved outcome.

Memory polling validates retained authority and identities before mutations and
snapshots this app's execution-owned policy, jobs, initial reviews and webhook
outbox inside the mutex. Failed execution restores those values before deferral.
Outstanding inspection remains outside the mutex and rechecks its existing version,
update clock and action fences before entering a fresh rollback boundary.

## Consequences

A faulty automatic policy cannot consume every healthy app's execution slot.
Failed attempts retain no new job, automatic charge, result, pause, history or
outbox effect. Deferral neither changes policy authority/version nor counts as
execution failure, grants retries or resumes an operator pause. Existing budget,
cooldown, generation and account locks remain authoritative.

Apply migration `20261014092444724` before deploying the worker. Rollback refuses
to drop retained polling failures. Typed policy inspection can still fail if the
policy JSON itself cannot be decoded; polling metadata remains separately stored
for operational diagnosis. Both stores have fairness and restart regressions;
PostgreSQL late-write failures test job, charge and history rollback in both modes.
