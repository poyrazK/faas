// Package meter is the usage-aggregation + quota-enforcement core for meterd
// (spec §4.7, ADR-010). It exposes pure functions the meterd daemon glues
// together with timers:
//
//   - SampleAndRoll writes one minute of usage for every live instance.
//   - MonthlyUsageGB sums the current month's billable GB-RAM-hours.
//   - EnforceQuota marks Free accounts suspended at 100 % and emits a
//     paid-tier quota warning at 100 %.
//   - AccountMonthKey / MinuteKey / GBHours are pure arithmetic helpers
//     that unit tests pin down.
//
// Billing rule (spec §4.7): the billable unit is plan RAM + 8 MB per running
// second — NOT the sampled cgroup RSS. Customers get predictable bills and
// the financial model's math (ex44_faas_financial_model.xlsx) depends on it.
// Cgroup readings stay in vmmd telemetry; meterd accumulates the admission-
// time RAM so the ledger line and the bill line stay identical.
package meter
