# ADR-1542: Managed OAuth credentials for MCP readiness

Status: Accepted (environment scope limitation superseded by ADR 1543)

Continuous readiness checks (ADR-1541) previously supported anonymous access and app-secret token references. OAuth-protected MCP deployments also need expiring access tokens, refresh-token rotation, revoked-grant handling, and recovery after an uncertain provider response.

A readiness role can now reference an existing app-local catalog OAuth connection by role and exact revision. `oauth_connection` and `secret_ref` are mutually exclusive. The referenced connection must be enabled, unpaused, configured for OAuth, and use the same MCP endpoint path. Connection references use the default environment scope because the existing managed OAuth connection and its grants are app-local in that scope. Other environment scopes cannot consume the grant. Existing managed-connection plan eligibility applies.

Both scheduled readiness monitoring and explicit platform probes use the managed credential when a reference is present. Explicit probes reject a caller-supplied probe token for these roles. The CLI release flow omits its local probe credential when recording evidence for a managed OAuth role; its existing local authorization checks still use the locally configured credential.

Credential acquisition takes the same durable observer lease as catalog checks and OAuth renewal. It ignores the catalog's next scheduled observation time, but does not change that schedule or claim that a catalog observation occurred. Concurrent consumers cannot refresh the same connection together. Lease contention leaves the readiness attempt for retry rather than counting a credential failure. Persistent contention remains detectable as stale readiness monitoring.

The existing observer OAuth implementation performs renewal and persists rotated tokens before use. The same journal and recovery handling apply to lost exchange responses, saved responses awaiting installation, revoked grants, and reauthorization. An unsafe exchange is never retried merely because readiness needs a token. The lease remains held until the readiness result is recorded, then is released; crashes retain the existing bounded lease recovery.

Readiness subjects now include the referenced connection revision, enable/pause state, authorization state, grant identity/source, granted scopes, and authorization-flow identity. Token ciphertext, expiry, routine renewal timestamps, and lease IDs are excluded. Ordinary renewal preserves evidence for the same grant. Reconfiguration, disconnection, changed grants, or reauthorization invalidate old evidence and reject in-flight results. PostgreSQL completion and traffic-admission paths lock the connection row while checking the subject, including direct SQL traffic increases. Roles without an OAuth reference retain their previous subject hash.

Fixed monitoring reasons distinguish `oauth_reconnect_required`, `oauth_connection_changed`, and `oauth_credential_unavailable`. Status responses and alert summaries include the connection reference and a recovery action. The MCP dashboard links back to connection health and authorization controls. Completing a reconnect changes the connection revision; an administrator must explicitly update the readiness reference to approve that new connection.

No refresh token, access token, ciphertext, or provider error text is added to policy responses, readiness observations, dashboard data, or webhook payloads. This increment does not add credential export, automatic deployment traffic changes, or OAuth connections shared across apps/environments.

The migration preserves later definitions on replay. Rollback requires removing current readiness OAuth references before restoring the original runtime-only readiness subject and traffic guard.
