# ADR-922: Recovery notification retry generation outcomes

Date: 2026-10-09
Status: Accepted

## Context

Retry decision history preserves the generation originally queued, but current delivery state may describe a subsequent retry. Operators need the outcome of their specific request without mistaking later success for earlier success.

## Decision

Extend the existing request detail API and CLI with retry_outcome, retained_attempt_count, attempt_count_complete, and optional completed_at. Skipped decisions are not_applicable. Queued decisions are succeeded or failed only when the retained attempt ledger contains a terminal succeeded or dead attempt for the requested generation. A matching current pending or in_flight generation is pending. Otherwise the outcome is unknown. The terminal attempt finish time is the completion time.

Count only retained completed attempts in the requested generation. A complete contiguous sequence from attempt one through the highest retained number establishes count completeness for a known outcome. Unknown outcomes always have incomplete counts; skipped decisions have a complete zero count. In-flight requests have not yet completed an attempt and do not contribute to this count.

Postgres reads the metadata-only aggregate through SQLC, scoped to the owned app, webhook, delivery, exact recovery source event, and requested generation within the existing read-only repeatable-read transaction. Memory uses the equivalent scoped delivery and source mapping under its existing lock. No payload, errors, response bodies, endpoint URLs, or secrets are returned. Existing authorization, bounded targets, request timeout, and retention apply. Go, Node, and Python models expose the fields.

## Consequences

Later retries cannot overwrite the displayed outcome of an earlier generation. Removed deliveries or ledger evidence remain explicitly unknown. No migration, durable receipt mutation, extra retention, or delivery behavior change is required.
