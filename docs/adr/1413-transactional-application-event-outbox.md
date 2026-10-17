# ADR-1413 · Transactional application event publication outbox

Date: 2026-10-15
Status: Accepted

## Context

An application can commit business state and crash before publishing the event.
Producer keys already support recovery of uncertain acceptance while receipts are
retained, but application-owned durable publication intent is missing. Slug reuse
must not redirect recovery into a replacement application's namespace.

## Decision

Provide opt-in PostgreSQL DDL and Go SDK helpers to stage immutable publication
intent in the caller's business transaction, inspect local status, and recover one
due row per call. Follow the existing application-owned SDK transaction helpers:
use fixed parameterized SQL, validated statically with SQLC, without adding a
platform Store dependency or runtime driver. These local queries are an explicit
exception to the platform SQLC-generated Store convention.

Use random recovery claims, SKIP LOCKED, database-clock leases, HTTP outside the
transaction, and live-token-fenced settlement. Preserve the exact saved request
and producer key. Bound uncertainty retries to an immutable maximum seven-day
window, shorter than normal 30-day acceptance retention for new keys. Block expired
or terminally rejected work for operator reconciliation rather than minting keys.

Add an optional expected_app_id field to the existing publication request and
apply it to both publication and content verification before acceptance/comparison.
Mismatches return 409. Existing callers omitting the field retain current behavior.
The outbox always supplies the canonical saved app UUID.

## Consequences

Business state and publication intent commit atomically, while remote acceptance
remains a separate recoverable step. Multiple workers can progress independently;
no ordering or indefinite exactly-once guarantee is introduced. Applications own
worker lifetime, database permissions, payload retention, business idempotency,
and reconciliation after exhausted evidence. No production migration, platform
clone entry, daemon worker, or notification transport is added.

See [usage, recovery and retention](../event-outbox.md).
