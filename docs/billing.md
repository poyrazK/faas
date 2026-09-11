# Billing

Gregale bills the account that owns an app. Plan prices and included capacity are generated from the platform limits table in [Plans and pricing](plans.md).

```bash
gregale usage --month 2026-09
gregale billing status
gregale billing portal
```

Usage is measured in GB-RAM-hours for running app instances. The plan also sets deployed-app, developer-app, concurrency, storage-layer, and idle-timeout limits. Apps parked by scale-to-zero do not accrue running-instance usage; storage and other explicitly metered services remain separate line items.

Free accounts have a zero monthly charge and are subject to the published limits. Paid plans are billed monthly through the account billing portal. Treat the portal as the source of truth for invoices, tax, payment methods, credits, and failed-payment recovery.
