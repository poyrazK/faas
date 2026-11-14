# ADR-20261113130001: Hosted invocation through exact tool grants

Status: Accepted
Amends: ADR-20261113100001 (discovery-only grants), ADR-20261112220001 (local invocation)

## Context

Personal hosted agents can discover an owner-granted tool but cannot invoke it.
Requiring deployment permission would broaden their authority unnecessarily.
The existing invocation journal already records one dispatch and preserves
uncertainty after worker failure; a second execution service would duplicate it.

## Decision

Reuse the bounded trusted Gregale CLI worker on the hosted bridge. Add native
`/v1/delegated-tool-invocations/{id}` GET/PUT and `/{action}` POST routes with
apps-read scope and exact active invocation-grant enforcement. Mutations retain
native MFA policy. APID remains the only customer-intent writer; no component
gains VM lifecycle ownership, a daemon shortcut, or arbitrary command execution.

The operator opts in with `--allow-tool-invocation`, disabled by default, and
includes `invoke` in the offered toolsets. Browser OAuth requires the separate
`gregale:tools:invoke` scope and an explicit final consent checkbox. The native
browser key can remain apps-read only. `gregale:write` and deployment actions
are independent. Existing OAuth grants gain no invocation permission on upgrade
or refresh. Personal headless agent keys require operator enablement and an
owner-issued invocation grant, with their native key acting as their credential.

Every call explicitly names the immutable version, tool, grant UUID, retained
request UUID, open access and execution consent. Only open tools are supported
by this hosted path. External OAuth token custody and application/tenant or
shared-organization delegation require separate designs. Tool annotations do
not imply authorization or absence of side effects.

## Native authority and concurrency

APID freezes grant, connection key and project identities in an optional
`delegation` field of the existing journal payload. The exact current key,
active allow-invocation grant, app ownership and frozen project must match on
every receipt read and mutation. The connection's current project limit can
only narrow the grant. Registry descriptions include only that key's active
grant IDs, expiry and invocation ceilings for the exact described tool.

Insertion and dispatch check authority within storage synchronization. Memory
uses the existing mutex; PostgreSQL takes shared locks on the grant, key and
application and a journal row lock in the same transaction. Revocation and
dispatch serialize at those locks. Once dispatch wins, revocation cannot undo
remote effects. Repeated dispatch never returns another dispatch grant.

Only the original key and exact still-active grant may read runtime results
through the delegated route. Another grant, a rotated key, expiry or revocation
does not inherit receipt access. The owner retains historical inspection and
cancellation through the account journal. That route refuses delegated dispatch
and outcome reporting, so owner authority cannot bypass the delegation fence.

## Execution and recovery

Only a fresh native `created` fact starts a worker. The bridge launches its fixed
trusted CLI with `mcp invocation-run --delegated --id <UUID>`, passing bounded
canonical arguments on stdin. HostedCLIRunner uses its existing clean private
home/environment and the recipient native credential, never the operator's
credential. Arguments are transient in the bridge and worker; the native intent
stores their digest, never an argument body or token. Worker stdout is not
execution evidence. Sanitized complete result data is retained in the journal.

The worker reloads the granted single-tool descriptor and exact native target.
It verifies deployment source, live status, preview identity, the complete
runtime catalog digest, the selected single-tool projection, and every current
replica's published catalog pin. The native bearer never goes to the application;
redirects remain disabled. Only then may native dispatch authorize tools/call.

There is no durable argument queue, lease takeover, restart resume or automatic
tool retry. Lost creation replies leave a retained queued attempt without a new
launch grant. Lost dispatch replies can leave running evidence without a call.
Lost tool replies can mean effects occurred. Deadline views become `not_started`
before dispatch or `outcome_unknown` after it. Reusing the same request UUID
recovers the existing fact and never executes again. Changed intent or grant
under that UUID conflicts. New attempts require an explicit new identity and
reconciliation of possible earlier effects.

Cancellation before dispatch prevents the call. After dispatch it requests
stopping the worker's wait, with no remote rollback guarantee. The worker polls
native cancellation/authority; revoked access stops the wait and may prevent
reporting a final result. The owner can still inspect the retained fact.

## Bounds, compatibility and evidence

Reuse the native limits: 32 KiB arguments, 64 KiB retained sanitized result,
four active calls and 1024 retained identities per account, a 1–600 second total
worker budget (default 60), and 30 seconds of reporting grace. The bridge adds
no unbounded worker pool; existing hosted CLI execution slots and lifecycle
draining apply. Workload admission, native rate limits and compute billing apply.

The existing journal's bounded JSON payload accommodates optional delegation;
no new migration or main Store method is required. Native API and worker updates
must precede enabling the bridge flag. Older bridges remain discovery-only.
Owner and delegated workers reject mismatched execution modes. The capability
remains preview and does not change existing hosting maturity or quotas.

Portable acceptance covers memory and migrated PostgreSQL authorization,
concurrent single dispatch, project middleware, both supported MCP versions,
OAuth consent without deployment scope, exact catalog/pin refusal, lost replies,
cancellation, revocation, result redaction and recovery without repeated effects.
These fixtures do not establish deployed runtime availability or KVM acceptance.
