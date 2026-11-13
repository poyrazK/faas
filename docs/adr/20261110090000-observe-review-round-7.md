# ADR-20261110090000 · Gregale Observe review, round 7: what production showed — honest statuses and error counts, alerts that announce new incidents, logs that keep secrets and long lines

- **Status:** proposed
- **Date:** 2026-11-10
- **Builds on:** [ADR-20261109153000](20261109153000-observe-review-round-6.md)
  (Observe review, round 6), ADR-020 (sealed secrets, spec §17 G2),
  ADR-082 (availability SLI), [ADR-747](747-custom-slos-error-budgets.md),
  ADR-127 (request analytics), ADR-20261030115139 (platform 504 shown as 503)
- **Context:** Rounds 1–6 read code. Round 7 deployed throwaway apps on
  production (rc.332, 2026-11-09/10) and drove every Observe command. It
  found defects that reading had missed.
- **Decision:**
  1. **Long log lines keep the stream.** The SSE decoders in `pkg/api` and
     the Go SDK accept lines up to `api.SSEMaxLineBytes` (8 MiB). A 200 KB
     line ended `gregale logs` with `token too long` and lost every later
     line.
  2. **Parked logs honour `--level`.** A parked app's retained and archived
     lines are filtered with the same level detection as live lines
     (`pkg/loglevel`). `--level` was ignored there, and with `--grep` it
     returned nothing.
  3. **Bidirectional controls are escaped** in terminal output (`\uNNNN`),
     like other control characters. U+202E could reorder what an operator
     read.
  4. **`latency_ms` 0 is reported.** A log-search event's latency is a
     pointer, so 0 ms appears in `--json`. The trace waterfall draws a span
     from `timestamp - latency` to `timestamp`; it drew completion as start.
  5. **A breach after a recovery fires at once.** The cool-down applies
     only while an episode is still firing. A new incident inside the old
     cool-down was never announced, and the receiver got a second resolve.
  6. **Webhook deliveries leave the evaluation path.** Each account sends at
     most `api.AlertAccountWebhookConcurrency` (4) webhooks at a time off the
     rule loop, so a receiver that hangs no longer delays the account's other
     rules past the tick budget. `delivered_at` is when the receiver
     answered, and a delivery whose retries ran out of budget names the
     receiver's last status.
  7. **A request the client abandoned records 499.** When the client's
     context is cancelled before any header is written, the gateway records
     499 in telemetry and metrics instead of 200. It is a client error: not
     good, not a server error.
  8. **Rollout probes leave customer request series.** Canary and readiness
     probe traffic no longer increments the app's request metrics; it
     inflated counts tenfold during a deploy and diluted error-rate alerts.
  9. **Availability SLOs and SLO burn read the status-class series.**
     `gateway_request_duration_seconds_count{class=…}` exists from an app's
     first request, so `increase()` counts its first 5xx. The per-code
     `gateway_requests_total` series did not exist before the first error of
     a code, and an hour with three 500s read a burn of 0.00×.
  10. **Request analytics keep their documented error basis.** They count
      every status ≥ 400 (MCP `error_basis: http_status_gte_400`); alerts,
      SLOs and app health count 5xx out of 2xx + 5xx. The basis is a
      contract, so it is not changed. The CLI now prints
      `Errors (4xx+5xx)` and `docs/log-search.md` explains the two counts.
  11. **Sealed secret values are masked in app output.** Spec §17 G2 decided
      that values are redacted from app logs; production printed them
      verbatim. guest-init, which holds the cleartext, wraps the main
      workload's output pipe in a streaming redactor before the serial
      console. Values of at least 8 bytes become `[REDACTED:KEY]`, including
      a value split across writes or spanning lines. The redactor holds back
      at most one value's length. Runtime secret reloads add values and keep
      old ones. Shorter values (`true`, `8080`) stay readable. Sidecars,
      jobs and app tasks are not covered yet.
  12. **Legacy numeric hosts are refused.** An outbound URL whose host is not
      a canonical IP but ends in a numeric label (`2130706433`, `127.1`,
      `0x7f000001`) is refused at create time. Go's resolver does not read
      these forms, so the egress check saw no address and saved a rule that
      targeted 127.0.0.1. Delivery already refused it.
  13. **Readiness maintenance says what it does and what is wrong.** Help
      says it silences MCP readiness notifications only; alert rules still
      deliver. Validation names the field (`--reason is required`, `start is
      in the past`, …) in the CLI and in apid's 400 detail. `list` and
      `create` print text unless `--json`.
  14. **Smaller CLI fixes.** `alert update` help lists every flag it takes,
      including `--enabled`; `alert check status|pause|resume|delete` accept
      a check's name; `debug issue get` prints text unless `--json`; the
      Observe journey's tail step follows `gregale logs --follow` and checks
      a marker line, where it watched async invocations.
- **Not changed:** these need a design or production access and are
  recorded for a later round:
  - **stderr is labelled `stdout`.** guest-init merges the app's stdout and
    stderr into one pipe before the single serial console. Separating them
    needs a line-tagging protocol between guest-init and vmmd, with
    version-skew handling for guests in existing snapshots.
  - **No spans or linked logs on production**, and no traceparent
    propagation for ESM Node apps. The guest bridge path (127.0.0.1:4318 to
    vmmd) needs tracing on a node.
  - **Inbound `traceparent` is honoured** from any client. Trace lookup is
    scoped to the caller's apps, so a client can only join its own requests
    to a trace id it already knows.
  - `debug status` counts only the serving deployment; durations print in
    Go's `335h12m59s` form; the custom-metric and readiness-failure errors
    lack a hint and the probed path and status.
- **Consequences:** No schema change, migration or OpenAPI change. Metrics
  gain the 499 status and lose probe traffic, so request counts around
  deploys drop and error rates around deploys rise to their true value.
  Availability SLO and burn values change for apps whose 5xx codes were
  first seen inside the window. Apps that print their own sealed values see
  `[REDACTED:KEY]` from the next wake on a new runtime image.
