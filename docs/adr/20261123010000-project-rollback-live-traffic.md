# Gate project rollback cutovers on production error budgets

`gregale project rollback <slug> --yes --verify --verify-live-traffic` adds a post-cutover gate for each targeted HTTP service with an enabled production route monitor and a 5xx budget. The operation requires at least one such service and request telemetry on the project account. The reviewed plan and saved operation pin the monitor revisions and 5xx budgets.

The worker reads the existing production route telemetry for the exact restored deployment. Both closed one-minute windows must start after all gateways acknowledge the pinned route generation. The judgments use the production monitor's sample minimums, budget thresholds, ingestion allowance, and telemetry loss checks. Each service has up to five minutes to get a result, and the project budget adds this allowance for each gate. The last eligible service must also pass before completion.

A sustained breach blocks the operation and releases unused preparations. Sparse traffic stays visible in the receipt and blocks on timeout. A resume starts a fresh observation window for a restored service whose gate has yet to pass. Confirmed probe failures still follow the existing automatic undo policy.

The checked rollback transaction requires persisted evidence for the exact route. The database also protects the saved policy and fences older checked workers during rolling upgrades.
