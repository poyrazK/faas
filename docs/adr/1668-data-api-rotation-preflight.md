# ADR-1668: Read-only Data API rotation preflight

Status: Accepted

## Context

ADR-1667 protects coordinated SQL refresh across managed credential generations.
Preparation rejects unapproved catalog or permission changes before publishing a
replacement, but customers receive a generic credential conflict. Operators need
actionable checks before requesting rotation and a view of existing rollout and
retirement progress. Neither task warrants granting owner access to the runtime.

## Decision

Add `preflight` to the owner-installed `refresh-rotation.mjs` tool. Inspect one
repeatable-read, read-only transaction, with existing statement and lock bounds,
and always roll back. Reuse exact table and hook validation from enrollment;
inspect binding membership, source role/OID, restricted privileges, fixed refresh
and deadline grants, approved RPCs, catalog witness, serving fingerprint and
managed baseline grants. Report policy TO lists that apply specifically to the
source login: a fresh restricted replacement cannot match an existing login's
OID, so the pinned witness cannot transfer. Do not evaluate arbitrary policy
expressions, execute user RPCs or install a hypothetical candidate to test them.

Return versioned JSON with stable check codes, `passed`, `blocked` or `unknown`
statuses, fixed repair advice, binding UUID, pause state and member count. No SQL
bodies, source role names, private scope markers, refresh tokens or connection
strings are returned. Dependencies that cannot be checked remain unknown. Exit
nonzero when checks are blocked or unknown. Read-only preflight neither acquires
coordination locks nor approves new catalog state; an unchanged paused binding
can pass without knowing its private refresh token.

Add `gregale data-api rotation preflight NAME --binding UUID --owner-check FILE`.
The small JSON file specifies a trusted local command's argument array and
directory, following the existing owner-command boundary. Use the matching
module's preflight action. No shell expansion occurs. The command inherits the
operator's environment, uses no stdin and has a bounded deadline and output.
Discard child stderr, validate a single bounded JSON result against its binding,
complete check set and derived readiness, and regenerate advice from fixed codes.
Never relay arbitrary child output or exception messages. The config is trusted
executable code; the wrapper does not sandbox a customer-supplied owner command.

Before executing the local command, read the existing app, managed binding and
app binding inventory. Verify app ownership, `data_api` access, environment key,
scope and generation/pending-state agreement. Partial or conflicting metadata
cannot produce a ready result. A binding must be ready with no previous rotation
pending. The command requests no rotation, restart, probe or owner task. It needs
account API authentication and the local migration connection; application JWTs
and refresh tokens are unnecessary.

Add `data-api rotation status NAME --binding UUID` for these metadata reads alone.
Show pending refresh, failed handoff, pending cleanup and retirement in progress.
Keep wake completion separate from cleanup. `no_previous_generation_pending`
describes catalog bookkeeping, not an independent provider revocation check.
Include timestamp-based runtime freshness with its existing limitations. Separate
reads are best effort; detect observed generation/state disagreements and ask
the operator to retry. An idle binding may never have rotated.

Hosted `data-api diagnostics --rotation-binding UUID` optionally includes the
same projection, respecting deployment selection. Metadata failures mark the
report partial while preserving log analysis. Offline diagnostics reject that
flag and remain offline. This adds no API endpoints, control-plane persistence,
public SQL functions or serving-path checks. Owner SQL remains within ADR-650's
external customer database boundary. New command/config/output limits live in
`pkg/api/limits.go`.

## Consequences

Owners can identify common rotation blockers and repair them through explicit
coordinated verification. Preflight is advisory: owner edits can follow its
snapshot, arbitrary RLS expressions can depend on identity, and unusual effective
privileges may differ on a future login. A passing result does not prove a future
candidate, runtime adoption, readiness or provider revocation. Existing provider
preparation rechecks its full witness transactionally and remains authoritative.
No automated policy broadening, secret publication or predecessor revocation is
authorized by diagnostics.

## Validation

Native PostgreSQL/PostgREST coverage exercises absent/mismatched enrollment,
private table ACL drift, hook drift, unsafe role attributes, serving/deadline/RPC
grants, schema drift, and approved source-specific RLS. State snapshots remain
unchanged across checks. The real owner command returns JSON on both success
and failure through a migration login without serving memberships. An unchanged
paused binding passes without a refresh token. CLI regression tests allow only
the expected GET requests, reject another app's binding, exercise deadlines,
bounded/invalid/duplicate/contradictory owner reports and discarded sensitive
output, and distinguish handoff completion from retirement. Hosted diagnostics
preserve deployment selection and offline diagnostics reject metadata reads.
