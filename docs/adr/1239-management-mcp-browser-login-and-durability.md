# ADR-1239: Native browser authorization and durable management MCP connections

## Status

Accepted. Extends ADR-1238.

## Context

A remote agent connection needs limited native onboarding, app-specific consent,
restart-safe refresh and release retries, and an atomic staging check. API-key
pasting and ephemeral plans make routine reconnects difficult. The existing
Gregale dashboard/device-code flow already enforces authenticated account choice,
CSRF, expiry and exactly-once credential exchange.

## Decision

Extend the device-code mint with optional `mcp-read` / `mcp-write` purposes. MCP
codes carry 256 random bits and a purpose prefix included in their stored hash.
Changing purpose changes lookup identity; exchange cannot widen a read code.
Normal CLI codes and their existing behavior remain compatible. MCP exchange
issues an eight-hour `mcp-login` key with `apps:read` and optional `deploy:write`,
using the dashboard session's account. Browser onboarding is the bridge default;
API-key onboarding is an explicit fallback. The bridge reviews agent, account,
callback, read/write access and all-versus-selected apps. Selected slugs bind to
app IDs and guard every app- and deployment-targeted tool; account listing is
filtered. Writes retain default-scope image/rollback semantics.

Offer an opt-in single-writer encrypted file journal configured with a separate
operator key. AES-256-GCM authenticates the complete state with a versioned AAD.
Private directory/file permissions, an exclusive lease, atomic replacement and
fsync are required. Withhold OAuth responses until state commits. Store clients,
flows/codes, grant credentials, access/refresh lookup hashes, refresh replay
history and grant-owned release plans. Persist a mutation's attempted state before
sending its stable idempotency key and body. API/endpoint and app/grant bindings
must match during recovery; policy narrowing invalidates prior write grants.
Storage failure disables further access. Revocation removes grant-owned plans.

Add an authenticated default-scope release-review API returning an opaque app
configuration hash plus exact latest and serving IDs. The configuration hash
covers persisted app intent, excluding observed placement/scaling fields. Hashes
are local to their storage backend and are not portable signatures. Read the
review from one database snapshot; recheck it after locking the app in the same
transaction as deployment creation. A changed review returns
`409 release_review_changed` before inserting or superseding a candidate. The
review is admission metadata and is never persisted on deployment rows. MCP
stages only at explicit 0% traffic and never falls back on older APIs. Promotion
and rollback keep their existing transactional guards.

Emit structured audit events for authorization and write tools with server-owned
identifiers. Credentials, browser codes, tool arguments and logs are excluded.
Keep operator ingress rate limits and HTTPS/Host/Origin protections.

## Consequences

Connections and unexpired reviewed plans survive process restart when configured
with durable state. The journal supports one process, not shared multi-replica
storage. Backup rollback is not an anti-replay recovery protocol; reset state and
reconnect after disaster recovery. Native account choice uses existing dashboard
sign-in, not a new multi-organization picker. Fine-grained named-environment grants,
source/Git deployment planning and service/worker releases remain separate work.

A repeatable real Codex app-server acceptance check discovers and invokes read
tools without a model call. Local fixture evidence does not claim Claude Desktop
or live-account acceptance. Those require the client/test-account environment.
