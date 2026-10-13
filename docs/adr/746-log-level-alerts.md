# ADR-746 · Log-level line counts and log alerts

- **Status:** proposed
- **Date:** 2026-10-09
- **Decision:** Count every guest log line by level where it first lands —
  the per-instance `logbuf.Ring` in vmmd — and alert on those counts:
  1. **Counter.** vmmd classifies each committed line with the same level
     heuristic `gregale logs --level` already uses (now shared as
     `pkg/loglevel`) and increments
     `vmmd_app_log_lines_total{app_id,level}` for `level` in `error` and
     `warn`. Info and unclassified lines are not counted.
  2. **Alerts.** Two app-scoped alert metrics, `log_error_lines` and
     `log_warn_lines`, observe `sum(increase(vmmd_app_log_lines_total{...}[window]))`
     across every node. They are ordinary threshold rules (webhook, rollback,
     demote, promote), evaluated by the existing alert evaluator.
  3. **Patterns (follow-up slice).** Customer-defined literal substrings —
     at most a handful per app — delivered with the wake request and counted
     under a `pattern` label, so an alert can watch `payment declined`
     rather than all errors.

  Neither part needs a feature flag. The vmmd counter is passive, and a rule
  created before the counter reaches a node observes zero and stays `ok`,
  which is also the correct verdict for an app that logs no errors.
- **Why:** "Too many error logs in five minutes" is the most common log
  monitor teams configure in external tools, and Gregale has no answer short
  of forwarding logs to one. Logs exist only in per-instance ring buffers and
  the daily S3 archive — there is no indexed store to query — so the only
  place a count can be both complete and cheap is where lines are written.
  `app_errors` (ADR-096) groups HTTP 4xx/5xx responses, which misses errors in
  background work, queues, crons, and handled exceptions that still return 200.
- **Consequences:**
  - **Cost:** classification is a lowercase plus a fixed set of `Contains`
    checks over at most the first `LogLevelScanBytes` (512) of each line, on
    a path that already copies the line into the ring. No allocation for
    lines shorter than the scan window beyond the existing lowercase copy.
  - **Cardinality:** two series per app that has logged an error or warning
    on a node since vmmd started, capped at `LogLineCounterMaxApps` (10,000)
    apps per vmmd. Past the cap, lines count under `app_id="other"`; an app
    in the overflow bucket evaluates as zero, which the counter's help text
    and `docs/alerts.md` state. The cap is two orders of magnitude above the
    live-instance ceiling of one node.
  - **Heuristic, not parsing:** level detection is the existing
    conservative substring set (`[error]`, `level=error`, `"level":"error"`,
    …). A line that says `error` in prose is not an error line; a JSON line
    with `"severity":"ERROR"` is. Matching `gregale logs --level error` means
    the alert and the command the operator runs next agree.
  - **Restarts:** a vmmd restart resets its counters; `increase()` handles the
    reset, at the cost of losing increments not yet scraped.
  - **Ownership:** vmmd stays the only component touching the ring; it gains
    a callback, not a dependency. No customer input reaches vmmd in slices 1–2.
- **Rejected alternatives:**
  - *An indexed log store (Loki, VictoriaLogs).* The right long-term answer
    for search, but a new stateful service with its own retention and
    capacity budget; alerts on counts do not need it.
  - *Counting in gatewayd-internal's log drain path.* Only sees apps with a
    configured drain.
  - *Counting from the S3 archive.* Hours late; the spool only receives
    lines evicted from the ring.
  - *Customer regexes.* Regex evaluation in the root daemon's hot path is a
    denial-of-service surface; literal substrings bound the cost per line.

## Slices

1. `pkg/loglevel` classifier, shared by schedd's `LevelMatcher`; vmmd counter.
2. `log_error_lines` / `log_warn_lines` alert metrics, CLI, OpenAPI, docs.
3. Literal log patterns (separate ADR amendment before work starts).
