# Pipeline operability — 2026-11-13 UTC

The approved batch implements [ADR-20261113181050](../../../adr/20261113181050-pipeline-freshness-durable-cli-and-index-advice.md): freshness-confirmation targets, durable CLI setup and mirror index advice. Customer commands and limits are in [data pipelines](../../../data-pipelines.md); CUST-004–006 remain recorded in [the findings ledger](../../../pipeline-findings.md).

Checks use Go 1.26.11 with the current Makefile development flags, disposable PostgreSQL 16 with TLS/native logical replication, and the repository's digest-pinned PostgREST and Node runtime. PostgreSQL data uses a disposable RAM-backed volume. No customer database, provider account, production deployment or feature gate was changed.

## Results

- Complete native pipeline/catalog, HTTP domain, source gateway and dashboard suites passed. Managed database capacity/setup and bounded Neon credential tests passed. The CLI pipeline/check suite also passed; a final CLI run includes manifest parity and single-receipt output while waiting.
- Focused native index tests passed: exact tenant policy expression and query-plan use, signed HTTP tenant isolation, source-independent inspection/build, N-1 approved-DDL claim fencing, lost acknowledgement replay, lock timeout rollback, provider-role temporary-file guard, UUID tenant expression, resync cleanup and invalidation of old applied advice.
- Focused operability domain tests passed: confirmation boundaries, idle/unknown status, target validation/revision fencing, persisted debounced incident/recovery, old-monitor exclusion, immutable setup recovery, read-only planning and scoped-replacement advice invalidation.
- Server pipeline/dashboard wiring and temporary discovery idempotency refusal tests passed.
- The browser setup preview passed its CSRF regression: it creates no job and preserves the original tab’s form token for the subsequent setup submission.
- Public durable setup HTTP plan/recovery/cancellation and foreign-account isolation passed; private provider plans and credentials remain absent from responses.
- Broad incremental `golangci-lint` passed with zero issues; store, seal-domain, problem-detail, trimpath, runbook SQL, text-encoding and ADR-uniqueness policy checks passed.
- Schema regeneration, SQLC generation, canonical clone registry, migration expand/contract lint, OpenAPI lint/parity, SDK coverage and docs links passed. OpenAPI lint passed with non-fatal warnings and informs.
- Capability inventory coverage includes all setup routes and maps planning to data pipelines; capability-matrix and stable SDK-spec comparisons passed.
- Node preview SDK generation/build and Python preview SDK generation/model round trips passed. The public SDK surface is unchanged.
- All 35 provider-runner, staging-canary and dashboard refresh Python tests passed.

The local native run omits the race detector to fit the workspace's build-cache budget. CI's unchanged acceptance command runs with `-race`; its result is required before review readiness. The shared build cache was preserved.

## Rollout limits

This remains an approved-account staging pilot. Live Neon-source qualification is pending real staging credentials, qualified allocations and reviewed provider fault controls. A provider role that cannot enforce the index temporary-file budget receives advice-only results. No live-provider pass, production readiness, deployment or merge is claimed.

Deploy the expanded catalog, capable pipeline/setup and alert workers, then the new controls. N-1 owners are fenced from approved index DDL, saved freshness-aware setups and freshness monitoring. Larger concurrent index builds, projection evolution, Auth organization synchronization and related-table read models remain future work.
