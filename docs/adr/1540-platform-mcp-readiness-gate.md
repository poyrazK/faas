# ADR 1540: Durable platform MCP readiness admission

This extends ADR 1539's client-side readiness checks. A client-provided success
flag or saved CLI report cannot authorize production traffic. The platform runs
the read-only probe itself, through a fixed deployment-preview origin, before
recording its result. The API body accepts only a configured role name. The
MCP-Probe-Authorization header supplies a separate scoped credential; it is
removed from the request and never stored in policy, evidence or audit records.
Production uses guarded DNS/IP egress, HTTPS and no redirects. App ownership and
app-scoped key authorization are resolved before any probe.

An opt-in app policy specifies endpoint, evidence age (60–3600s), and up to 32
roles with protocol version, canonical catalog format and expected SHA-256.
Admin scope and applicable MFA are required to configure/disable policy. Every
change uses an expected-revision CAS, creates a new UUID revision and preserves
the full policy plus reason/account in policy history. HTTP applications only
can enable policy through the API. Deployments must become live at zero traffic
so their public preview can be probed before traffic admission.

The server captures database time and a runtime subject before network work.
The subject hashes deployment identity, image/build/source/rootfs identity,
scoped runtime overrides, relevant app runtime/ingress configuration and scoped
secret delivery versions/value hashes. No secret plaintext is read. Evidence is
committed only if that subject and policy revision still match. A late probe
cannot overwrite a newer-started result. Each deployment/role retains its latest
result, including failures, so an earlier success does not mask a newer failure.
Result payloads contain fixed checks and hashes, never catalog definitions,
server response bodies or tokens. Freshness is measured from probe start.

A PostgreSQL BEFORE INSERT/UPDATE trigger guards any transition into positive
live traffic or increase of existing traffic. It reads the current policy and
requires every role's latest passing evidence to match the policy, subject and
maximum age. This includes ordinary deploy activation, traffic APIs, canary
steps, rollbacks and automated writes. A row can lose traffic without readiness; compensating increases for another
revision still require evidence or an override. Other admission and conditional-serving guards remain in force.
The memory store mirrors these checks in activation/traffic paths.

An admin can authorize an emergency override with the exact policy revision
and a required reason. The server binds it to the current deployment subject.
It expires after five minutes and permits one traffic increase. Only one matching unconsumed approval may be pending, so request retries cannot
stack duplicate overrides. Consumption is
part of the traffic transaction; rollback restores it if another guard fails.
The audit retains account, reason, binding, creation, expiry and consumption time.
Overrides do not bypass other admission guards. Policy changes and runtime
subject changes invalidate unused overrides.

The CLI adds `mcp readiness --record --role NAME`. MCP deploy records fresh
platform results for all enforced roles using the corresponding release-role
credentials before guarded promotion. Policy/state APIs and the MCP dashboard
show retained results, age/configuration mismatches and override audit. Snapshot
lists are bounded to the latest 50 evidence and 50 override rows for the selected
app/deployment. Policy and override audit live until app/deployment retention
removes their owners; evidence retains at most one row per deployment/role.

Readiness is a point-in-time protocol/catalog check, not continuous availability,
authentication-policy qualification, handler correctness, or evidence that all
replicas are healthy. Existing exact-replica and authorization checks still apply.
Background synthetic monitoring and provider login are outside this decision.
