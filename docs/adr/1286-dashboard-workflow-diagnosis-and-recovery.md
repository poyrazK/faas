# ADR-1286 · Dashboard workflow diagnosis and recovery

- **Status:** accepted
- **Date:** 2026-10-14
- **Decision:** Extend the guided automation dashboard with read-only run
  diagnostics, continuation previews, explicit step-attempt inspection,
  continuation history, and separately confirmed resume/cancel forms. Reuse
  existing API handlers and their idempotency, admission and generation checks.
- **Why:** App owners can author and start automations in the browser, but still
  need the CLI to explain waits and safely recover a failed run.
- **Consequences:**
  - App and automation identity are checked against the actual run before any
    diagnostics, history, attempts or action request. A selected step must appear
    in the run's observed steps before its attempts are requested.
  - GET performs no mutation. Missing diagnostics fail closed for resume; a
    failed history read is shown as unavailable. A preview reserves no capacity.
  - Resume submits the displayed continuation count. The existing API validates
    the captured graph, safe effects, deployment/integration eligibility,
    runtime availability and current generation. An already admitted request
    must reach the API's idempotency wrapper even if a new preview is ineligible.
  - Resume/cancel each use a distinct per-form UUID key and explicit confirmation.
    Both preserve the browser session/MFA, verified-email, write-scope and named
    CSRF boundaries. An unsuccessful action requires a fresh GET and confirmation.
  - Cancellation targets the selected run's current state; it has no new
    generation fence and does not undo completed effects. The existing API
    leaves terminal runs unchanged and allows cancellation while runtime is off.
  - Browser views omit real inputs, outputs, raw errors, historical errors,
    actor identifiers and managed-operation effect records. Attempt inspection
    is an explicit read for one step, rather than automatic per-step fanout.
  - No schema, public API or native admission changes are introduced. Native
    execution and leak qualification remain separate requirements.
- **Rejected alternatives:** Mutating state from the browser adapter would
  duplicate runtime policy. Rechecking preview eligibility before the API's
  idempotency wrapper would reject a valid repeat of an admitted continuation.
  Inferring a safe continuation from a failed status would bypass the existing
  planner's effect and control-step restrictions.
