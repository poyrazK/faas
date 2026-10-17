# ADR-1294 · CLI live automation verification

- **Status:** accepted
- **Date:** 2026-10-15
- **Decision:** Add `gregale automations live start|list|show|check|wait` over the
  existing scoped live verification API. Support explicit one-case admission,
  bounded observation and safe, versioned JSON results for deployment pipelines.
- **Why:** Live cases could be started and checked through the dashboard and SDKs,
  but a CI job lacked CLI admission, result checks and reconnection commands.
- **Consequences:**
  - Start requires an explicit saved case, automation version, published revision,
    deployment UUID, stable idempotency key and real-action confirmation. It sends
    the original fenced request without refreshing versions or generating keys.
    An unconfirmed admission is retried only with its original flags and key.
  - Start alone confirms admission. `start --wait`, `wait` and `check` require a
    terminal, passing report with both `report_current=true` and `outdated=false`
    for success. A deliberately expected failed/dead run may pass its assertions.
    `show` and `list` are inspection operations rather than verification gates.
  - Wait observes one run and checks its terminal frozen assertions if needed.
    An already current report avoids another check. A continuation observed during
    checking returns to observation; a terminal report still stale after checking
    stops as unavailable. Changed case/revision/deployment evidence stops as outdated.
  - Observation uses a 5-minute default deadline, a 1-hour maximum, a 3-second
    default poll interval configurable from 1 second to 1 minute, and 10-second
    per-request deadlines. Requests are sequential. Failed reads/checks stop;
    observation never starts replacement work or resumes/cancels workflow execution.
  - Timeout and signals retain the last safe observation and reconnect command.
    JSON results include schema version, outcome, exit code, target and safe report.
    Error diagnostics use sanitized RFC 7807 on stderr; raw backend bodies and
    payload fields are excluded. Existing HTTP exit classifications are preserved;
    unsuccessful/pending/stale verification uses 1, wait deadline expiry 124 and
    interruption 130. An individual request timeout is an infrastructure error (3).
  - No API DTO, SDK, storage or executor changes are required. Tests use recorded
    HTTP responses and qualify the CLI contract rather than deployed handlers or
    native guest execution. Multi-case execution and release policy changes remain
    separate milestones.
- **Rejected alternatives:** Automatic version refresh can change an interrupted
  admission's target. Polling workflow success alone does not establish assertion
  success. Replaying execution to repair a failed observation can repeat side effects.
