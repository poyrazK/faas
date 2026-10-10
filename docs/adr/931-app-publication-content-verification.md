# ADR-931: Read-only application publication content verification

Date: 2026-10-09
Status: Accepted

## Context

Lookup by app and producer key proves retained acceptance but does not compare the producer's original intent. A reused key can therefore refer to different content. Producers need to verify an original request without attempting another publish.

## Decision

Add POST `/v1/apps/{slug}/events/verify-publication`, `gregale events publish-app-verify APP --file ORIGINAL_JSON`, and `VerifyAppEventPublication` in both Go clients plus generated Node/Python SDK methods. POST carries a potentially large original JSON body, but the handler is strictly read-only. Use owned-app authorization, apps:read/admin scopes, MFA, existing request rate limits, no-store and a five-second deadline. Reuse publication's exact producer-key validator, identity derivation, strict single-object decoder and 1 MiB body bound. Reject query parameters. The CLI shares its strict bounded file reader with publishing.

Normalize the original publication envelope, then compare type, schema version and semantic JSON data according to the existing account/source/ID identity rules. Occurrence time and trace metadata are excluded, as in publication. Do not consult current schema registration or admission rules: previously accepted content remains verifiable after registry changes.

Refactor retained lookup into a shared comparison operation returning Matches and AcceptedAt. PostgreSQL uses existing SQLC EventStorageIdentity and EventReceiptAcceptedAt queries in one transaction holding the retained identity row's share lock. Memory compares under its existing mutex. A content mismatch still returns the original acceptance timestamp. Existing publication lookup delegates to comparison and preserves its ErrConflict/ErrNotFound behavior. Publication and verification cannot drift in content equality rules.

Return HTTP 200 with status match, conflict or unavailable, app_id, source, event_id, observed_at and receipt_url. Both match and conflict include the retained original acceptance receipt from that same snapshot. Conflict is a comparison observation, not an attempted write or a new receipt. Unavailable includes reason not_retained_or_not_observed and no receipt; it cannot distinguish never accepted, pruning or concurrent acceptance not yet visible. Other read failures remain errors. No raw keys, supplied data or stored data are returned, and no current consumer outcome is inferred.

The CLI always emits one JSON observation and exits 0 for match, 1 for conflict/read/validation error, and 2 for unavailable. Do not automatically publish on any verification result. The existing status/receipt surfaces provide consumer routing and execution evidence separately. Key identity and receipt retention follow the existing app publication contract, including replacement-app UUID namespaces and key reuse after actual pruning. This does not prove continuity with a pruned earlier acceptance.

No append, fanout, lease, replay, retention refresh or short-lived idempotency response cache is introduced. Existing comparison queries and storage suffice; no migration or clone-registry classification is required. No tests are added or run, and spec-compliance registries remain unchanged under the user's standing instruction.

## Consequences

Producers can confirm or reject retained content matches after a lost response without generating another event. Equality is the current publication contract rather than byte-for-byte JSON formatting equality. Missing evidence remains uncertainty, and matching acceptance remains distinct from successful consumer side effects.
