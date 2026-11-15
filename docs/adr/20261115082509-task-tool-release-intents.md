# ADR-20261115082509: Durable Task stack release intents and owner review

Status: Accepted
Amends: ADR-20261115072125

## Decision

Add an apid-owned Task release-intent journal, independently of execution and
single-app publications. Pin the native source/policy fingerprint, exact existing
web/worker/observer/previous-worker app IDs, default-environment configuration and
latest/serving deployment reviews, tool permissions, endpoint, metrics destination
and required owner setup. All apps must be active and owned by the same personal
account. Node workers and the modern Task protocol remain the supported scope.

Bind the immutable caller-chosen intent ID and complete intent digest to the
native account and requesting key. Preparing the same ID retrieves its original
record; changed reviews conflict. Retain at most 256 identities per account,
including terminal reviews, without eviction or reuse. Expire reviews 15 minutes
after preparation. Decisions and retries never renew that deadline.

Owner approval, rejection or revocation names the exact intent digest and source
fingerprint. Only an unscoped personal administrator or authenticated owner
session may decide; agent and MCP-login credentials are excluded. Reload cached
credential and project restrictions for each request. Memory and PostgreSQL
stores verify requesting-key authority, personal app ownership and native review
snapshots during preparation and checks. PostgreSQL locks the requesting key and
app rows while changing the journal, ordering app locks by native ID.

## Agent and owner workflow

Local `build-tools` adds `gregale_prepare_task_tool_deploy`, read-only
`gregale_get_task_tool_deploy` and `gregale_check_task_tool_deploy`. Prepare and
check need connection write consent. Hosted, organization and app-scoped management
connections cannot list or call these local capabilities. The agent catalog gains
no approval or execution tool. Another publishing key cannot inspect or invalidate
the requesting key's review; the owner retains inspection and decision access.

The CLI `gregale mcp server tasks intent` offers prepare, get, approve, reject and
revoke. Prepare, get and approve review the supplied local plan; CLI get checks
its fresh fingerprint and native app reviews. Owner decisions require
`--intent-sha256`. Rejection and revocation use the exact retained intent and
remain available without local files or live requesting-key/app authority.
Native GET reads retained metadata. POST check re-evaluates current facts and is
excluded from HTTP idempotency replay. Observed source or native drift permanently
marks an approval stale, including if the original inputs are later restored.

## Authority boundary

Approval covers a source and native-app review only. `ready_to_apply` and
`bindings_verified` remain false after approval. Database and key setup is recorded
as fixed requirement categories, not verified resources or credential references.
There is no apply, migration, fixture, registry publication or invocation method.
Existing apply cannot consume this record. The owner-operated release gates remain
unchanged. Paths, executable arguments and credential values are excluded.

The API cannot watch local files: approval binds their reviewed fingerprint.
Native GET is the last recorded observation, not a new source/binding check.
Future execution must recompute source and policy, revalidate app, credential and
binding identities, enforce fresh readiness and consume separately defined
authority atomically. Review approval cannot substitute for those checks.

## Validation

Cover digest binding, fixed deadlines, repeat-safe preparation and decisions,
revocation, permanent stale states, memory/PostgreSQL owner separation and
reconnect, cached key revocation, native deployment drift, CLI review without
Node, MCP consent and both protocols, malformed/executable receipts, schema
parity, SDK route coverage, generated clients and migrations. Live staging and
maturity promotion are excluded.
