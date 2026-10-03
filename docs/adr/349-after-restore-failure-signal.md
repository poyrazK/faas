# ADR-349 · Distinguish application restore-hook failures

- **Status:** accepted
- **Date:** 2026-09-28
- **Decision:** Guest resume ACK 13 from ADR-348 maps to a typed
  `ErrAfterRestoreHook` on the host. The wake-failure classifier emits the
  closed reason `after_restore_failed` for that error, including when the
  restore falls back to a successful cold boot. The vmmd counter
  `vmmd_wake_failure_total{box,app,reason}` pre-instantiates the new reason.
- **Why:** A configured callback may reject a restore while the snapshot and
  platform resume protocol are healthy. Counting that as
  `snapshot_restore_err` sends operators toward snapshot regeneration instead
  of the application's restore handler. The existing fallback counter and
  `RestoreError` retain the total and the diagnostic message.
- **Bounds:** Only ACK 13 gets this reason. Other nonzero resume ACKs and
  transport failures keep their existing classification. The metric stores a
  fixed reason string; it never uses the app's path, response body, or error
  text as a label. Cold-boot behavior and the customer wire contract do not
  change.

This extends ADR-127's closed wake-failure vocabulary by one vmmd reason. The
runbook and Grafana legend describe the distinct remediation path.
