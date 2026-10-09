# ADR-725: Fair workflow schedule admission, history, and transient retries

- **Status:** accepted
- **Date:** 2026-10-07
- **Decision:** Scheduled tenant/workflow pairs compete for their app's existing
  concurrency quota in least-recently-admitted order. Admission timestamps
  survive skips, cadence changes, deployments, run retention, and scheduler
  restarts. Every evaluated due minute records a started, skipped_overlap, or
  skipped_quota occurrence atomically with its cursor and any new run.
  App admission locks canonicalize UUID spellings and acquire both historic
  forms in a fixed order for compatibility with older writers. App handlers
  retry HTTP 408, 425, and 429 within the existing attempt budget,
  honoring a bounded Retry-After deadline through the internal gateway.
  Unsafe outbound actions retain their existing no-retry policy.
- **Why:** Fixed tenant ID ordering can indefinitely deny the same customers
  when schedules share a quota. A latest-outcome cursor hides earlier skips.
  Throttled app actions currently terminate instead of waiting for capacity.
- **Consequences:** Fair scans return one workflow per tenant candidate, bounded
  by the central batch limit, and exclude cursors already evaluated this
  minute. Non-due tenant evaluations advance the scan timestamp while
  preserving their latest occurrence. Paused schedules do not consume scan
  capacity. Concurrent replicas still serialize admission on the existing
  app lock and cursor fence. Fairness controls admission order; it does not
  guarantee capacity or catch up missed minutes. History is retained for
  30 days, excludes input/output and secrets, and remains inspectable after
  runs expire. App-scoped history is authenticated through existing read
  scopes. Workflow failure counts, quota skips, pending age, and wait age are
  durable alert signals using the existing alert-rule system.
- **Rejected alternatives:** Hash tenant IDs each minute (no bounded starvation
  guarantee); increase quotas (does not fix unfairness); retain only counters
  (cannot explain an individual skip); retry every 4xx (validation and
  authorization failures are terminal).

Retry-After supports seconds and HTTP dates and is capped at one hour. The
scheduler persists the later of its ordinary backoff and the downstream
hint. Attempts keep the same step idempotency key. Exhausted transient 4xx
responses remain failed, rather than being classified as transport failures.
Ordinary handlers remain at-least-once; customers must deduplicate effects.
Definition snapshots still do not pin handler code. Catch-up policies,
scheduler heartbeats, deployment pinning, and a visual authoring UI remain
separate changes.
