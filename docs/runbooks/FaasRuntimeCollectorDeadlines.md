# FaasRuntimeCollectorDeadlines

## Symptom

One apid has recorded an estimated three or more reports with
`deadline_exceeded` per five-minute window for five consecutive minutes.
The collection uses a shared three-second source deadline and can inherit an
earlier deadline from its caller.

## Check

1. Open **Gregale Runtime Diagnostics** for the alert's **apid instance**.
   The warning's **Runtime Diagnostics** link opens that view. Review
   **Collection deadlines by phase and origin (5m)**. For `caller_deadline`,
   check the verifier's timeout and any inherited time budget. Compare
   **Collection phase duration p95 by apid (5m)** to see how earlier phases
   contributed to `collection_budget` expiry.
2. Check **Compute inventory outcomes by apid (5m)** if the inventory phase is
   slow. An inventory deadline points to a budget issue and a source error
   points to the registry. If report assembly or preview fitting is slow,
   check apid resource pressure. Also check the caller's timeout.
3. For slow core or advisory reads, compare **Non-successful source reads by
   group (5m)** and **Source read duration p95 by group (5m)**. Add an
   `instance` matcher in Grafana Explore to isolate the apid named in the alert.
   For a focused verifier run, use `--runtime-diagnostics none` to keep core
   evidence or select only the advisory groups you need. Compare the next
   report's evidence coverage and timing.

## Verify

Confirm expected polls produce fresh evidence and the five-minute deadline count
drops below the warning threshold. See [runtime collector metrics](../ops/post-deployment-monitoring.md#runtime-collector-metrics).
