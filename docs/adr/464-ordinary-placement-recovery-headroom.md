# ADR-464: Ordinary placement preserves regional recovery headroom

- **Status:** accepted; fleet enable requires native qualification
- **Date:** 2026-10-02
- **Milestone:** Multi-region compute placement
- **Related:** ADR-422, ADR-424, ADR-463

## Problem

ADR-463 atomically rejects ordinary resource growth that consumes regional
service recovery capacity. Ordinary placement only checks physical capacity.
An unrestricted wake can repeatedly choose its preferred EU host and be refused,
even when a US host can safely admit it. Service candidate flags cannot answer
this question: an ordinary guest shrinks the available slot budgets, and its
resource shape may differ from a service replica.

## Decision

Extend the existing internal capacity projection with each healthy host's
remaining RAM, CPU quota and guest-vCPU budgets after ordinary usage and before
service residency. Preserve fractional slot headroom. Include an internal
`ordinary_inputs` qualifier; a scheduler requiring these fields refuses ordinary
growth against an older enabled projection. Public operator reports remain typed
aggregate projections and do not expose these node budgets.

For an ordinary candidate, subtract its resource cost from the remaining budgets
and recompute that host's uniform service slots using ADR-463's current shape.
Require existing service survivors to fit, the global host-loss certificate to
hold, the affected region's pinned reservation to survive every host loss beside
fixed unrestricted survivors, and a stable final placement for all pending
unrestricted service intent. Reuse ADR-463's bounded binary solver for the
affected region; cache unchanged regions' maxima within the placement snapshot.
No exhaustive search or per-host database query is used in production.

Match durable insert-time costs: main RAM plus VM overhead and deployment
sidecars; at least the certificate's default main CPU quota plus configured
sidecar quotas (defaulting missing quotas); and the account plan's guest topology.
Standalone jobs use one guest vCPU. Deployment ownership must match the app.
Enabled ordinary app admission requires a target deployment so sidecar costs
cannot silently disappear. Physical placement still checks startup CPU boost.

All VM placement paths consult the certificate when enabled. Normal service
wakes and prime use declared service candidate flags without requiring a
replica-convergence context. Mirrors, warm fills and internal app tasks are
ordinary residents even when bound to service apps. Jobs are ordinary residents.
Owner claims bypass prospective allocation checks because they allocate no VM.
Selected-region eligibility takes priority over affinity; a pinned request never
spills to another region. Ordinary growth requires a protected certificate,
whereas declared service recovery retains ADR-463's degraded-incident rules.

These are advisory placement checks. The serialized database after-write guard
remains authoritative for concurrent allocation, plan/deployment changes,
heartbeat expiry, direct SQL and older schedulers. A stale safe hint cannot
authorize an unsafe write.

## Migration and qualification

Migration `20261002200131931_service_capacity_ordinary_placement.sql` only adds
private input metadata. Both directions lock the capacity policy before replacing
the input function. They preserve the enabled setting and the full ADR-463
admission certificate. Downgrade removes ordinary hints; the new scheduler then
refuses ordinary growth while enabled. Operators must upgrade the schema before
using this scheduler with protection enabled. The disabled default is retained.

Shared MemStore/Postgres tests reproduce unsafe preferred-host selection and
safe foreign placement, sidecar bounds, pinned jobs, service recovery and owner
claims. A deterministic independent oracle enumerates small fleets' final legacy
placements and host losses after prospective resource reductions. Postgres replay,
downgrade and stale-hint writes verify the durable boundary.

Native Linux x86_64 KVM host-loss/routing tests, admission-throughput qualification,
`test-metal` and `leakcheck` remain fleet-enable gates. This increment does not
establish whole-region failover, independent regional control planes or metadata
and storage durability.
