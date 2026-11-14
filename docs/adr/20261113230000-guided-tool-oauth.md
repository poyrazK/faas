# ADR-20261113230000: Guided OAuth for execution connections

Status: Accepted
Amends: ADR-20261113220000

## Decision

Add owner-only browser authorization to execution connections. The first guided
mode supports public clients with authorization code plus S256 PKCE, explicit
HTTPS authorization/token endpoints, an exact resource and exact scopes.
Discovery and confidential guided clients are deferred. Existing manually
provisioned connections retain their source-secret contract and binding digests.
Monitoring/readiness connections and their consent remain separate.

A guided intent has `guided: true`, `authorization_endpoint`, no refresh/client
secret references and `token_auth_method: none`. Its source digest is a versioned
mode marker. The immutable authorization digest still covers account, app,
project, connection UUID and complete provider configuration. The initial state
is `authorization_pending`; a grant cannot be issued until credentials have been
installed. Provider consent does not itself delegate any tool to an agent.

`tool_oauth_authorizations` retains one attempt per account/connection, an expiry,
hashed state and an Account-domain encrypted envelope containing the random
nonce, verifier and exact callback URI. The envelope namespace includes the
connection authorization digest. The attempt expires after five minutes. Only
the personal administrator under MFA policy may begin, complete or inspect it.
These routes never appear in the agent MCP catalog.

Begin is idempotent for the same unexpired attempt and callback URI; it returns
the retained consent URL. A different callback URI conflicts. The CLI listens
only on IPv4 loopback, validates the exact host/path/state and optional issuer,
and accepts one callback even after its delivery channel has been drained.
The authorization URL must match the reviewed endpoint, resource, client,
callback, scopes and S256 parameters before the CLI opens a browser.

Completion validates current owner/app/project authority, consumes the nonce
once, removes the persisted verifier envelope and records an exclusive 45-second
connection lease before the provider request. A code exchange runs through the
guarded HTTP client with the existing 15-second budget, response/token bounds
and no redirects. The provider must issue a refresh token, a Bearer access token
and supported expiry; a returned scope change is refused. Successful credentials
are sealed directly into Account-domain connection state and saved before use.
They are never added to app environment secrets or returned in public responses.

Finishing uses a bounded context after request cancellation. Interrupted or
ambiguous issuance, scope changes, expired attempts/leases and failed saves
cannot authorize another code exchange. A process restart retains consumed
attempts and never resumes issuance. Local revocation clears credentials and
pending verifier state and rejects late exchange completion. Uncertain issuance
may leave a provider grant requiring manual retirement; provider revocation is
outside this preview.

`connect --id UUID` authorizes a new guided connection. Repeated connect on an
already connected identity reads its status. `reconnect --id NEW --from OLD`
checks the frozen app/project identity, creates the new intent, locally revokes
the old connection and begins fresh provider authorization. App/project or
provider configuration changes require explicit new configuration review. A
replacement connection requires new exact tool grants; old grants never migrate.
Routine token renewal preserves the original approval as before.

## Verification and release

Local TLS provider tests exercise S256 exchange, exact callback/resource/client
bindings, no app-secret source, encrypted token/verifier isolation, explicit
granting, refresh with unchanged grants, callback replay, changed scopes, denied
consent, lost/oversized/redirected responses, revocation and restart recovery
against memory and PostgreSQL stores. CLI tests cover browser callbacks and
replacement identities. Portable evidence does not establish live provider or
deployed staging acceptance; the live run remains skipped.

Deploy the expand migration before the matching API and CLI. Existing private
invocation gates and independent agent consent remain required. Authorization
setup alone enables no execution. No VM lifecycle or scheduler ownership changes.
