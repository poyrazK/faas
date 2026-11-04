# ADR-20261103121407: Final egress at teardown, plan changes dated when they happen, held usage and ledger drift alerted

- **Status:** accepted
- **Date:** 2026-11-03
- **Owner:** billing maintainers
- **Amends:** ADR-046 (when host-interface egress is read), ADR-20261101113742 (what makes egress complete), ADR-566 (when a plan's terms take effect), ADR-1162 (held usage), ADR-20261019193600 (dual-run alerting)
- **Related:** ADR-1748, ADR-20261029180000, ADR-20261030120000

## Context

A third review of metering, on main 8f8fc20d64 (2026-11-03), found four gaps.

1. **Egress in an instance's last moments was never metered.** meterd
   checkpoints vmmd's cumulative host-interface byte counters once a minute,
   from schedd's stats snapshot (refreshed at most every 30 s). The snapshot
   lists live VMs only, and vmmd forgets an instance's counters at teardown,
   with no final reading sent anywhere. Bytes sent after meterd's last read
   were lost when the instance parked. A function parks after 30 s idle, so a
   short wake's whole response traffic was often lost. ADR-20261101113742
   treats every fully rolled minute as egress-complete, so strict egress
   budgets and egress billing would rest on a systematic undercount.
2. **The ledger dual run was not alerted.** `meterd_financial_ledger_reconcile_accounts`
   and `meterd_financial_ledger_drift_mb_seconds` were exported, but no rule
   read them; `BillingDrift` compares the provider with the delivery ledger,
   not the financial ledger with the receipts.
3. **A plan change was dated when meterd noticed it.** meterd records a
   month's plan history by observing each subject's plan every minute and
   stamping a change with that minute. A change made while meterd was down or
   catching up was dated hours late, and the hours between got the old plan's
   allowance, Free status and cap.
4. **Held usage was dropped silently.** The pusher holds a pending hour back
   while its subject is suspended, being deleted, has no complete billing
   identity or is no longer listed, and retries it, but only within the
   30-day billing lookback. Past it the hour is never billed or settled, and
   nothing reported it.

## Decision

1. **schedd keeps a departed instance's final counters.** When an instance
   leaves schedd's stats snapshot, `instancestats.Reader` keeps its last row
   (only if a network counter is valid) for
   `api.MeterFinalNetworkReadingRetention` (5 minutes: meterd's refresh TTL,
   one roll and the settle, with margin). A row that reappears is live again.
   `ListInstanceStats` serves departed rows after the live ones with CPU and
   disk marked unknown, so only their network counters are read. meterd's
   sampler already drains an instance that ended within
   `MeterTelemetryDrainWindow`: its next roll checkpoints the final counters,
   into a telemetry row if the instance no longer holds residency. At most
   one schedd frame (about a second) before teardown remains unmetered. A
   meterd outage longer than the retention still loses the final reading.
2. **Two alerts.** `FaasMeterdLedgerDrift` fires when any month reconciles
   with `drift` or `failed` accounts for two hours.
   `FaasMeterdHeldUsageExpiring` fires when held hours leave the lookback
   within 72 hours, for an hour.
3. **Plans are dated from their change time.** `accounts.plan_changed_at` and
   `orgs.plan_changed_at` are stamped by a trigger whenever `plan` changes, on
   every write path. When meterd records terms for a plan different from the
   month's latest snapshot, it dates them from the subject's change time at
   minute precision, clamped to no earlier than the previous terms (snapshots
   stay ordered) and no later than the observation. An unknown change time
   (rows from before the column, stores without it) keeps the observed minute.
   An hour the pusher already delivered before the change was recorded is not
   re-billed; the window where that can happen is a meterd price-capture
   failure that outlasts delivery, which `FaasMeterdUsageDeliveryStalled` and
   `FaasMeterdSampleFailing` already page on.
4. **Held usage is reported.** Each push pass sets
   `meterd_billing_usage_held_windows{meter,reason}` and
   `meterd_billing_usage_held_expiring_windows{meter,reason}` (held hours
   that leave the lookback within 72 hours), with reasons `suspended`,
   `deleting`, `identity_incomplete` and `unknown_subject`, and logs the
   expiring counts. Whether a suspended subject's usage should be billed
   stays an operator decision; the lookback is unchanged.

## Consequences

- Egress totals rise by the traffic of instances' final seconds, which was
  previously unmetered. Strict egress budgets and the egress meter see it.
- `ListInstanceStats` may list instances that are no longer running for up to
  five minutes; meterd is its only consumer and reads them by instance ID.
- The migration is expand-only: two nullable columns, NOT VALID CHECKs
  validated separately, and a trigger. The previous release neither reads nor
  writes the columns; its plan writes are stamped by the trigger.
- Operators get paged before held usage is lost and when the ledger and the
  receipts disagree.
