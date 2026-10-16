# ADR-984 · Scenario resilience campaigns and recovery regression gates

- **Status:** accepted
- **Date:** 2026-10-12
- **Amends:** ADR-423 (scenario-scoped chaos), ADR-981 (private TCP faults)
- **Decision:** Extend registered scenario tests with leased private-service DNS
  faults and connection-attempt evidence, plus bounded staged recovery assertions,
  repeated fault campaigns, deterministic seed selection, regression gates,
  isolated replay, and failure minimization. Execute every campaign seed as its
  own scenario workload and evaluate aggregate policies at the campaign root.
- **Why:** A passing load SLO alone does not prove that a dependency fault was
  exercised or that recovery stayed within a previous application version's
  behavior. Saved execution history must support reproducible CI decisions.
- **Scope:** DNS faults act only on verified callers and targets in the same
  registered scenario run, under the existing fault lease and cleanup rules.
  TCP attempt counts include failed connections and distinguish attempts from
  matched faults. These mechanisms do not modify host networking or production
  workloads. Existing service authorization and normal tenant limits apply.
- **Evidence:** Coverage counts come from uniquely identified, complete fault
  windows. Recovery gates use confirmed recovery and each target's own phase
  metrics. Saved baselines must match workload, profile, seeds, repetitions and
  fault choices; report fingerprints and full policies are pinned in aggregate
  replay. Missing evidence fails selected gates. Existing SLO, cleanup and
  cancellation failures remain independent.
- **Bounds:** CLI campaign, replay and minimization run/time budgets limit test
  execution; they do not create or change account plan quotas. Trend reports read
  bounded saved reports offline and never provision workloads. Descriptive
  worst-observed metrics do not claim statistical confidence.
- **Verification:** Portable socket, DNS, authorization, store conformance,
  CLI/report and replay tests cover the feature. Real VM forwarding acceptance
  remains a separate native KVM gate; portable tests do not substitute for it.

See [scenario test configuration](../scenario-tests.md) for all limits, commands,
report contracts, and CI gate semantics.


## Saved campaign execution (2026-10-15)

An explicit `test policy run` command may execute the native batches of a saved,
budgeted coverage plan. Bind the expanded manifest, policy and input reports;
require an explicit clean source revision per baseline/current side. Execute one
side per checkpoint directory, with concurrency 1 by default and an upper bound
of 4. Recompute batch costs and enforce the saved plan budgets before scheduling.
Do not execute arbitrary saved command text or bypass native workload limits.

Checkpoint before launch and record the exact completed report hash afterwards.
Verify context, case/configuration counts and source provenance before accepting
completion. Resume skips verified completed work. Uncertain in-flight or partial
work must be reconciled rather than automatically repeated. Cancellation stops
new scheduling and lets active native runners finish cleanup. Cleanup failure
halts scheduling without discarding available evidence.

Merge completed receipts through the existing provenance, duplicate and revision
guards, then reevaluate required coverage and environment policies from raw
execution evidence. Planned coverage, preflight data and checkpoint completion
alone do not satisfy assertions.


## Interrupted campaign reconciliation (2026-10-15)

Reconciliation may adopt finalized raw execution evidence into a separately
versioned recovery bundle. Preserve complete campaign hierarchies; partial
extraction accepts only individually verified executed leaves and records their
original receipt paths and report hashes. Skipped receipts, planning metadata
and uncertain work never count as covered execution. Missing/conflicting evidence
and recorded cleanup failures block automatic continuation. Optional live
inspection is limited to read-only developer session GET requests for known
run identities and never waives fixture cleanup failures.

Reserve the full estimated cost of every attempted batch, including orphan
outputs. Grant a same-side continuation only the original side allocation less
those reservations, with hard zero bounds on exhausted dimensions. Never refund
uncertain cost or borrow the other revision side's allocation.

Publish a parent-directory seal after the evidence and continuation artifacts.
The seal freezes the original scheduler and binds the parent checkpoint and exact
continuation plan, preventing both schedulers from spending the same pending
allocation. A continuation without its matching seal is not executable. Blocked
seals can be refreshed after evidence reconciliation; ready seals are immutable.
Original checkpoints and execution report bytes remain unchanged.


## Unified campaign status (2026-10-15)

Expose an informational read-only snapshot of plan/checkpoint progress, verified
receipt coverage, cost reservations, recovery adoption and concrete next actions.
Do not provision workloads, take an execution lock, change artifacts or infer
process liveness from checkpoint labels or lock presence. Reevaluate coverage
from verified receipt inputs, never saved policy verdicts. Recovery adoption
requires its matching sealed bundle.

New execution checkpoints retain the current plan's recovery link with lineage
version 1. Existing versionless checkpoints remain compatible and are not
rewritten; status stops at an unrecorded ancestry boundary. Bound tracing to 32
parents and validate every parent checkpoint/seal link. Export escaped standalone
HTML and versioned JSON outside execution/evidence directories. A status snapshot
is not an execution, cleanup or policy enforcement gate.


