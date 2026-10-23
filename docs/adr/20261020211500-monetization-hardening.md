# ADR-20261020211500: Handoff reservations, usage-only credit in spend caps, reprice on change, credit on self-service statements

- **Status:** accepted
- **Date:** 2026-10-20
- **Decision:** The end-to-end review of API monetization (2026-10-19) left four smaller findings after ADR-20261019180000. Each gets a fix.
  - **Exports reserve the handoff before charging.** An export checked ADR-238's overlap rules before it called the provider, but recorded the handoff only afterwards. Two exports of overlapping statements could both pass the check and both create invoices. One could be a consumer statement and the other a customer statement that includes that consumer. Now, once an export holds its claim, it reserves its statement's handoff before the provider sees anything. The reservation is `usage_statement_invoice_exports.handoff_reserved`. Creating a reservation takes the account lock that handoff creation takes, and the overlap rules count reserved exports as handoffs. An overlapping export therefore gets `409` with the overlap detail, and so does a manual handoff. A refused attempt releases the reservation, so the statement can be retargeted or another statement handed off. A completed export keeps it; its handoff then exists too.
  - **Spend caps count only credit that paid usage.** Spend caps compare usage, not fees, with the cap (ADR-20261019100331). They subtracted all credit applied to the month's statements, including credit that paid base fees and minimums. That credit lifted the cap a second time. Credit now pays a statement's fees first. A credit position counts only `greatest(credit_applied − fees, 0)` per statement, where negative fees count as zero, for consumers and tenants alike.
  - **Spend checks reprice only on change.** The worker repriced every capped consumer's and tenant's whole month every minute, reading every usage minute of the month each time. Now an unforced check reuses the last price when all of these hold:
    - the price is for this month;
    - it is younger than `ConsumerSpendCapRepriceInterval` (15 minutes);
    - the cap has not changed since;
    - no usage minute of the consumer or tenant was written since the check, less 30 s for in-flight writes and clock skew.
    The usage-minute lookup covers minutes from `ConsumerSpendCapUsageLookback` (1 hour) before the check. Request, route and meter units are recorded in one transaction, so the usage minutes show any change. A reused check still compares the stored amount with the cap as it is now. Changing a cap or credit forces a reprice, as before.
  - **Self-service statements show credit.** `GET /v1/consumer-billing/{app}/statements` adds `credit_applied_millicents` and `amount_due_millicents`. A customer can then see why an invoice is smaller than the statement.
- **Why:** The first finding could invoice the same usage twice. The second let a cap be overshot by the fees that credit paid. The third made spend checks cost about one full month's usage read per capped customer per minute. The fourth left customers unable to reconcile statements with invoices.
- **Consequences:**
  - **New 409s.** An export of a statement that overlaps one being exported now gets `409` until the first finishes or is refused.
  - **Delayed non-usage changes.** Rate card or trial changes, credit expiring, and credit applied at finalize reach a cap within 15 minutes instead of 1. Usage still reaches it within a minute. A write to a usage minute older than the lookback waits for the 15-minute reprice.
  - **Caps can trip sooner.** A consumer whose credit paid fees reaches the cap by up to those fees sooner.
  - **Migration.** `usage_statement_invoice_exports` gains `handoff_reserved boolean NOT NULL DEFAULT false` (expand). Existing exports have no reservation. Completed ones have their handoff, and those still running reserve on their next attempt.
- **Rejected alternatives:**
  - Recording the handoff before calling the provider would need the provider's invoice ID, which does not exist yet.
  - An advisory lock held across the provider call would pin a connection for minutes, as ADR-20261019180000 already rejected.
  - Pricing only the usage since the last check and adding it to a stored total is wrong for tiers, allowances and monthly unit streams, which price the whole month.
  - Storing the usage amount on the cap row, to recompute credit every minute, would need columns on both cap tables for a 15-minute gain.
