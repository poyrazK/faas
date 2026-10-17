# ADR-1457 · Labeled Issues grouping evaluation

- **Status:** accepted
- **Date:** 2026-10-13
- **Decision:** Evaluate the real grouping normalizer against explicitly labeled
  examples through `cmd/issues-grouping-eval`. Report pairwise false merges and
  splits, same-family precision/recall, and group counts for each supported
  strategy. Use app/environment namespaces so unrelated projects cannot inflate
  false-merge counts. Pair counts use frequency intersections, not quadratic
  comparisons.
- **Why:** Feature coverage does not establish grouping accuracy. A reviewed
  corpus makes caller, code, wrapper, and fallback behavior measurable and gives
  regressions an explicit expected-family reference.
- **Scope:** The initial 19 cases are synthetic Node, Python, Go, HTTP fallback,
  override, and namespace examples. They are not representative production
  incidents. Their perfect cause-aware result is not an empirical accuracy claim.
  Capture IDs/timestamps are synthesized for identity evaluation. Read-only
  symbolication stamps are omitted; mapped evidence is retained. This does not
  evaluate symbolication correctness, ingestion delivery, or missing captures.
- **Bounds:** At most 10,000 cases and 8 MiB of corpus JSON. The offline developer
  tool has no network or database dependency. Outputs contain aggregate counts,
  not event messages or labels. Missing denominators omit precision/recall.
- **Follow-up:** Add reviewed, sanitized real incidents and platform-specific
  frameworks before using these measurements for rollout decisions.
