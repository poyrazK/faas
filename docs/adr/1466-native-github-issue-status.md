# ADR 1466: Native GitHub connections for issue status

Status: Accepted

## Decision

Reuse the existing GitHub App installation/OAuth flow and app repository binding for native issue status reads and pushes. Add GET/POST on `/v1/apps/{slug}/issues/{issue_id}/tracker-links/{link_id}/native-status`, typed clients and `issues tickets sync --native`. Keep installation credentials on the server. Linear native OAuth, native ticket creation and continuous callbacks remain separate increments.

Require the caller to own the app's durable installation binding, with positive pinned repository/owner identities, a current account-owned installation row and a matching repository ID/name in the installation's current catalog. Limit access to an existing reference in the current issue group, pointing to the bound repository. A legacy binding without immutable IDs requires rebinding. Never borrow another account's GitHub grant for a shared app. Use githubd's existing token-mint RPC after validating the scope; use fixed provider HTTPS origins and bounded, sanitized status requests. Recheck membership and binding around credential acquisition and status reads.

Reads require app read permission and Issues entitlement. Pushes require deploy-write and the normal session MFA posture. The requested remote lifecycle must match the current canonical Gregale lifecycle; the body includes an expected provider snapshot. Recheck local identity/lifecycle/binding and remote status before writing, and verify the resulting ticket identity and state. Return only connection identifiers and normalized status, never token values, expiry material or provider error bodies. Share the existing 15-second provider operation budget.

## Consequences

App owners can preview, push and pull connected GitHub status without local GitHub credentials. Pull still uses Gregale's existing lifecycle API and requires fixing deployment attribution for resolution. Existing deployment/source-control connection management remains authoritative; no new credential table or duplicated OAuth flow is introduced. Members of shared apps cannot implicitly use an owner's provider grant. Repository identity changes, removal from the current installation catalog, unbinding and group/reference changes stop operations. Separate provider/control-plane operations still have race windows after final checks; no atomic cross-provider transaction or continuous synchronization is claimed. Installation permission rejection is surfaced rather than silently falling back to a personal token.
