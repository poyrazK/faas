# ADR: Gateway-metered object storage billing

Date: 2026-10-24
Status: Accepted
Related: [ADR-1430](1430-plan-entitlements-from-october-2026-cogs-report.md), [ADR-1320](1320-object-storage-plan-allowances-ceilings-and-overage-cap.md), [ADR-627](627-prospective-gateway-storage-safety-accounting.md), [ADR-156](156-object-storage-accounting.md), [ADR 20261023223000](20261023223000-object-storage-bucket-scoped-availability.md)

## Context

ADR-1430 prices object storage. Each plan includes average stored bytes per
month (1 / 10 / 50 / 200 GiB), and storage beyond that costs $0.03 per
GiB-month. Egress is free, and request pricing is deferred. Free stops at
1 GiB.

Production could not bill this:

- Production runs `gateway_safety_v1` accounting (ADR-627). That mode had no
  stored-byte-hours meter, and the configuration rejected any rate card in it.
- Billing from provider reports needs a usage exporter for the backend. Only
  the OVH exporter is wired up. The GCS exporter is deliberately not wired:
  it still requires EUR and a qualified Monitoring and Cloud Billing source.
- The generated plans page did not list storage.

Gateway safety already inventories every bucket at least every 15 minutes
(ADR 20261023223000). Those observations measure stored bytes directly.

## Decision

1. **Gregale meters stored bytes in gateway-safety mode.** A new table,
   `object_storage_gateway_storage`, holds one row per bucket per UTC month:
   `accrued_from`, `accrued_through`, and `stored_byte_hours` as an exact
   numeric. Every minute, apid's accounting loop does two things in one
   transaction, after the inventory sweep:
   - opens this month's row for each live bucket;
   - accrues every open row of this or last month at the bucket's last
     inventoried bytes, up to now, the end of the month or the bucket's
     deletion, whichever comes first.

   Each update starts from the committed `accrued_through`, so concurrent
   apid processes never count the same time twice.
2. **Billing never covers time Gregale did not meter.** A row opens at its
   first accrual. A row continues from the month start only when the same
   bucket was metered the month before. A bucket that was never metered while
   it lived bills nothing.
3. **Month close bills a metered month from these rows.** It waits until every
   row is accrued to the end of the month, or to its bucket's deletion
   (`ErrObjectBillingIncomplete`, retried each minute). A month that has both
   metered rows and provider reports is refused (`ErrObjectBillingConflict`).
   Requests and egress come from the gateway counters, at zero price.
4. **Gateway safety accepts a storage-only rate card.** Request and egress
   prices must be zero. `ValidateObjectStorageBillingSetup` allows live
   billing in this mode on the same terms.
5. **Provider cost is not a priced meter.** A charge or estimate can be
   computed when only `cost_millicents` is unavailable
   (`ObjectStorageUsage.Chargeable`). The live usage report marks stored
   byte-hours available once every live bucket is metered.
6. **The rate is published in `pkg/api/limits.go`.**
   `ObjectStorageMillicentsPerGiBMonth = 3000` is the published rate.
   `docs/plans.md` renders each plan's included storage and this rate. The
   operator's rate card applies it.

Billing stays off until the operator turns on object-storage billing (Polar
`shadow` or `live`, ADR-156). This change only makes ADR-1430 billable.

## Consequences

- **Accuracy:** billed bytes are sampled at inventory cadence, at most
  15 minutes apart, then integrated each minute. Growth between scans bills
  from the next scan.
- **Outages:** if apid is down across a month boundary, last month's rows
  close on the next accrual, at the bucket's then-current bytes. A row older
  than last month never closes, and that month cannot bill. That fails closed.
- **Compatibility:** expand-only migration. An N-1 apid neither accrues nor
  reads the rows, and N-1 month close still sees no reports in gateway mode,
  so it fails closed as before.

## Validation

- `TestGatewayStorageAccruesAcrossTheMonthMem` covers accrual, the month-end
  close, continuation into the next month, and month-close usage.
- `TestGatewayStorageAccrualCountsTimeOncePG` has two concurrent accruals of
  one hour count it once.
- `TestGatewaySafetyReportsMeteredStorage` covers the live usage report.
- `TestGatewayMeteredMonthBillsADR1430Storage` covers the charge beyond
  Hobby's allowance, within it, a never-metered bucket, an open row and mixed
  sources.
- `TestGatewaySafetyAccountingBillsStorageOnly` covers billing setup
  validation.
