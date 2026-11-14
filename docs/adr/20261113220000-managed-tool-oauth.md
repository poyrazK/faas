# ADR-20261113220000: Managed OAuth connections for private tool execution

Status: Accepted
Amends: ADR-20261113200000

## Decision

APID may renew owner-provisioned OAuth credentials for private tool invocation.
Execution connections are separate from catalog monitoring and readiness OAuth
grants. An unscoped personal administrator creates an immutable connection with
the application/default environment, issuer, token endpoint, client, resource,
exact scopes and existing ordinary `secret:MCP_*` refresh/client secret references.
HTTPS endpoints have no userinfo, query or fragment. The initial preview supports
public clients (`none`) and confidential clients (`client_secret_basic`). Setup
is declarative; it performs no provider request or browser authorization.

The connection freezes source ciphertext hashes and an authorization digest.
An exact version/tool/key invocation grant binds that digest. Routine access and
refresh token generations do not change it. Changing provider configuration,
source ciphertext, application/project identity or approved authority requires
a new connection UUID and owner-reviewed grant. Token scope changes fail closed.
The MCP application remains responsible for validating audience and permissions.

APID owns `tool_oauth_connections` and all credential exchanges. Access and latest
refresh tokens are encrypted in the Account seal domain, with the connection
authorization digest as the envelope namespace. Application/runtime identities
cannot open this renewed state. A distinct staged Account identity is required;
new execution state never uses a fleet fallback or dual-recipient rollout. The
initial refresh token and optional client
secret remain ordinary app secrets and can reach trusted app code through the
existing environment mechanism. Owners must trust that application and use a
separate provider grant/source for each execution connection. This preview does
not add a secret vault or deduplicate refresh grants across connections.

Before an exchange, APID atomically validates the current exact grant/key,
project, app and sources, then persists a 45-second exclusive renewal lease.
Other callers receive a conflict. A successful token response must be saved
before its access token can be used. Persistence uses a bounded context even
after request cancellation. An unsuccessful, lost, malformed or scope-changing
response requires owner reconnection. An expired lease also requires reconnection;
it never authorizes replay of a possibly consumed rotating refresh token.
This rule survives API restart. There is no automatic provider or tool retry.

Provider requests use the guarded egress client, a 15-second budget, no redirects
and a 64-KiB response bound. Renewal starts within 30 seconds of token expiry.
Store grant, source, connection and dispatch guards serialize authority changes;
a revoked connection cannot accept a late renewal result. Dispatched remote
effects cannot be undone. Database transaction failures retain the durable lease
or reconnect state, so a failed save never grants access to unsaved credentials.

The owner API and CLI expose configuration, generation, expiry and state without
credentials or leases. The agent-only status projection requires the exact
active invocation grant and independent private-invocation consent; it omits
provider settings and secret references and never refreshes. Readiness metadata
does not prove current provider availability. Known access/refresh/client secret
reflections are redacted before result journaling; arbitrary transformed secrets
and private application data still need application controls.

Local revocation clears stored renewed credentials and fences subsequent
execution. It retains metadata and does not revoke the provider grant, delete
source secrets or cancel already dispatched external effects. Reconnection means
obtaining fresh provider credentials through the owner's existing process,
creating a new connection and approving new exact grants. Browser login/PKCE,
provider revocation, discovery and additional client authentication methods are
deferred. Existing static access-token grants retain their original contract.

Retained connections are bounded to 128 per account, 32 per app and 20 per page;
configuration is at most 8 KiB, metadata 16 KiB and ciphertext 128 KiB. Existing
private worker and invocation quotas continue to apply. Limits live in
`pkg/api/limits.go`; revoked records count toward retained capacity.

## Verification and release

Portable tests use a local TLS OAuth provider and authenticated MCP application
with memory and PostgreSQL stores. They exercise rotating refresh tokens,
confidential client encoding, unchanged grants, account-domain isolation,
restart recovery, ambiguous exchanges, lease expiry, concurrent acquisition,
revocation and known-secret redaction. API/CLI/MCP contracts are checked separately.
These tests provide no deployed staging or real-provider acceptance evidence.
A deployed acceptance run is required before claiming staging readiness.

Deploy the expand migration before APID and matching CLI/bridge updates. Private
execution continues to require the existing disabled-by-default operator gates
and separate connection consent. No VM lifecycle behavior changes.
