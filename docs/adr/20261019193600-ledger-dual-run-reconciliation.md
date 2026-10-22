# ADR-20261019193600: Ledger dual-run reconciliation for compute delivery

Status: accepted.

Implements the dual-run requirement of ADR-566 ("Switching provider delivery
to ledger results requires exact dual-run reconciliation") for the compute
meter.

## Context

Two paths compute what a customer owes for compute. The meterd pusher reads
`usage_minutes`, removes the month's allowance and caps, sends each final UTC
hour to the billing provider and records a receipt in
`billing_usage_deliveries`. The financial ledger (ADR-566) retains per-minute
evidence with price contracts and computes the same month's net quantity
independently. Billing still comes from the pusher. Making the ledger the
single source first needs evidence that the two agree.

The existing drift detector (ADR-049) compares the pusher with the provider's
own totals. Nothing compares the pusher with the ledger.

## Decision

1. **What is compared.** Every `FAAS_RECONCILE_INTERVAL` (default 6 hours)
   meterd checks each paid account with a billing identity for the active
   provider, for the current and the previous UTC month. The window starts at
   the later of the month start and the identity's `billing_from`, and ends at
   the pusher's cutoff: the last hour whose minutes are all final, never past
   the month end. The ledger side is its compute quantity at the cutoff
   minus its quantity at the window start. For overage-mode providers (Polar,
   Paddle) that is the net quantity above the month's allowance; for the
   raw-mode legacy Stripe path, which applies the allowance in the provider,
   it is the gross quantity. The pusher side is the sum of compute receipts
   for the window's hours.
2. **Results.** Each account-month gets one result:
   - `match`: the two quantities are equal.
   - `pending`: usage in the window has no receipt yet; delivery is behind.
   - `capped`: the pusher delivered less and the account has an overage cap,
     which the ledger does not apply.
   - `incomplete`: the ledger's evidence for the month is incomplete.
   - `drift`: any other difference. It is logged with both quantities.
   - `failed`: a read failed.
3. **Signals.** `meterd_financial_ledger_reconcile_accounts{month,result}`
   counts the last pass's results. `meterd_financial_ledger_drift_mb_seconds{month}`
   sums the absolute differences of `drift` results.
4. **The switch is a later decision.** Provider delivery stays on the pusher.
   Moving it to ledger results needs its own ADR with evidence: at least one
   closed UTC month with no `drift` across paid accounts, covering a mid-month
   plan change, a credit and a refund (ADR-566).

## Consequences

- Read-only: the reconciliation never changes usage, receipts or the ledger.
- Compute only. Egress and object storage have their own delivery paths and
  need their own comparison before they can move.
- Cost is two ledger reads per account-month per pass; at a 6-hour interval
  that is small next to the pusher's hourly work.
- Rejected: comparing money instead of quantity (the pusher sends MB-seconds
  and the provider prices them, so quantities are the exact common unit);
  comparing only the current month (the previous month is the only one that
  is final, and it is the evidence the switch needs).
