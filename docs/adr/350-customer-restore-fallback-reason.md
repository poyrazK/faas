# ADR-350 · Show application restore-hook fallback in wake timelines

- **Status:** accepted
- **Date:** 2026-09-28
- **Decision:** When a configured application `after_restore` callback fails
  and the requested restore succeeds by cold-boot fallback, vmmd returns the
  closed `after_restore_failed` reason to schedd. Schedd includes it as
  `restore_fallback_reason` on the customer-visible `wake.boot_completed`
  event. The CLI wake timeline explains the code in plain language.
- **Why:** ADR-349 made the failure distinguishable in operator metrics, but
  customers still saw only a cold boot. The callback owner needs a per-wake
  reason to diagnose repeated slow wakes without operator assistance.
- **Bounds:** The field is absent on successful restores, planned cold boots,
  and other restore failures. vmmd and schedd both check the closed code and
  the requested/actual wake methods before exposing it. The new reason
  contains no callback URL, response body, or raw error text. Older
  vmmd versions omit the optional proto field and keep the old event shape.

The wake-timeline API retains its existing per-app authorization and plan gate.
The change adds one optional event payload field; no storage migration is needed.
