# ADR-711: Retained native startup provenance

Status: accepted · 2026-10-07

## Context

ADR-710 authenticates the public gateway's actual startup session,
configuration and boot/process tuple. ADR-706 provides retained native
service, executable, cgroup and socket observations. A later unreachable-host
review needs their association recorded while the selected startup is still
reachable. Reconstructing that association after withdrawal, or treating a
stored DTO as fresh authentication, would supply evidence that was never
observed together.

The selected deployment is native bare metal, with no out-of-band controller
configured. This decision supplies historical selected provenance. It does
not qualify an enforcement authority or resolve unreachable withdrawals.

## Decision

Add a private `NativeStartupProbe` in `pkg/gateway/edgetopology`. Its public
constructor accepts only the existing canonical ingress token and uses the
fixed retained local Linux collector. The administrative review selects
exactly one public service and an explicit canonical loopback endpoint held
by that service. Freeze and normalize the complete selection before I/O;
derive the expected slot/session/configuration and boot/process tuple from it.
The constructor accepts no injected HTTP client, filesystem path, process
reader, supplied observation or verifier.

Open one retained native scope session, capture it, probe the authenticated
ADR-710 startup endpoint with a fresh nonce and connection, then capture that
same retained session again. Require equal host, systemd invocation, main
process/start ticks, UID, executable identity/digest, cgroup identity and the
complete reviewed listener inode/FD set. Reject any inconsistency, capture
failure, malformed proof or cancellation and return no opaque observation.
Close the retained session on all paths. The envelope retains both native
snapshots and the exact authenticated startup proof; returned bytes are copies.

Use the centralized 90-second collection/transaction budget, including database
lock waits, and a 1 MiB stored-envelope/review ceiling in `pkg/api/limits.go`.
The existing native capture and HTTP sub-budgets still apply. Native snapshot
timestamps are local collector annotations. The database independently samples
its own clock around collection after acquiring the roster locks.

`ValidateNativeStartupRecord` checks canonical storage shape and selected
identity consistency only. It rejects duplicate/unknown/aliased fields,
noncanonical framing, foreign reviews, inconsistent bookends, malformed epoch
proof fields, invalid listener identities/FDs and reversed timestamps. It has
no HMAC key and does not authenticate historical proof or reconstruct an
opaque live observation. A correctly shaped bad HMAC can pass this parser;
the concrete live probe must authenticate it before first enrollment.

Add a private PostgreSQL store seam and the forward-only
`runtime_upgrade_native_public_startups` table. Key the immutable row by public
startup session; retain the original gateway/public roster revisions,
slot/configuration, machine/boot/process tuple, complete canonical selection,
complete envelope, their SHA-256 digests and database observation/record times.
Store unsigned kernel start ticks as canonical decimal text to preserve the
full uint64 range. Namespace inode representations receive the same unsigned
range checks. Use SQLC for all implementation queries.

First enrollment acquires the internal head then public head in the established
order, requires the exact current guarded member with an unexpired guard and
no historical withdrawal, samples database time, invokes the concrete retained
collector inside the transaction, then rechecks eligibility before insertion.
Caller-provided observations cannot enter this public store seam. A private
collector callback exists only for portable database contract tests; it is not
exported, configured or wired into a daemon.

The migration independently checks current heads, exact member/guard, absence
of withdrawal, evidence digests and matching embedded identity fields. UPDATE,
DELETE and TRUNCATE are rejected. These checks protect ordinary writes, not a
database administrator who disables triggers or replaces the schema. SQL
checks are structural; they do not authenticate HMAC or inspect a kernel.
Register this table as operational history in the environment-clone schema
inventory, so customer environment cloning cannot copy native startup evidence.

Exact retry requires the original canonical selection and original roster
revisions. It returns the first committed row's original bytes, nonce and
timestamps without invoking the collector or requiring renewed membership or
guard freshness. This supports lost responses and process restarts even after
withdrawal. Concurrent enrollment retains one original record; a different
selection for the same session conflicts. Reads return historical copies and
require the exact slot/session. Commit failure returns no accepted record.

## Consequences and limits

An independently selected native scope and an authenticated startup can now
be observed together and durably reviewed later. This record is evidence of
that selected observation only. It does not demonstrate complete host scope,
continuous liveness, nonresumption, physical-resource identity or termination.
Snapshot bookends cannot prove no transient change occurred between samples.
A shared-token holder, compromised host/kernel or privileged database operator
remains outside independent assurance. Cloned identifiers and saved execution
images cannot be excluded through these records.

This change installs no worker, administrative route, CLI command or production
collector invocation. It enables no private flag, daemon capability or new root
component; public runtime-upgrade `execution_available=false` remains unchanged.
Receipt, capacity-release, traffic-cutover and retirement paths do not consume
this row as authority. A missing historical enrollment cannot be backfilled
from unreachable-host claims. Existing withdrawals stay pending until their
separate accepted resolution protocol succeeds.

Next, define trusted physical-resource attribution and explicit authority
qualification that refers to this immutable enrollment. Provision independent
out-of-band control and signing-key custody before a real fencing adapter can
claim irreversible termination. Native Linux/systemd/socket, crash/restart,
lost-response and hardware enforcement acceptance remain required; macOS
synthetic fixtures and cross-compilation cannot supply them. The larger KVM
`test-metal`/`leakcheck` gates remain pending and VM lifecycle is unchanged here.

## Validation and adversarial review

Require normal and race coverage for joint reconciliation, all authenticated
startup-field substitutions, malformed HMAC, boot/process/invocation/executable/
cgroup/socket/FD changes, cancellation, canonical evidence, ownership of
returned bytes, current guarded enrollment, withdrawn/stale member rejection,
immutable historical retry after restart, concurrent first enrollment, clock
sampling after head-lock waits and database mutation rejection. Run actual
PostgreSQL migrations and SQLC regeneration; compile Linux amd64 tests and run
repository-pinned lint, policy and diff gates. Distinguish executed tests from
cross-compilation and pending native acceptance in the final validation record.

Local validation executed 139 top-level tests both normally and with race
detection, with identical test sets and no failures/skips: 76 topology tests,
37 ingress tests, 23 PostgreSQL enrollment/fencing tests and three clone-schema
policy tests. The full migration set applied to an isolated PostgreSQL 16.15
cluster. Its live schema matches the snapshot after version-noise/EOF whitespace
normalization; SQLC v1.31.1 regeneration and all repository policy gates passed.
Linux amd64 test binaries compiled for topology and state; neither binary ran
on this macOS arm64 host. The final state compilation disabled redundant vet
for that compile-only check; executed normal/race tests used default vet.
Repository-pinned golangci-lint v2.4.0, including tests under Linux and whole
changed Go files, reported zero issues. SQL negative tests require SQLSTATE
23514 from the evidence/immutability guards; a duplicate-key failure cannot
satisfy those assertions. No PR, push, deployment or remote host operation was
performed. Actual native Linux/hardware/KVM acceptance remains unexecuted.

Adversarial review: a complete selected review is not complete fencing scope.
HMAC authentication alone cannot bind a physical chassis or exclude resume
paths. Historical bytes cannot become fresh liveness through decoding or retry.
Collecting before acquiring current-head locks can attribute a superseded
member, so both bookends and both database clock samples follow those locks.
Unsigned process identity must survive SQL round trips without signed overflow.
No provenance row closes a withdrawal or manufactures an enforcement receipt.
