# ADR-847: Application-scoped producer-key event publication

Date: 2026-10-09
Status: Accepted

## Context

Account/source/event-ID publishing already deduplicates retained identities. Producers that operate as an application need a stable key namespace without choosing and persisting generated event IDs after an uncertain response.

## Decision

Add POST `/v1/apps/{slug}/events:publish`, Go `PublishAppEvent`, generated Node/Python SDK support, and `gregale events publish-app APP --file PATH`. Resolve the owned app with existing authorization, MFA and events:publish/deploy:write/admin scopes and request rate limiting. Limit the JSON body to 1 MiB and keys to 1..256 exact printable ASCII bytes without whitespace. Keys are case-sensitive and never normalized. Operational limits live in pkg/api/limits.go.

The canonical source is `app.<canonical app UUID>` and ID is `key.<lowercase SHA-256 hex of key>`. The account remains the tenancy boundary. App renames retain key identity; recreating an app creates a different UUID namespace. The raw key is not retained in the envelope or receipt. Source and event ID remain ordinary router identities: account-scoped legacy publication of the same source/ID addresses the same event. This is a convenience namespace, not an authorization boundary between producers within one account.

Return app_id, source, duplicate and the ordinary acceptance receipt, with HTTP 202, Location and no-store. Same app/key and identical normalized type, schema version and JSON data returns the original accepted_at without extra fanout or storage charge. Different type/schema/data conflicts with HTTP 409. Occurrence time and platform trace extensions preserve first-publication metadata and are excluded from duplicate content comparison, consistent with existing event identity semantics. JSON comparison uses existing JSONB/memory semantic equality.

Look up existing content using the existing SQLC EventStorageIdentity and EventReceiptAcceptedAt queries in one transaction holding the retained row's share lock; memory uses its existing mutex. Identical retained content returns before current ingress schema admission. New events pass current schema validation and use existing atomic acceptance: PostgreSQL account publication serialization and memory mutex, transactional outbox, subscription capture and storage capacity checks. A concurrent lookup miss does not reserve an identity: the acceptance critical section remains the final authority and returns duplicate/conflict as appropriate.

Use a five-second request deadline. If acceptance cannot be confirmed, return a retryable capacity problem instructing the caller to preserve app/key/content. Do not put this endpoint behind short-lived request-wide Idempotency-Key middleware. Only new acceptance emits a scheduler wake hint; durable outbox polling recovers missed hints. No recipient selection is implicit: existing account subscriptions match the generated source and may fan out to several consumer apps, independently of the producer app.

Retention follows existing receipts: settled routing becomes eligible for pruning after 30 days; unsettled work and retention holds can extend this period. Retries do not refresh retention. After actual pruning, the same key can be newly accepted and distributed again. There is no permanent deduplication or exactly-once consumer-side effect claim. No new persistent fields, schema migrations, SQL queries or clone-registry classifications are required.

## Consequences

Producer retries can recover a durable original receipt with only the original app and key. Independent consumers keep existing retry/replay/dead-letter behavior. Subscriptions and schemas use the generated app.UUID source. App deletion removes this ingress route; retained receipts remain accessible through existing account receipt APIs. Builds, vet, SDK generation and lint verify integration; no tests are added or run under the user's standing instruction.
