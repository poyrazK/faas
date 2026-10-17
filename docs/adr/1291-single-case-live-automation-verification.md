# ADR-1291 · Single-case live automation verification

- **Status:** accepted
- **Date:** 2026-10-15
- **Decision:** Explicitly admit one saved case input as a real published workflow
  run. Freeze separate live expectations, the case hash, published revision and
  deployment identity atomically with the run. Check terminal outcomes on demand
  and retain payload-free summaries.
- **Why:** Mock traces cannot establish that deployed handlers and connected
  services produce the expected real outcomes. Existing manual runs lacked
  durable links to saved cases and their assertions.
- **Consequences:**
  - A new verification table references the existing workflow run and automation.
    Admission uses the same app lock, active-run quota, physical code pin and
    durable executor as ordinary workflows. Runtime, plan, account, maintenance,
    default deployment and outbound-binding checks remain enforced. Apps requiring
    platform-tenant identity are rejected in this first app-scoped milestone.
  - The request must confirm real actions and provide a stable idempotency key,
    current automation revision, published revision, deployment and saved case
    name. Case edits, publication and target selection are fenced under the app
    lock. Deployment identities compare UUID values while preserving the opaque
    string returned by the backend. Retained keys replay before current-target
    and runtime checks; changed requests conflict without creating another run.
  - Only the saved case's input is executed. Mocks and mock assertions are ignored.
    Live expectations use real terminal states, optional exact JSON output, and
    the durable step attempt counter, including resumed attempts. Explicit null
    and count zero remain distinct from omitted assertions. The default expected
    run state is succeeded; failed/dead can be asserted explicitly. Cancellation
    always fails verification.
  - The new optional case `live` object survives guided and advanced JSON edits,
    duplication, saved-suite storage and Go/Node/Python roundtrips. Live expectations
    are authored explicitly; simulated success is never translated into approval.
  - Checking results never starts or retries execution. Running and parked runs
    have no verdict. Report writes serialize with resume under the app/run locks;
    changed run state, update time or resume count invalidates old observations.
    Cancellation of checking persists no partial verdict.
  - Reports expose only case/run/deployment/revision identities, timestamps,
    issue codes, states and mismatch field names. Frozen expected values and
    actual inputs/outputs/errors are excluded from this read surface. They never
    grant publication authority. Case, published revision or default deployment
    changes mark historical verification outdated; unrelated draft/enabled intent
    alone does not. `report_current` concerns the recorded run state, and must be
    combined with `outdated=false` for current-target evidence.
  - Clearing saved test data deletes frozen live assertions and verification
    history atomically; it does not cancel or delete the actual workflow run.
    Automation deletion and ordinary run retention remove verification rows by
    cascade. Idempotency lasts for the retained verification record.
  - Central bounds allow 128 live assertions, 3 MiB per live spec, 1 MiB real run
    input/output assertions, a 4 KiB start request, 128 KiB reports and 20 recent
    history entries. Existing suite/case totals and execution quotas still apply.
  - Portable tests qualify admission, recorded outcomes and browser behavior with
    fixture state; they do not qualify guest execution/providers or open native
    KVM admission. No VM lifecycle implementation changes are introduced.
- **Rejected alternatives:** Starting first and attaching provenance afterward can
  execute an unconfirmed revision during a publish/deploy race. Reusing mock
  expectations confuses mocked and real states. Automatic replay while refreshing
  results can repeat side effects. Retaining actual payload diffs is unnecessary
  to explain which assertion failed.
