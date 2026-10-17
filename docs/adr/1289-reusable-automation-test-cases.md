# ADR-1289 · Reusable automation test cases

- **Status:** accepted
- **Date:** 2026-10-15
- **Decision:** Explicitly save a definition-free sample and named mock test cases
  with an automation draft. Run the complete saved set against the authoritative
  draft and retain safe case/step summaries through the API and dashboard.
- **Why:** Custom automation samples were transient. Owners had to reconstruct
  them on reopening and could not retain a reusable regression set or its results.
- **Consequences:**
  - Nullable JSONB suite/report columns extend the existing automation record.
    Migration replay preserves existing definitions. Both stores validate bounded
    suite shapes and use the existing automation version for compare-and-swap.
  - Suite edits advance intent and invalidate older publishing receipts. Draft
    and suite saves preserve each other and do not reset schedule cursors.
    Clear removes both suite and report. Deletion removes the record normally.
  - Simulation runs outside write locks; report persistence fences the unchanged
    draft revision under the same app admission lock as other automation writes.
    Cancellation or stale revisions cannot persist a partial or mismatched report.
    Observations change neither revision nor updated time or runtime admission.
  - Results retain only case names, issue codes, assertion pass/fail, step names
    and observed states. Inputs, outputs, raw errors and publication receipts are
    excluded. All cases run even after assertion or simulation failure.
  - Content hashes cover the definition and full saved suite. They tolerate JSONB
    object ordering without float64 precision loss. Editor comparisons also accept
    equivalent numeric spellings normalized by JSONB (1e2 and 100), while retaining
    exact large-integer comparisons. Reopened report forms preserve omitted default
    fields, so running saved cases does not create an unsaved draft edit.
    Unsaved edits mark displayed
    results outdated; publication or enabled intent alone leaves them current.
  - Dashboard run/check/publication reject unsaved suite edits. Publication still
    obtains fresh server-issued, actor/version/policy-bound receipts. Existing API
    policy modes remain authoritative; reports grant no publishing authority.
  - Samples are saved only by explicit user action, with storage guidance and
    confirmed removal. No real run/event data is captured. Session/MFA, app-bound
    CSRF, verified-email and deploy-write authorization remain enforced.
  - Go, Node and Python API contracts expose saved suites and safe reports. Mock
    acceptance does not qualify handlers/providers or open native KVM admission.
- **Rejected alternatives:** Keeping fixtures only in browser storage loses them
  across devices. Treating a retained report as a receipt bypasses policy/actor
  freshness. Advancing the draft version on every run creates editing conflicts
  for observations. Resetting schedules on test edits changes live execution.
