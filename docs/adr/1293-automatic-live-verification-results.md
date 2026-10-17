# ADR-1293 · Automatic live verification results

- **Status:** accepted
- **Date:** 2026-10-15
- **Decision:** Observe the selected live verification while its dashboard page is
  visible, then explicitly check its frozen assertions when execution terminates.
  Replace only its safe result fragment and preserve the editor's unsaved fields.
- **Why:** Single-case live verification required manual result checks and full
  form submissions, making progress difficult to follow while editing test data.
- **Consequences:**
  - Separate session-authenticated dashboard GET and POST routes delegate to the
    existing scoped verification APIs. GET only reads. POST checks results and
    retains the same app-bound CSRF, verified-email and deploy-write controls.
    It accepts only the CSRF token; no editor payload, input or execution intent
    enters this path. No progress response rotates the main form's CSRF cookie.
  - Requests bind app, automation name and run ID. Responses render only the safe
    selected-result template: identities, state, freshness and payload-free report.
    Backend errors are replaced by generic diagnostics. Input, output bodies,
    expected values, raw errors and other editor content are excluded.
  - The browser reads queued/running/waiting state and checks terminal outcomes
    only if their report is absent or no longer current. Refresh never starts,
    resumes or retries workflow execution. A current terminal report stops updates.
    Later changes require reopening or explicitly resuming observation.
  - Updates replace only the selected result article. They never serialize or
    replace editable fields. A local dirty flag preserves the displayed-case
    warning across fragment replacement, independently of saved-target freshness.
  - A single in-flight request and generation fence prevent paused or superseded
    responses from updating the page. Hidden pages abort requests and pause timers;
    visibility restores observation with its remaining budget. Form navigation
    and pagehide stop observation. Manual checks remain available without JavaScript.
  - Central limits provide a 3-second interval, 10-second request timeout,
    100 requests per observation session and a 4 KiB result-check form. Reads and
    checks both consume the budget. Failure, timeout, invalid response, permission
    loss or budget exhaustion stops updates and offers an explicit restart.
  - No migration, public API DTO, SDK or executor changes are needed. Browser
    qualification advances fixture state; it does not qualify deployed providers
    or native guest execution.
- **Rejected alternatives:** Polling a full page can erase unsaved edits and rotate
  CSRF envelopes. Evaluating assertions on GET mutates state through a read route.
  Retrying execution while refreshing results can repeat real side effects.
