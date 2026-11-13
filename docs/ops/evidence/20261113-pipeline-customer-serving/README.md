# Pipeline customer serving — 2026-11-13 UTC

Implementation of the agreed Neon onboarding, tenant access and column selection
batch follows [ADR-20261113160716](../../../adr/20261113160716-pipeline-tenant-serving-and-projections.md).
Customer limitations and commands are in [data pipelines](../../../data-pipelines.md).

Local checks used Go 1.26.11, disposable PostgreSQL 16 with TLS/native logical
replication, and the repository's pinned PostgREST/Node runtime. No real customer
database, provider account, deployment, webhook or production flag was changed.

## Local results

- Complete pipeline unit/catalog suite passed (125.881 s), including actual N-1
  claim queries against both worker fences and frozen membership approvals.
- API domain tests passed (43.594 s), including foreign-account rejection and
  membership preview/approval through the typed Go client.
- OpenAPI route/schema parity passed. Schema dump, SQLC generation and clone
  registry were updated; migration expand/contract lint passed.
- Dashboard pipeline regressions passed after correcting the shared form partial.
- CLI pipeline/check/manifest tests passed (4.004 s), including local contract
  validation before allocation, offline Neon guidance and frozen approval IDs.
- The native customer-serving scenario passed (6.451 s): real signed HTTP tenant
  isolation, excluded-column rejection, projected initial copy and insert/update/
  delete CDC, tenant-key moves, ignored tenant claims, verification/quarantine,
  HTTP 503, scoped repair with API refresh, resync, lost policy acknowledgement,
  stale gate reopening, source-independent acknowledgement, grace and full
  revocation, and publication-widening detection.
- Node SDK generation/build passed. Python SDK generation and serving/empty-array
  revocation round trips passed. The public SDK surface remained unchanged;
  the additions are in preview.
- All 35 provider-runner, staging-canary and dashboard refresh tests passed.

The complete race-enabled native script could not finish locally within the
workspace's disk budget. Initial attempts exhausted disposable database storage;
the later RAM-backed fixture started, but the additional build-cache footprint
required deferring that full run to CI. The shared cache was preserved. The focused
native scenario above and ordinary unit/API suites did complete locally.

## Provider qualification

The Neon-source offline check correctly reports `state: blocked`,
`production_ready: false` and `pilot_ready: false`. This environment has no Neon
source credentials, signed staging identities, reviewed fault adapter, qualified
allocations or staging deployment URL. A real provider pass cannot be claimed.
The source-aware runner requires direct Neon URLs and reviewed source-provider
evidence separately from destination evidence. The existing positive native
WAL cap of at most 1 GiB remains mandatory.

CI results and the review URL will be recorded here after the branch is checked.
