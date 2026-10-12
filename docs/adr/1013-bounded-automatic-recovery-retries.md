# ADR-1013: Bounded automatic recovery retries

Status: Accepted

## Context

Policies prepare exact response plans and retain final execution evidence. Operators
need opt-in recovery of saved failures with execution limits and an automatic stop
after repeated failures. Preview remains the default.

## Decision

Add `automatic_retry` policy mode. Configuration requires explicit workflow names,
explicit `failed`/`dead` failure statuses, only the `saved_failure` trigger, an hourly
execution budget (1..100), and a consecutive failure pause threshold (1..10). Existing
absolute resume limits, cooldown, preparation budgets, and native recovery quotas
still apply. Cancellation stays manual. Configuration uses the existing authenticated
deploy-write/MFA endpoint and compare-and-swap version fence.

Only newly prepared retry actions from that enabled policy version acquire durable
automatic intents. Preview plans are never converted implicitly. A policy replacement
suppresses old pending intents atomically, resets its failure streak and pause, and
requires a new version. Frozen targets already prepared under older policies remain
available for manual review; they are not automatically re-prepared. Dismissed reviews
suppress pending automatic actions. Approval alone never creates authority.

The scheduler polls a bounded batch. Serialize automatic retries per app: no next
retry until the outstanding retry has proven its terminal execution outcome. Unknown,
missing or superseded evidence defers progress. Recheck frozen parent lineage, run,
generation, current workflow/failure filters and retry preflight before child creation;
native admission rechecks generation and quota again. Manually created matching child
receipts suppress automatic application without consuming an automatic budget.

In PostgreSQL, acquire the existing account quota/pruning lock before the policy row
lock. Lock the pending review and intent before checking dismissal, serializing
with review decisions. In one transaction create the child, write its automatic application receipt,
and charge the execution budget; a rollback changes none of these. Refactor the manual
child creation path into the same transaction helper. MemStore shares its existing
creation helper under the mutex. Stable plan action IDs reuse existing request-ID
idempotency. Configuration changes serialize with application. Disabling prevents new
automatic children; committed child recoveries remain controllable through existing
recovery commands. No cancellation occurs implicitly.

Execution budgets count automatic child job creation, including eventual admission
refusals. They and execution cooldown span policy versions; editing settings does not
refund prior execution. Preparation budgets remain separate. Applied intent retention
follows its review and parent recovery job. Independent execution charges retain the
spent budget and cooldown across review pruning; charges older than the maximum
policy cooldown are pruned during automatic polling. Child identifiers and saved results are
retained independently of live workflow rows. Every creation audits a trusted system
actor naming its policy app and version.

Process final automatic results once under the policy lock. Failures increment the
current version's streak; success or cancellation resets it. Old-version results still
settle their outstanding slot but do not affect a new version's streak. Terminal
skipped admission counts as failure. On reaching the threshold, persist the pause,
stop both preparation and automatic execution, and atomically enqueue one historical
`automation.recovery_policy_paused` event per version with frozen enabled app receiver
IDs. With no receivers, the durable policy pause is still visible and no event replays
to subscriptions added later. Existing completion events still report final plans.

Policy inspection exposes failure count and pause evidence; review inspection exposes
intent/application records and final proofs. Reuse webhook signing, delivery ledger,
backoff and cooldowns. Receivers deduplicate by stable `event_id`. A later policy update
does not revoke the historical pause notification. Migration downgrade refuses any
retained automatic execution, automatic policy, pause or notification evidence.