## Portable campaign handoff (2026-10-15)

Transfer enumerated metadata and shareable receipt artifacts with byte-preserving
hashes and a bounded, versioned inventory. Use an external node resolver for
immutable parent directory labels so relocation never rewrites the original
plan/checkpoint/seal identities. Exclude source trees, application manifests,
logs and credential files; reject recognized credentials/configured secrets and
raw payload/diagnostic captures. Orphan log existence transfers as uncertainty
metadata, retaining full cost reservations without transferring log contents.

Freeze the source before publishing the bundle. Imports are inspection-only
unless an explicit shared exclusive claim activates one host/location-bound
recipient. Serialize claim operations, bind each plan to one work directory, and
revoke the previous recipient on a subsequent handoff. Immutable imported
evidence is separate from mutable runtime checkpoints. Native continuations bind
their first work directory in the parent before execution; exporting an ancestor
after continuation ownership is established is rejected.

Bundle integrity and portability do not waive native source/configuration checks,
budget ceilings, incomplete evidence or cleanup/reconciliation failures. Shared
claims coordinate only workers using the same claim store.


## Campaign cancellation and cleanup recovery (2026-10-15)

A plan-bound durable cancellation request stops new scheduling and drains active
native batches. The scheduler persists halted state without replacing completed
receipts; interrupted or incomplete runs retain uncertain status. Reconciliation
is required for continuation, with existing attempted-budget reservations.

An explicit cleanup command defaults to inspection and requires an inactive,
unsealed, owned campaign. It validates the saved plan, manifest, policy, report,
terminal native run identities, configuration and revision, and recorded workload
app IDs. All live identities are inspected before mutations and rechecked before
session deletion. Only the recorded run workspace may be deleted. Missing or
changed identities fail closed. The session API has no conditional delete by
app ID, so a final lookup/deletion race remains bounded by the unique native
run namespace. Shared import claims and runner locks protect normal campaign
ownership. Raw receipts remain immutable; observations cannot waive fixture or
consumer cleanup failures or refund execution cost.


## Live native run identity journal (2026-10-15)

Campaign subprocesses receive a private journal directory bound to their saved
plan, batch, side and source revision. Before provisioning, each native run
persists its ID and workload membership; provisioning intents precede API
requests and returned app IDs precede further workload work. Atomic snapshots
are synced with their containing directory. Cleanup records progress per
resource and continues even when journal writes fail, retaining explicit errors.

A provisioning intent without a returned identity is uncertain: recovery cannot
infer ownership from a project name alone. Explicit journal cleanup uses the
existing inactive-owner locks and live app-ID checks, never the default
workspace. Reconciliation may expose journal cleanup diagnostics, but journals
cannot substitute for terminal assertion evidence, waive cleanup failures,
establish completion, refund attempted cost or grant coverage. Missing reports
remain blocking. Unresolved journals must never be silently discarded by portable bundle
export. The following extension defines their transport.


## Portable journal recovery (2026-10-15)

Campaign bundles may carry exact journal bindings and run snapshots for attempted
checkpoint batches at each declared ancestry node. A per-journal inventory pins
the exact snapshot set and checksums; the existing bundle identity and immutable
import inventory pin those bytes. Import validates paths, bounds, checkpoint and
run bindings, workload lifecycle states, and complete inventory membership before
claiming authority. Earlier bundles without journals remain valid.

Root snapshots are copied into the owned recipient work directory, separately
from immutable artifact snapshots. Cleanup uses existing active shared claims,
runner locks, manifest bindings and live app-ID checks. Re-export captures current
work snapshots and revokes the old recipient through the existing handoff flow.
Ancestor journals stay immutable history. Unconfirmed provisioning remains
uncertain across transport. Journal evidence cannot grant coverage, establish
test completion, waive recorded failures or refund attempted execution budget.
Malformed or uncheckpointed journals are rejected rather than lost in transit.


## Unified read-only recovery assessment (2026-10-15)

An explicit `test policy recover --inspect` command combines verified campaign
status, receipt/journal cleanup identities, ownership and recovery seals into a
read-only assessment. Offline recorded identities remain distinct from optional
read-only live session observations. Deletion advice requires matching live
identities and available ownership; the existing mutation commands still perform
their own checks. No execution, deletion, lock acquisition, claim mutation or
checkpoint rewrite occurs during assessment.

Advice is emitted as argument arrays and POSIX-quoted display commands, with
mutation labels and explicit prerequisites. A ready verified continuation can be
inspected; a matching blocked seal can be refreshed without reopening its source.
Invalid/missing reports, uncertain provisioning and recorded fixture/consumer
cleanup errors remain visible blockers. Bounded evidence fingerprints before and
after inspection suppress commands if state changes. Informational reports do
not grant coverage, refund budget or act as cleanup authorization.


## Recovery completion verification (2026-10-15)

