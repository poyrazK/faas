# ADR-1459: Environment-owned cache policies

Status: Accepted

## Context

ADR-1458 introduced guarded MCP updates to shared app cache rules. Named
project environments inherited those rules, so an environment-scoped remote
grant could not safely authorize an isolated cache change. The native named
policy overlay previously owned only headers and CORS.

## Decision

Add an independent cache-policy collection keyed by app ID and immutable
project environment ID. Only registered named environments are supported.
Absence preserves app cache inheritance. Saving a complete bounded rule list
replaces every inherited cache rule on that environment's immutable host;
an explicit empty list disables caching there. Other hosts, environments and
rule kinds are untouched. Rules are unconditional and enforced, with concrete
globs, distinct priorities, and the existing validated cache action vocabulary.
Host selection and rule IDs are platform-owned.

Expose GET/PUT `/v1/apps/{slug}/cache-policy?environment=...`. Writes bind app,
project, environment, expected policy revision and a stable request UUID.
Serialize on the native account/app locks and retain the live environment row
through commit. Commit desired policy and its request-hashed receipt in the
same transaction. Replays recover the original response even after subsequent
edits; conflicting request reuse fails. Receipt lookup follows live ownership
and immutable identity checks. Environment deletion cascades policies and
receipts; reusing a slug cannot inherit either. MemStore mirrors those rules.

Gateway rule loading overlays cache independently of headers/CORS. It fails
without a valid policy reader instead of silently assuming inheritance. The
existing compiled policy cache expires after 30 seconds; this initial API does
not participate in edge-rule fleet convergence acknowledgements. Synthetic
cache-rule IDs include app, environment and policy revision, so newly loaded
settings cannot reuse response-cache entries from an earlier revision. The
ordinary authentication, cookie, method and origin cacheability bypasses remain
in force. A receipt confirms desired intent, not gateway observation.

Add MCP read, plan and apply tools for this collection. Plans pin exact account,
app, project and environment identities and the override revision for 15
minutes. Apply rechecks current write consent, including on saved receipts.
Durable bridge journals retain reviews and attempts across restart; the native
transactional receipt recovers lost responses. Existing shared app rule tools
retain their unrestricted local access requirement. Scoped route advice now
observes this overlay and labels its metadata `environment_policy`.

## Consequences

Remote environment grants can change staging cache behavior without acquiring
app-wide authority. Concurrent reviews cannot overwrite newer policy. Gateway
updates are eventually observed through normal cache expiry; operators must
apply the database migration before deploying readers, then deploy the new
gateway implementation before relying on isolated policy enforcement. No new
fleet convergence claim or native qualification is implied.

This collection is independent of manifest/GitOps headers/CORS intent. Existing
environment clone operations do not copy it; a new environment starts with app
inheritance until explicitly configured. Restoring inheritance, declarative
ownership and clone integration are future additions. Native default scope
and shared cache rules keep their existing APIs and consent model.
