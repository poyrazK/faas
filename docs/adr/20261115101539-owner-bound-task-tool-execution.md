# ADR-20261115101539: Owner-bound local Task tool execution

Status: Accepted
Amends: ADR-20261115082509

## Decision

Compose the existing native Node Task deployment adapter with a separate,
single-use execution authorization in the durable Task review journal. An
approved review alone still grants no execution authority. An unscoped personal
non-agent administrator API key authorizes one immutable operation ID and salted
digest of a private local binding profile. Bind the profile to the API, account,
intent, operation, exact plan path and environment values. Never return its path,
salt, credential values or the executor's claim token through agent tools.

The authorization inherits the original 15-minute review deadline. Repeated
authorization never renews or replaces it. Only the original requesting key may
claim/guard/finish the operation. The native store serializes claim transitions
with the existing memory mutex or PostgreSQL row lock, checks current requester
and authorizer authority, and pins app ownership, identity and configuration.
Initial claim also rechecks the original latest/serving baselines. Subsequent
guards allow deployment revisions produced by the adapter; its existing exact
generation, serving-baseline and readiness fences still govern traffic and park.

The trusted local CLI generates and privately retains a random claim token;
only its digest enters the journal. Another claim cannot obtain a replacement
lease. A repeat apply inspects retained state. Running, completed and uncertain
claims never automatically restart, including after management-server restart.
Failure marks `reconcile_required` where possible. Process death can leave
`running`; neither state permits automatic takeover. Existing owner-operated
native inspection, restoration and retirement remain the recovery authority.

## Bindings and execution

The owner supplies a bounded mode-0600 JSON binding file through the CLI, not an
MCP tool. The retained mode-0600 profile lives outside both deployment sources
under the private Gregale configuration directory. It permits only environment
references selected by the reviewed Task configuration and release policy, plus
the fixed namespace/migration/observer bindings. It cannot override CLI/API
credentials, executable paths or Node loader settings. Changing any profile
value requires another review and execution identity.

After claiming, a trusted read-only PostgreSQL preflight requires the runtime,
schema-owner migration and observer roles to reach the same database/schema,
separates their identities, rejects privileged runtime/observer roles and owner
membership, and checks runtime access and observer read-only privileges. The
schema must already be initialized by the owner; first provisioning is excluded.
This verifies the supplied local bindings. The owner's pinned app configuration
and the adapter's live namespace/observer/worker gates remain responsible for
the deployed stack; the preflight is not proof of every remote secret value.

Recompute source and policy and recheck native authorization before migration,
each deployment, promotion and every previous-worker park. Reuse all existing
Task compatibility, retained-key, admission, worker receipt, live catalog,
release policy, conditional promotion and conditional park gates. Completion
requires the adapter's recorded deployment identities, promotion and worker
retirement, followed by a final fresh authorization check. No migration rollback
or automatic cleanup follows a failed release.

Local `build-tools` exposes asynchronous `gregale_apply_task_tool_deploy` with
explicit execution consent and `gregale_get_task_tool_operation` for reconnect.
Transport disconnect does not cancel the lifecycle-tracked worker; shutdown and
the fixed deadline do. The worker shares existing bounded publication capacity.
Hosted, organization and app-scoped connections cannot list or use these tools.
Go/Python Task rollouts, named environments, registry publication and invocation
authority are excluded. Root `ready_to_apply` and `bindings_verified` remain false;
the nested execution journal represents this narrower separate authorization.

## Validation

Exercise single claims, duplicates, wrong claim tokens, expiry, changed source
or bindings, requester/owner revocation, native app drift, owner/requester
separation, memory/PostgreSQL persistence, both MCP protocols, local private-file
handling and interrupted real CLI execution without repeat effects. Preserve
existing native release and recovery assertions. Regenerate preview clients and
run schema parity, SQLC, OpenAPI and ADR checks. Live staging remains excluded.
