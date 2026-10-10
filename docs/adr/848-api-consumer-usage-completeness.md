# ADR-848: Usage completeness check for app consumer statements

- **Status:** accepted
- **Date:** 2026-10-09
- **Decision:** `GET /v1/apps/{slug}/consumers/{consumer_id}/usage-completeness?since=&until=` compares a consumer's billing ledger with request telemetry. The comparison is hour by hour and counts only successful requests (`request_count − error_count` in the ledger, `status < 400` in telemetry). It reports `confirmed_requests = Σ min(billed, seen)`, `missing_requests = Σ max(0, seen − billed)` and a status:
  - `gaps_detected` when anything is missing;
  - `unverifiable` when billed requests exist but telemetry has none;
  - `partial` when telemetry confirms fewer requests than were billed;
  - otherwise `verified`.

  The check is read-only and stores nothing. `gregale consumers completeness` shows it. `statement-draft` and `statement-finalize` warn on stderr when it reports `gaps_detected`.
- **Why:** The usage ledger can silently lose a request if the gateway crashes after the response but before the outbox write (ADR-843 limits). Operators had no way to see that before invoicing. Request telemetry is written by a separate path from the same requests, so it is independent evidence.
- **Consequences:**
  - **What a gap means.** Telemetry is sampled under pressure, can be turned off per app, has no event IDs and is kept for 14 days. It can therefore prove a lower bound of missing usage, but never that every request was billed. A positive gap is real evidence, since telemetry saw more successful requests in that hour than were billed. Fewer telemetry requests than billed is expected and reported as `partial`, not as an error.
  - **Unbilled requests.** Admission denials (ADR-847) and platform failures (ADR-843) are errors, so excluding errors on both sides keeps them from looking like gaps. A guest 4xx is billed but excluded from both sides alike.
  - **Window.** Only whole UTC hours are checked: hours that ended at least 10 minutes ago, since telemetry publishes asynchronously, and that fall within the 14-day retention. A window entirely outside them returns `unverifiable` with an empty checked range. The request window is capped at 90 days, like usage reads.
  - **Advisory only.** The check never blocks finalization. Blocking on a lossy signal would stop invoices for apps that disable telemetry. The warning is best effort: if the check fails, the CLI command still succeeds.
  - **Cost.** The Postgres query uses the existing `(app_id, consumer_id, received_at)` telemetry index and aggregates per hour. The in-memory store keeps a seedable table for tests.
- **Rejected alternatives:**
  - Storing a completeness verdict on each statement revision would freeze a lossy, retention-bound signal into the immutable financial record and need a migration. Recomputing on demand reflects telemetry as it is.
  - Matching individual requests would need a shared request ID in both the outbox and telemetry, plus telemetry without sampling. That costs too much on the hot path for a reconciliation aid.
  - Comparing all requests, including errors, would report every rate-limit rejection and platform failure as a discrepancy.
