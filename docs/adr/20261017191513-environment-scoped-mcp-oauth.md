# ADR 20261017191513: Environment-scoped MCP OAuth connections

Status: Accepted (app-wide readiness policy limitation superseded by ADR 20261017191514)

Each catalog connection role binds to one immutable deployment `scope`. Omission
means `default`, preserving existing connections. Role names remain unique per
app (the existing 32-role limit); use names such as `staging-reader` and
`production-reader`. Moving an existing role to another environment is rejected
in both stores and by PostgreSQL. Create a new role instead.

All static tokens, refresh sources and client secrets resolve in the connection's
scope, with no fallback to default secrets. Guided authorization, renewal leases,
recovery journals and disconnect remain fenced by that role and revision. Secret
changes invalidate and retire only roles in the affected scope. Durable retirement
records preserve their original scope, including app deletion and lost renewal
responses. Provider revocation holds remain account-wide for the same provider
client, since a provider may revoke related grants across environments.

Readiness monitor `oauth_connections` maps environment scopes to pinned connection
roles/revisions. It excludes `oauth_connection` and `secret_ref`. The existing
single reference is still supported; its optional `scope` defaults to `default`.
A missing environment binding fails closed. Probes and admission evidence select
only the deployment's environment, so staging connection changes cannot invalidate
production evidence. OAuth-bound evidence must be refreshed after this migration
because the binding fingerprint now includes environment identity. Policies remain
app-wide: editing a policy (including a pinned connection revision) still requires
fresh evidence for every deployment under the new policy revision.

Managed catalog observers for nondefault environments resolve a live deployment
in that exact scope and use the existing guarded deployment preview URL builder.
Unavailable previews or mismatched resolutions fail before unsealing credentials.
Readiness probes continue using their specific deployment's preview.

CLI setup accepts `--scope`; control/reconnect actions use the existing immutable
role, so they cannot silently change scope. Dashboard connection rows, health
metadata and retirement records expose the environment without exposing tokens.

The migration captures predecessor function definitions for rollback, refuses
rollback with environment bindings or unresolved scoped retirements present, and preserves later definitions on
replay. No automatic traffic movement or provider calls occur during migration.
