# Customer reference platform qualification

This milestone qualifies one deployable customer platform before making a
general platform-for-platforms promise. The baseline is the
[`customer-platform` starter](../cmd/gregale/templates/customer-platform/README.md),
two independent customers, tenant-required ingress, and shared PostgreSQL with
forced row security. The initial contract covers trusted application code and
pooled customer data. Per-customer arbitrary code and dedicated environments
require separate isolation contracts.

## Acceptance criteria

| Journey | Required evidence | Current gate / remaining work |
|---|---|---|
| Deploy | Tenant ingress enabled before public activation; failed migration cannot activate | Starter deployment contract; qualify on native x86_64 KVM |
| Onboard | Distinct, retry-safe customer identities and hash-only credentials; owner token stays outside guest | `make test-customer-platform` |
| Isolate data | Forged headers and cross-customer CRUD denied; forced RLS survives pooled connection commit/rollback | Starter Node/PostgreSQL tests and owner API/gateway acceptance |
| Rotate and suspend | Old key denied after rotation; suspension blocks only the selected customer; resumption preserves revocation | Two-customer acceptance; qualify during real snapshot restore |
| Run background work | Tenant-bound status/results/replay/cancellation; suspension holds unclaimed requests | Existing deferred-HTTP acceptance; native VM delivery still outstanding; named queues/OCI Jobs/AppTasks outside this contract |
| Review monthly usage | Full 31-day month across two continuously active apps; deterministic daily drafts; compact price-source review; retry after lost response | Starter billing tests simulate 89,280 minute records; real owner API gate verifies daily periods and tenant boundaries |
| Invoice | Explicit finalization; stable external handoff references; additive late adjustments; no duplicate coverage | Extended owner API/PostgreSQL starter gate; external invoice provider integration remains open |
| Activate domains | Verified DNS, certificate readiness, customer-visible status; one tenant cannot claim another tenant's domain | Existing platform-tenant APIs; integrated reference onboarding UI and native deployment scenario remain open |
| Offboard | Revoke access and safely detach managed resources; preserve accounting receipts; define app-data export/deletion | Existing offboarding plan/apply; reference workflow and customer data lifecycle remain open |
| Recover | Gateway/API outage and restart preserve acknowledged work and accounting; one failing customer does not degrade another | Native failure drills and load qualification remain open |

## First implementation slice: monthly billing

The local owner tool now reviews a completed calendar month using stable UTC-day
statements and compact invoice lines grouped by source and effective price.
Exact minute-level billable-unit coverage remains private in each immutable
revision and drives additive late-usage adjustments. Draft and finalized totals
stay separate; finalization and invoice handoff remain explicit owner actions.

## Coverage volume check

Run `DATABASE_URL=<disposable-postgres-dsn> make bench-platform-tenant-coverage`
to measure a maximum-length 90-day statement spanning two continuously active
apps. The opt-in PostgreSQL test records logical and stored coverage bytes,
statement-row and relation sizes, statement-write and usage-fixture times, and
adjustment-plan/build time and Go allocation/retained-heap deltas. It adds one
late unit and verifies revision 2 bills exactly that unit, while a replay finds
no new usage. The fixture bulk-loads the already-aggregated minute table, so
these numbers characterize statement persistence and adjustment reads, not
request-event ingestion. Record the PostgreSQL version, local architecture,
command, and output with any comparison; this is a workload probe, not a
throughput or latency promise.

Two runs on 2026-09-30 with PostgreSQL 16.15 on macOS arm64:

| Measurement | Result |
|---|---:|
| Coverage records / public lines | 259,200 / 2 |
| Go JSON / PostgreSQL JSON text | 41,472,001 B / 43,545,600 B |
| Stored coverage / full statement relation | 1,311,946–1,313,087 B / 1,466,368 B |
| Usage-minute relation | 125,566,976 B |
| Statement write / statement read | 1.55–3.71 s / 469–544 ms |
| Usage-minute query / adjustment build | 1.14–5.37 s / 118–161 ms |
| Allocated during statement read / usage query / adjustment build | 238.6 MB / 306.2 MB / 90.1–90.2 MB |
| HeapAlloc delta before the next GC, same stages | 114.7 MB / 191.7 MB / 90.1–90.2 MB |

