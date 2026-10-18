# ADR-1656: Default-off GitHub push automation for internal CI

- Status: implemented behind default-off gates
- Date: 2026-10-16
- Extends: ADR-1572, ADR-1654 and ADR-1655

## Decision

Associate an account-owned GitHub binding with an immutable execution profile
and explicit exact push branches. This policy permits new App-authenticated
branch pushes to create fresh Jobs executions through existing admission.
Repository pipeline files, tags, PR events and fork code cannot select execution
configuration. An operator selects the profile and branches; this does not
assert that GitHub branch protection is enabled. Branches must be a subset of
the binding's push filters. No deployment or production flag enablement occurs.

Policies have a stable UUID, one retained row per account/binding, and a
monotonic generation. At most the existing 16 retained bindings/account can
have policies. Identical saves preserve identity/generation. Replacing or
re-enabling a policy advances its generation; disabling it is idempotent.
Setup rechecks source ownership, active account plan, profile state, binding
generation and current materialized template images, with compute flags off.
All policy routes use internal account policy, authenticated admin scope, MFA,
the configured HTTPS origin and no-store responses. Setup additionally needs
GitHub opt-in; read/history/disable remain available after admission is disabled.

## Authenticated capture and no promotion

`FAAS_CI_AUTOMATION_ENABLED` must be exactly `1` in githubd to capture automatic
intent and in apid to admit it. The new setting is `0` in every shipped unit.
Internal/GitHub gates and the account allowlist remain mandatory. Only the
existing App-level HMAC ingress calls the capture seam, before acknowledging
the durable webhook. Legacy installation-scoped signers remain excluded.

The new `ci_automation_events` journal contains the immutable source SHA,
binding snapshot, selected policy/profile and generation, with no commands or
credentials. Under the account lock, capture checks both it and the original
`ci_github_triggers` journal for the logical event identity. Delivery UUIDs do
not change that identity. A source already recorded in either journal can
never change admission mode, including after flag changes, policy replacement,
redelivery or daemon restart. Old blocked receipts remain blocked.

Flag-off events and events without an eligible active policy retain metadata
admission behavior. Same-repository PR events can still produce metadata
receipts under their existing binding filters; automation is push-only.
Both journals share the existing 256 retained snapshots/account and 64 KiB
snapshot bound. No second event allowance or unbounded source queue is added.

## Admission, fencing and recovery

The apid execution-worker lifetime also owns a bounded automation admission
worker. It requires automation, internal/GitHub, dispatch, worker and transport
gates, managed storage, the HTTPS origin and source RPC readiness. It performs
no native action. schedd and vmmd retain their existing ownership boundaries.
Source access is freshly verified, and ordinary profile admission revalidates
the account, binding and all pinned images. Existing execution quotas apply.
A capacity/provider failure in one account does not stop attempts for others.

Events use 60-second leases, unique claim tokens, PostgreSQL time, eight maximum
attempts and persisted capped backoff, matching the existing GitHub worker
bounds. Provider failures and temporary capacity errors retry; authority loss
is ignored permanently. Exhausted rows become dead. Public history exposes
only safe metadata and stable error classifications. Worker logs redact
provider/database error strings. This release has no dead-letter redrive.

The execution and event acknowledgement commit in the same account-serialized
transaction. Admission rechecks the current policy/profile, policy generation,
binding snapshot and unexpired claim. The final acknowledgement repeats the
lease check; its failure rolls back execution insertion. A lost response after
commit leaves an admitted event and one durable execution. Stale owners cannot
admit, replace another owner's retry schedule, or create another attempt.
Execution identity includes the event and policy provenance, so an explicit
manual request cannot reuse an automatic request's idempotency key.

Policy replacement/disable, profile disable and binding disable invalidate
pending claims under the account lock. Re-enabling policy cannot revive ignored
events. Admitted executions retain their frozen authority and continue ordinary
recovery/cancellation. Turning off the global automation flag pauses admission
of already captured automatic intent; disable the policy to revoke it. Worker
shutdown joins admission before storage closes. No blocked receipt is promoted.

The additive migration refuses rollback while any automation event remains:
terminal rows are still deduplication authority. Keep the journal during binary
rollback; removing it requires a separate reviewed reconciliation procedure.
The prepared native qualification candidate remains frozen as an earlier source
revision. This change does not alter VM lifecycle or publish execution Checks.
Real queued/running/terminal GitHub Checks are a subsequent implementation slice.

## Portable verification

PostgreSQL/race tests cover cross-journal deduplication, capture without compute
admission, revoked authority during verification, profile/image changes,
expired/reclaimed leases, bounded provider/capacity retries, and rollback when
the source lease expires between execution insertion and acknowledgement.
Signed HTTP tests reject unsigned ingress and deduplicate distinct delivery IDs.
Authenticated HTTPS tests configure policy, admit through the apid worker,
disable the profile/policy, restart, and cancel the same execution without
native work. API/CLI tests cover explicit scope and account isolation.
