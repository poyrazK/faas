# ADR-1735: Owner-approved deadline grants during credential rotation

Status: Accepted

## Context

ADR-1734's SQL deadline hooks require explicit per-login execution grants.
Replacing a managed Data API login can therefore produce a credential whose
runtime fails its deadline startup check. The provider must prepare permissions
before publishing the replacement, while preserving the previous credential
until the existing scheduler rolling refresh confirms retirement is safe.

## Decision

Add an optional stable binding UUID to internal credential requests, independent
of the generation-specific identity key. The binding service supplies it for
ordinary and privately reserved credentials. Neon carries it to the existing SQL
role transaction. It does not affect role names, passwords or idempotency keys.
Standalone qualification requests may omit it.

Add explicit `enroll`, `check-enrollment` and `unenroll` owner commands to the
deadline installer. Enrollment requires the stable managed schema owner and an
unprivileged managed `data_api` role with its existing resource-scope marker.
Hooks must already match the runtime and belong to the managed schema owner.
Store approval by `(binding_id, scope)` in
`gregale_data_api.deadline_binding_policies`, with protocol version one and the
SHA-256 hashes of both fixed function bodies. This is owner-installed customer
DDL, following ADR-650's external-database boundary, not a catalog migration.
The table is private, permanent, owner-controlled, without RLS or inheritance.
Repeated enrollment accepts the same contract; incompatible approval requires
explicit removal. Unenrollment removes automation, preserving existing grants.

Before committing a candidate Data API role, Neon explicitly assumes the stable
schema owner for policy lookup and verifies table ownership, ACLs (including
column ACLs), shape and primary key before reading its row. Lock the table against
concurrent DDL, recheck it, then read the approval and grant only schema usage and execution of
`enforce_deadline()` and `check_deadline_request()`. Verify exact source hashes,
signatures, language, invoker security, volatility, search path, ownership,
absence of public execution and the resulting serving permissions. A test ties
the provider's hashes to the embedded runtime bodies and canonical API limits.
The installer and provider serialize using the existing schema-owner advisory lock.

No policy table or matching binding/resource row preserves manual behavior.
Malformed table metadata or an incompatible matching approval fails closed.
Failure rolls back this attempt's role creation and grants; the provider returns no material
and the binding service does not replace the sealed secret or revoke the old
credential. Owner repair and rotation retry resume the same generation. Successful
preparation uses the existing rolling refresh/retirement lifecycle unchanged.
The provider never installs, repairs or adopts hooks, copies arbitrary RPC grants,
changes runtime opt-in, or creates an additional serving pool/probe.

This enrollment supports databases without coordinated SQL refresh gates. Reject
enrollment and matching-policy preparation if `gregale_data_api.refresh_gate`
exists, and reject enrollment in coordinated-refresh mode. Its per-login pause
and fingerprint state cannot safely be copied while the predecessor still serves:
preparation commits before secret publication and runtime activation. Existing
manual hook/gate setup remains supported. Unenrollment is allowed after adding a
gate so owners can return to manual setup. A later coordinated transfer protocol
must account for both active generations and activation/retirement boundaries.

## Consequences

[ADR-1667](1667-data-api-coordinated-refresh-rotation.md) subsequently adds explicit
shared-gate enrollment for supported coordinated-refresh bindings. This ADR's
deadline-only approval remains independent and must be removed before that setup.

Deadline-only enrolled bindings retain SQL deadline protection through credential
rotation without a manual grant window. The owner still chooses which bindings
are enrolled and must coordinate hook upgrades and arbitrary RPC permissions.
Other providers and unenrolled bindings retain their prior behavior. Owner edits
after validation remain an operational coordination concern. No hosted deployment
or provider-wide rollout follows automatically from local validation.

## Validation

Real PostgreSQL tests exercise active RLS traffic during replacement preparation,
both user scopes, private-table/RPC denial, idempotent preparation, old-session
retirement and replacement survival. Drift cases cover source, definer mode,
owner, search path, public execution/schema/table/column grants, hidden RLS/view
rows, incompatible hashes/version and coordinated refresh. Failed transactions
leave no candidate role and preserve the previous password; owner repair permits
retry. Binding-service tests verify no publication or revocation on preparation
failure and stable binding identity across generations. Native Node tests exercise
the actual owner installer, read-only checks, explicit removal and permissions.
