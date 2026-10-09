# ADR-844: App-wide recovery notification retry backlog

Date: 2026-10-09
Status: Accepted

## Context

Job-level retry history requires knowing each recovery job ID. Operators need to discover unresolved requests across an app without reading an unbounded amount of retained delivery evidence.

## Decision

Expose GET `/v1/apps/{slug}/event-recoveries/notification-retry-backlog` and `gregale events notification-retry-backlog APP`. Reuse the original-generation outcome calculations and read-only snapshot helpers from job history. Default statuses are failed, pending, and inconclusive; an explicit distinct status union can include succeeded. Require read scope and MFA, account/app ownership, existing rate limits, no-store responses, strict query validation, and a five-second evidence deadline.

Paginate retained jobs containing saved retry requests by created_at descending and ID descending. Each page inspects five jobs by default, at most ten, and returns at most 1,000 request summaries. Within each job, preserve decision-time descending and request-ID ascending ordering. Query one extra job to determine continuation. Bind the cursor to account, app, endpoint, and canonical status selection. Page size can change between requests. Missing/pruned cursor boundary jobs do not prevent continuation because the cursor contains the ordering tuple.

Return app_id, observed_at, jobs_scanned, counts_scope=job_page, totals, matched_count, requests, and optional next_cursor. Totals include all saved requests in scanned jobs before status filtering; they are never whole-app totals across unseen pages. Return continuation even for a page with no matching requests. Jobs without saved retry decisions are excluded. Each request includes job identity/time, its existing outcome summary, and paths to request detail and the job's retry preview. Retention gaps remain unknown rather than failure or success.

Postgres validates the owned live app and reads the bounded job page plus all its evidence in one read-only repeatable-read transaction. Memory uses one lock and the equivalent shared history helpers. Pages are independent live snapshots: newer jobs are discovered by restarting from the beginning, and earlier job outcomes can change after a page was read. Do not sum page totals as a frozen whole-app count while concurrent writes or pruning occur.

Expose the read in Go, Node, and Python SDKs and generated CLI help. No payloads, endpoint URLs, secrets, actor identities, or error bodies are returned. No automatic retry, delivery mutation, additional retention, migration, or snapshot-token storage is introduced.

## Consequences

Operators can discover unresolved requests across retained app jobs and inspect existing recovery surfaces. Bounded job pages keep reads finite, while explicit count scope prevents misleading aggregate claims. Empty filtered pages must be followed when next_cursor is present.
