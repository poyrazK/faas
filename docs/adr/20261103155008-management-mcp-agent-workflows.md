# ADR-20261103155008: Complete the management MCP agent workflows

Date: 2026-11-03
Status: Accepted
Amends: ADR-1239, ADR-20261026141841, ADR-20261026152009,
ADR-20261026174727, ADR-20261026195952, ADR-20261029085359

## Context

The hosted default excluded implemented release and diagnostic tools, including
the advertised Git deployment path. Configuration tools used the secrets CLI
and were local-only. Agents could provision dependencies but could not restart
the app, inspect their bindings or recover the receipt of a completed upload.
The journal saved temporary plans which its own restore code rejected.

## Decision

1. The hosted default permits all toolsets, subject to existing app,
   environment, diagnostic and mutation consent. Browser connections can select
   a smaller catalog before sign-in. The selection survives restart and cannot
   expand the operator's configured maximum. Add `operate` for configuration,
   restart, scaling, plan receipts and costs; keep a bounded core catalog.
   Plan-producing groups include receipt lookup, and provisioning includes the
   common operation applier for its reviewed domain and cron changes.
2. Never journal temporary files or legacy configuration inputs. Ignore old
   unresumable temporary records rather than poisoning the connection. Keep
   pending temporary plans in memory during idle cleanup. Journal completed
   receipts stripped of temporary inputs, including the state needed to promote
   an uploaded staged release. Headless journals use the account and complete
   credential hash as their namespace; no bearer token is stored there.
3. Configuration plans use the native non-secret variable API locally and
   remotely. Add explicit `env:write` and `usage:read` consent. Sensitive names
   and credential-like values are refused; values never appear in plan results
   or configuration reads. Private non-secret operation inputs are sealed in
   the existing bridge journal. History and rollback reuse native config guards.
   Each variable write checks observed revision state, but a batch is not atomic.
   An ambiguous batch requires inspection rather than an automatic overwrite.
4. Restart, scaling, cron edits/deletion and domain removal use expiring plans
   and the existing typed API. Plans pin app/environment identity and observed
   configuration. Native named-workload revisions retain their atomic guard;
   default-scope configuration and resource fingerprint checks are read-before-write.
   They do not add compare-and-swap semantics to native endpoints that lack them.
   Restart keeps the
   native idempotency key and durable wake status. Protected named targets use
   their approved native workflow. Receipt retries never repeat a known write.
5. Add bounded app-scoped domain, cron, bucket, storage-binding and bound-database
   inspection. Projected results exclude credential values, domain challenge
   tokens and unrelated app resources. Cost reads reuse the CLI's app-attribution
   projection and preserve native coverage limitations.
6. Provisioning can return immediately with a `plan_id` status handle. Detached
   workers preserve OAuth authorization expiry and participate in OAuth revocation
   and graceful shutdown. Native calls recheck headless credentials. Their
   process-wide bound follows the four CLI slots.
   Cancellation does not reverse accepted native mutations. Completed receipts
   are durable; unfinished temporary inputs still require native state inspection
   and replanning after process death.
7. App-bound headless credentials extend the existing deploy-token principal,
   not an MCP-only authorization overlay. `agent=true` adds the agent/read identity
   and permits only app-safe scopes. Existing deploy tokens keep their old scope
   set. The native middleware permits an app agent to read minimal principal and
   capability metadata while retaining its immutable app boundary on every app
   route. Include these credentials in agent-connection listing and revocation.
   Per-environment delegation continues through OAuth's existing registry-bound
   consent; app-bound keys do not acquire project-registry access.
8. Advertise output schemas for stable result envelopes and per-operation retry
   hints. Validate these envelopes without a floating-point JSON round trip:
   large integer telemetry must remain exact in both text and structured output.

## Compatibility and rollout

Deploy the expanded deploy-token CHECK before issuing app-agent tokens. Upgrade
apid before the bridge: extended browser consent uses an `MC2` prefix with two
hex scope digits, while all existing `MCPR`, `MCPW` and one-digit scoped codes
retain their original hashes and permissions. Old apid rejects the new format.
Old unresumable journal records are discarded without granting new permissions.

No changes bypass native account ownership, app identity, environment protection,
plan entitlements, secret entry or quota checks. Functions, initial multi-service
project creation and arbitrary job execution retain their existing CLI workflows.

## Validation

Exercise upload/provisioning restart recovery, promotion after upload recovery,
legacy bad journals, key-journal cleanup, variable scans and revisions, stable
restart/scaling receipts, read-only consent, catalog narrowing and persistence,
asynchronous cancellation, and cross-app native credentials. Apply the migration
to an isolated PostgreSQL schema and keep the canonical schema digest current.
The deployed `TestMCPAppAgentCredentials` journey covers CLI minting, native
authorization, hosted variable plans and receipt recovery, and revocation through
connection inventory. It requires a configured journey target and credentials.
