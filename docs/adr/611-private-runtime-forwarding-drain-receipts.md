# ADR-611: Private predecessor forwarding fences and durable drain receipts

Status: accepted · 2026-10-06

## Context

ADR-610 observes forwarding activity, but zero can become busy immediately.
Candidate/predecessor IDs and traffic percentages can also repeat after a
rollback and reactivation. Neither an old zero nor an installed-weight receipt
can identify that new routing state. These gaps precede safe retirement.

## Decision

Keep public Apply and worker admission disabled. Add the default-off private
`FAAS_RUNTIME_UPGRADE_DRAIN_CONFIRMATION=1` flag, requiring the existing routing
confirmation flag and canonical slot configuration. Construct one fencing
activity tracker before exposing any forwarding factory, with the same fresh
process session. Without this flag ADR-610 remains observational and existing
forwarding admission remains unchanged. No systemd unit enables the flag.

Append a forward-only migration. Every deployment row receives a non-nil random
routing token; a BEFORE INSERT/UPDATE trigger always generates a fresh token,
including direct SQL, attempted token restoration and clone insertion. Deletes
remove the row from the snapshot. Do not add a global routing mutex or rely on a
prunable log cursor. Tokens are operational metadata in an otherwise existing
deployment row; no customer intent or public DTO is added.

Read all live rows for an app, ordered by UUID, in one repeatable-read snapshot.
SHA-256 the canonical `id:token\n` sequence. Bound this complete snapshot to 256
rows; an overflow fails closed without substituting a partial vector. It includes
stages conservatively, while the weight projection uses the existing production
lane selector. Any row update changes the revision, even unrelated metadata.
Rollback followed by the same percentages cannot restore a prior revision.

Project a drain plan only for the matching completed private operation and
immutable cutover with the exact activated candidate at 100 and predecessor at
0. Read the current reviewed roster in the same snapshot. A missing roster can
close private forwarding but cannot publish a drain observation. No health or
qualification authority is inferred from this operational projection.

The optional gateway weight-snapshot seam installs weights and then delivers
the exact revision/plan under the existing per-app refresh stripe. Close the
predecessor's admission atomically at actual forwarding dispatch, including a
target selected before cutover. Existing forwards continue to completion.
Ordinary HTTP, streaming HTTP/gRPC, production mirrors, synthetic invocations
and HTTP upgrades share the wrapped VM bridge. Return the existing sanitized
503 response for denied forwards. In private fencing mode, unidentified or
untrackable forwards are denied and coverage becomes permanently unknown.
Capacity exhaustion cannot let uncounted activity enter behind a closed zero.

Exact fence retries retain their process-local UUID. A different routing or
reviewed operation/roster binding gets a new UUID, atomically, without briefly
opening the predecessor. Retain at most 256 app fences. A failure, timeout,
expired heartbeat/receipt or missing membership never releases a held fence.
An authoritative different revision without an applicable plan releases it;
rollback requests can then run. Reactivation installs a new fence and waits for
those requests too. Private repair merges its recent cutover scan with every
locally held fence, including old cutovers and rollbacks, sorts/deduplicates and
pages at the existing 32-app limit. Held fences never age out of repair coverage.

Gatewayd alone publishes operational drain facts after a known zero under the
closed fence. Bind each to app, stable slot, process session, completed operation,
candidate, predecessor, cutover time, reviewed roster revision, routing revision,
fence UUID and monotonic activity version. Use database-clock observations with
an exact one-minute lease. Zero is a table CHECK. Validate canonical identifiers,
digest and uint64 versions; reject a lower version for the same process/app.
The app's receipts are bounded to 64 processes; expired rows are pruned before
admission and by bounded repair. An expiry index and stable statement-clock
predicate let bounded repair find expired receipts without sorting the whole
table. Old roster facts cannot claim current membership.

Publication locks the roster head for share, then the app and all live deployment
rows in UUID order. Reread the plan and routing revision after lock waits. Check
the exact current slot/session heartbeat using a database-clock SQLC insert
after those reads; expiry during a wait rejects publication. Failed writes keep
the local admission fence closed for retry. Gateway observation facts do not
change deployments, instances, artifacts, traffic weights or historical journals.

The private apid `DrainControls.Verify` seam returns a fresh consistent database
snapshot. Use the database clock for its initial evidence evaluation. It first
requires ADR-607/609's current scoped health, activation,
qualification, complete roster, heartbeats and installed weights. Then require
matching, nonfuture, unexpired drain receipts for every reviewed process and the
current routing revision. Conservative validity is capped by the earliest drain
lease and existing evidence expiry, checked with the database clock after reads.
The shared PostgreSQL membership evaluator refreshes its check time from that
clock after reading heartbeats, including existing private verification callers.
Return `forwarding_drained` only for this observation. Missing evidence stays
pending. Never borrow historical verified journal status or silently select a
subset. There is no new public endpoint, durable success journal or cleanup action.

## Consequences

This closes new forwarding at the shared HTTP-to-vmmd seam and records its
drain. It does not fence earlier scheduler wake/admission, raw TCP/UDP services,
legacy URL transports, direct vmmd callers, guest work after client disconnect,
or the actual public ingress fleet. The reviewed roster still needs evidence
from authoritative ingress configuration. A snapshot observation is not a
retirement lease: a cleanup writer must fence and recheck current routing,
membership, qualification and evidence expiry at its own publication point.
Scheduler/VM-side quiescence and retained rollback artifacts remain required.
No automatic retirement, artifact deletion, forced connection close or rollback
is added. No backend PR, push, production launch or deployment is performed.

The deployment token is registered in the clone schema inventory; insert triggers
regenerate it rather than inheriting source tokens. Drain receipts are operational
clone data. The new PostgreSQL-only fact store is additive to `Store`; pure
gateway tests use explicit fakes rather than a memory store minting DB revisions.
Native Linux amd64 KVM acceptance, `test-metal` and final `leakcheck` remain gates
before customer enablement. Local SQL, socket and gRPC fixtures are synthetic
control-plane/forwarding evidence, not native VM retirement proof.

## Validation

Pure contracts cover retained in-flight work, late dispatch denial, candidate
admission, exact retries, rollback/reopen/reactivation, unknown coverage, bounds,
copy-safe repair enumeration, concurrent admissions and release, mismatched
installed weights, publication failure and complete paged repair. PostgreSQL
contracts exercise full-roster receipts, account isolation, missing participants,
direct SQL token restoration, lower-version replay, invalid process/slot,
future/expired evidence, expiry pruning, rollback/reactivation with the same IDs,
and routing/heartbeat changes while publication waits on an app lock. The migrated
clone registry check, existing routing/health contracts and real streaming/socket
fixtures remain validation dependencies.
