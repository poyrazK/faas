# ADR-246: Cross-app platform tenant request activity

**Status:** Accepted
**Date:** 2026-09-25

## Context

Platform tenants provide one stable customer identity across a platform's apps. Operators can inspect durable usage and finalized billing statements, and verified request-time tenant identity is already carried through the gateway telemetry protocol. However, the per-request debugger store does not retain that attribution, so support requires manually correlating app-local views and must not infer historical ownership from current consumer links.

## Decision

- Persist the verified `platform_tenant_id` on request-debugger telemetry at request time. Historical rows remain unattributed; current resource links are not used to backfill identity.
- Add a bounded account API that returns recent retained telemetry across one platform tenant's apps, with optional app and HTTP-status filters and a keyset cursor pinned to its tenant, filters, and time window.
- Apply the existing debugger-plan retention limit and account read/MFA authorization. The query is always scoped by both the owning account and immutable request-time tenant attribution.
- Return only bounded debugger metadata and platform-owned guest evidence. Never return request/response bodies, HTTP headers, API credentials, or raw error text.
- Keep this surface explicitly diagnostic. Telemetry is sampled, can be dropped by plan/rate limits, and is retention-bound; durable usage and statements remain the accounting source of truth.

## Consequences

Platform operators can triage one customer's recent errors across apps without cross-customer queries or manual correlation. Older requests do not appear unless their request-time tenant identity was persisted; changing consumer or surface links cannot rewrite attribution. Page totals describe only the returned telemetry rows and must not be used as billing totals.
