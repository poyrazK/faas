# ADR-1299 · Read-only CI gates for live automation suites

- **Status:** accepted
- **Date:** 2026-10-15
- **Decision:** Add `gregale automations live gate` as an opt-in, read-only
  evaluation of the coherent comparison introduced by ADR-1298. Version the
  initial policy as `strict_v1` and publish a manually dispatched CI example.
- **Why:** Comparison counts alone cannot gate releases: zero regressions can
  coexist with unchanged failures, pending added cases or removed coverage.
- **Consequences:**
  - Callers explicitly select an app/name, distinct baseline/candidate suite IDs
    and the expected candidate saved version, published revision and deployment.
    One authenticated comparison GET supplies the entire observation. No target
    discovery, assertion checking, admission, resume, cancellation, journal write
    or deployment action is performed. There is no automatic waiting or retry.
  - `pkg/automationgate` validates the response identities, bounded distinct case
    union, selected/linked counts, result/change categories and summary coherence
    before trusting evidence. The Go comparison client also requires explicit,
    non-null freshness flags; missing booleans cannot silently become false.
    Missing fields, inconsistent direction or unknown future categories are
    protocol errors (3), not evidence of success.
  - A pass (0) requires the expected current target, every candidate selected case
    passing, and no regressions, unknown/incompatible matches or removed baseline
    cases. Passed added cases and recoveries are allowed. Any known failed case
    in the intended current candidate fails (1), including unchanged failures or
    failures accompanying other evidence gaps.
  - Wrong or outdated candidate targets are inconclusive (7), regardless of old
    assertion verdicts. Other incomplete evidence and removed coverage also use 7
    unless the intended current candidate has a known failed case (1). Reasons
    remain visible together. The baseline may be historical: its original run's
    still-current report is distinct from current-target freshness.
  - Valid invocations emit a schema-versioned JSON report with the policy, exact
    selection, expected target, outcome, exit code, stable reason codes, candidate
    pass/fail counts and optional safe comparison. Request/protocol failures omit
    untrusted evidence and also emit sanitized RFC 7807 diagnostics. Raw backend
    details and private execution data are never included. Human mode shows the
    decision, target, reasons and each case's original result categories.
  - CLI error conventions remain 1 invalid usage, 2 authentication, 3 platform or
    protocol failure, 4 unavailable records, 5 conflict, 6 forbidden and 130
    interruption. Comparison reads use the existing ten-second request deadline;
    timeout is an infrastructure error (3). Help and invalid flags stay local.
  - The workflow example lives outside `.github/workflows`, so it is not enabled
    for this repository. After copying, users explicitly dispatch it with fixed
    identities and a pinned CLI release. The read token is scoped to the gate
    step. Summary/artifact steps always run; gate failures are never suppressed.
  - This is not a persisted release receipt, a maximum-age policy, proof of all
    possible case coverage, or a deployment/publishing gate enforced by the API.
    Retention and external workflow changes can invalidate later observations.
- **Rejected alternatives:** Treating zero regressions as success hides unchanged
  failures. Gating only on comparison categories hides pending added cases.
  Refreshing targets, baselines or reports silently changes the evidence being
  approved. Workflow-installed real-action execution weakens explicit confirmation.
