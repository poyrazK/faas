# ADR-930: Application producer-key publication reconciliation

Date: 2026-10-09
Status: Accepted

## Context

An unanswered app-key publication can already have committed. Repeating a publish after receipt pruning can create another acceptance. Producers need a read-only path to inspect retained evidence without another append.

## Decision

Add GET `/v1/apps/{slug}/events/publish-status?key=KEY`, `gregale events publish-app-status APP --key KEY`, and Go/Node/Python SDK methods. Resolve the owned app using apps:read/admin scopes, MFA and existing rate limits. Share producer-key validation and the exact source/event-ID derivation with publication. Keys remain exact and case-sensitive; raw keys and payloads are not returned.

Accept only one nonempty key, after and limit query value, rejecting unknown parameters, repeated keys, malformed encoding and invalid limits. Default recipient page size is 100, maximum 200 (existing receipt limit); cursor size is bounded at 8192 bytes. New operational bounds live in pkg/api/limits.go. Use the existing receipt cursor, bound to account/source/event-ID and outbox acceptance identity; stale or mismatched cursors fail validation. App renames preserve identity, replacement apps do not.

Read the existing EventReceiptStore snapshot with a five-second request deadline and no-store responses. Return app_id, source, event_id, observed_at, status and receipt_url. If retained, include the original publication receipt and the existing rich evidence response, preserving global routing counts, bounded recipient rows, execution/workflow outcomes, missing execution evidence and recovery history/actions. Pagination is live per-page evidence, not a frozen cross-page snapshot. No current mutable handler result is promoted to proof of original execution success.

Status processing means retained acceptance with routing_settled_at absent. Status accepted means retained acceptance whose routing is settled, including filtered/failed routing outcomes; it does not imply successful consumer delivery or completed execution. Both statuses prove durable acceptance. Consumers may continue processing after routing settles. Snapshot membership can be unknown for legacy rows and remains explicitly snapshot_captured=false.

If EventReceiptStore returns ErrNotFound, return HTTP 200 status unavailable with reason not_retained_or_not_observed, no receipt or evidence, and the deterministic identity/receipt URL. This cannot distinguish never accepted, concurrent acceptance not yet visible, or pruning. Missing apps still return 404; other read failures remain errors, not unavailable. Never publish, select recipients, claim work, refresh retention or automatically retry missing keys. The CLI always emits JSON, returning 2 for unavailable and 0 for retained acceptance; exit 0 is not a delivery-success check.

Reuse all storage and existing SQLC queries. No migration, new persistent fields or clone-registry classification is needed. The root Go SDK uses existing typed receipt DTOs; the standalone Go SDK preserves recipient rows as lossless JSON because it does not yet mirror the full receipt DTO family. Generated Node/Python SDKs reuse the existing typed receipt schema.

## Consequences

Producers can reconcile retained publication safely without an additional POST. Evidence disappearance remains uncertainty, not rejection or successful execution. This endpoint does not extend deduplication retention or prove that caller-supplied content matches the retained event; publication remains the content-conflict authority. No tests are added or run under the user's standing instruction, and spec-compliance registries remain unchanged.
