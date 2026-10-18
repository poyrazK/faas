# ADR 1463: Native issue tracker references

Status: Accepted

## Decision

Store references to existing public GitHub and Linear issues in Gregale. Expose listing, idempotent saving, title updates and deletion through issue detail pages, the management API, CLI and SDKs. This increment performs no provider requests, ticket creation, account connection or status synchronization.

Require HTTPS on github.com or linear.app, validate the provider-specific issue path, and canonicalize host, project identity and issue number while discarding query strings, fragments and Linear title slugs. Reject credentials, custom ports, encoded path separators and pull request URLs. Titles are optional, escaped at rendering and limited to 256 bytes; URLs are limited to 512 bytes.

Attach references to original issue identities. Reads aggregate through the existing canonical group assignments. Merging combines references; splitting returns each original identity's references to its resulting group. An upsert through any alias searches the current group and retains an existing reference's original identity. Duplicate references inherited from separately linked groups remain independently removable.

Serialize mutations with the app lock and cap new references at 20 per current group. Preserve inherited references when a merge exceeds the cap; updates and removals remain possible. Paginate lists at 50 references with a cursor bound to the app and canonical group. Require Issues entitlement and app read permissions for lists, and deploy-write plus session MFA for mutations. Dashboard mutations use the existing Issues CSRF protection. Record linked, updated and unlinked activity transactionally without generating lifecycle notifications. Exclude operational tracker metadata from workload clones. Migration rollback refuses to discard stored references or their audit history.

## Consequences

Triage now has durable, native tracker destinations with attribution and regrouping provenance. A stored reference does not prove the provider issue exists or that the viewer can access it. Provider-managed creation and bidirectional state synchronization require a separate integration and delivery model, including credentials, permission checks, retry semantics and status mapping.