The compressed JSONB is small on disk for this repeated-minute workload, so a
normalized coverage table should not be justified on storage size alone. The
read/build path allocates over 600 MB cumulatively across its statement, usage,
and delta stages. HeapAlloc deltas are sampled immediately after each stage,
before another GC, so they include temporary objects. Timing varied across the
two runs; use this as a historical local baseline, not a production limit.

After moving adjustment planning into PostgreSQL, one rerun on 2026-09-30 with
the same PostgreSQL 16.15/macOS arm64 workload measured:

| Measurement | Result |
|---|---:|
| Coverage records / public lines | 259,200 / 2 |
| Go JSON / PostgreSQL JSON text | 41,472,001 B / 43,545,600 B |
| Stored coverage / full statement relation | 1,272,593 B / 1,425,408 B |
| Usage-minute relation | 125,566,976 B |
| Statement write / fixture copy | 623 ms / 5.30 s |
| Adjustment plan / build | 2.225 s / 18.9 μs |
| Allocated during plan / build | 14.2 KB / 3.2 KB |

The adjustment path now allocates about 17 KB in Go for this one-unit late
adjustment, compared with about 635 MB across the historical read/query/build
stages. PostgreSQL still scans and groups the private coverage to find deltas;
the local plan took 2.225 seconds, so this change removes large Go materialization
costs without establishing a latency guarantee. The scale test checks replay;
a companion PostgreSQL plan test checks the usage-regression path.

The public 20,000-line ceiling is removed. A full active 31-day month across two
apps can produce 89,280 minute coverage records and two public invoice lines per
day. Coverage remains stored in statement JSONB, so storage and adjustment reads
still grow linearly with usage. The opt-in 90-day check above establishes a
single-host baseline; production-like volume and concurrency qualification are
still required before making high-volume guarantees. Changing window sizes
after handoff is not a recovery strategy.

## Qualification sequence

Local evidence on 2026-09-30: `make test-customer-platform` passed with disposable
PostgreSQL 16 databases and a non-superuser customer role on macOS arm64. The
run used `GOFLAGS=-vet=off` because other local Go builds were competing for
temporary disk space; it ran the starter application/billing tests, live
PostgreSQL RLS tests, and selected state, owner API, gateway, scheduler and
authentication tests. This includes compact invoice lines, exact minute
coverage, additive adjustments, and the monthly billing lifecycle. It is local
evidence, not native Linux/KVM acceptance; CI separately uses Node 22.

Native smoke dispatch on 2026-09-30
([workflow run 36679321200](https://github.com/poyrazK/faas/actions/runs/36679321200))
stopped at GitHub OIDC authentication. GCP reports that the workload-identity
project is suspended; the acceptance instance was not started or contacted and
no hardware tests executed. Native qualification remains blocked on restoration
of that GCP project. The workflow accepts `main` only, so this run used the
current main commit, not this local working tree.

1. Run `make test-customer-platform` on disposable control-plane and customer
   databases. Preserve the commit, runtime versions, command, and test output.
2. Deploy the same starter to a native KVM test fleet. Repeat the customer
   lifecycle with warm, artifact cold-boot, and snapshot-restore instances.
3. Run concurrent customers during key rotation, suspension, retries, and
   dependency outages. Verify foreign data and results never cross boundaries.
4. Complete domain onboarding and external invoice handoff in the reference
   workflow. Use provider test credentials and stable per-statement references.
5. Exercise recovery and measure latency, request admission contention,
   accounting backlog, concurrency fairness, and adjustment accuracy. Publish
   measured limits before introducing availability or throughput promises.

## Remaining release boundaries

- Request counts do not reserve money or enforce per-customer CPU/concurrency.
  Benchmark PostgreSQL budget-row contention and define tenant fairness.
- Exit-time accounting has a pre-fsync crash window. Require reconciliation and
  explicit completeness checks before representing the ledger as invoice-grade.
- A shared app's database role and code can access its pooled data. Keep tenant
  isolation explicit, use a separate migration role, and define a dedicated
  tenant option before accepting arbitrary downstream customer code.
- Add end-user roles within each customer, a customer administration UI, and
  customer-data export/deletion. Platform tenancy alone is not end-user RBAC.
- Add platform capabilities to the maturity registry with operational owners,
  recovery procedures, plan entitlements, and qualifying evidence.

Passing local tests does not establish native deployment acceptance or customer
adoption. Record those results separately; do not promote capability maturity
solely because implementation merged.