Read-only recovery assessment may explicitly verify completion using fresh session
GET requests. It separates complete resource inventory, known workload session
absence, terminal execution evidence, policy and overall recovery completion.
All available journal identities join receipt identities, including diagnostic
runs; uncertain provisioning, missing evidence, conflicting identities and
recorded fixture/consumer/workload cleanup errors remain independent blockers.

Version-two cleanup observations bind the original checkpoint/batch, revision,
evidence digests, resource identities and operation completion. Journal cleanup
also records the resulting journal digest. Matching observations provide
provenance only: legacy, partial, stale, unrelated or duplicate records cannot
substitute for fresh absence checks or test evidence. Observations may be supplied
separately after handoff; no authority follows from a saved summary.

Bounded local/external observation files and membership participate in snapshot
stability checks. Changing evidence invalidates the completion claim. Shared
claims and runner locks are inspected without acquisition. The exported summary
is informational, cannot waive recorded failures, grant coverage, refund budget,
or reopen sealed or transferred work.

Registration expiry is captured by the server from each member's preview lease;
the namespace deadline is the latest persisted member deadline. The preview
janitor may remove an expired namespace only after all member apps are deleted.
Discovery is bounded by runs and grants no mutation authority: PostgreSQL
serializes with registration/manual cleanup, compares discovered membership and
deadline, and locks member apps before deleting membership and match evidence.
Unknown legacy deadlines remain for explicit cleanup. Expiry observations are
informational and cannot establish resource absence or test completion. The
janitor does not extend its ownership to sessions or consumer credentials.

Campaign consumer creation commits a deterministic intent digest and requested
credential expiry atomically with the consumer row. The intent binds run, consumer
name and immutable app identity; external-reference uniqueness also prevents
adopting legacy or unrelated creations. Replays retain the same consumer and its
current revocation state. Creation requires a live registered developer session
and serializes on the run lock used by namespace cleanup.

Workers persist the intent before the request. A separate read-only, account-
scoped lookup lets recovery resolve a lost creation response without creating
resources or minting keys. Recovery verifies the complete receipt and records
recovered IDs only in observations, preserving frozen journals across handoffs.
Missing receipts remain uncertain; they do not establish absence. Schema-2
cleanup can cancel the exact intent using a durable tombstone. Cancellation and
creation serialize on the same run lock; cancellation revokes an already committed
consumer or prevents a delayed creation. Read-only completion requires a bound
cancellation proof, and old server missing receipts still require inspection.
Tombstones survive namespace cleanup and app soft deletion. Existing
consumers with known journaled IDs retain their original cleanup semantics.

Intent-scoped revocation compares the receipt and expected consumer ID under a
consumer row lock (or the memory-store mutex), including after app soft deletion.
Both linked and managed platform-customer ownership block this operation. New
intent journals use this route for saved and recovered IDs; legacy known IDs
retain their existing route. Unsupported lookup routes cannot prove absence.

The PostgreSQL recovery gate now composes the CLI portable handoff path with a
subprocess running the production authenticated API route table and a migrated,
isolated PgStore. Delivery faults control both creation/cancellation orderings
and lose committed consumer, key and cancellation responses. Acceptance requires
safe repeated recipient cleanup, disabled credentials, unchanged frozen evidence,
and resource completion that preserves execution uncertainty. Missing or skipped
PostgreSQL acceptance is a gate failure; the portable-only gate remains available.

Restart acceptance moves isolated database ownership to the CLI test parent.
Child API processes own only their listeners and pools. Real SIGKILL after
committed consumer, key and cancellation responses must leave the original SQL
state intact. A new process uses the same account, bearer, database and schema;
read-only lookups recover receipts and cancellation proofs before recipient
cleanup resumes. Both creation/cancellation orderings must pass across restarts,
including a delayed request reaching the replacement API and revoked credentials
remaining disabled. No database crash or VM restart is inferred from this evidence.

Recovery I/O uses a two-minute operation budget, five-second request deadlines
and at most three transient read attempts with bounded exponential backoff.
Server Retry-After delays are honored or deferred to a later invocation when
they exceed the local wait limit. Mutations are never automatically retried:
a subsequent recovery observes the committed outcome first. Failed and unvisited
resources remain explicitly unverified in recipient observations, with sanitized
reason and attempt metadata. Availability failures never establish absence or
execution success. HTTP outage and real API-to-PostgreSQL connection-cut acceptance
must demonstrate nonmutating failure, preserved frozen evidence and safe resumption.

The opt-in `test policy cleanup-watch` worker monitors one explicitly selected
private campaign. A stable grace period begins only after process identity
verification proves coordinator and worker termination; resource deadlines never
supply that proof. It reuses runner-lock recovery and full-history fault cleanup,
with finite attempts and existing bounded recovery I/O. Missing ownership proofs
or claim exclusion remain manual recovery cases. Watch success establishes cleanup
only, preserving all execution uncertainty and immutable evidence.
