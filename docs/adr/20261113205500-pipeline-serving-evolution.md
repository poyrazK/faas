# ADR-20261113205500: Approved pipeline evolution, Auth memberships and related reads

- Status: implementation for local/staging qualification; production disabled
- Date: 2026-11-13
- Amends: ADR-1591, ADR-20261113160716, ADR-20261113181050

## Context

Customer portals need to add selected fields and tables without replacing their
mirror, inherit organization membership from Gregale Auth, and read related
records through the Data API. Each operation must preserve verification and
tenant isolation through retries, source outages and older worker deployments.

## Decision

An account-owned evolution endpoint saves a frozen preview and approves its
exact account, pipeline, generation, revision and prior serving contract. Version
2 serving contracts require `pipeline-evolution-v1` workers; the expand migration
retains this claim fence across binary rollback. Creation stays version 1.

Projection changes add columns or tables, separately. Primary keys and tenant
columns cannot be removed. Discovery validates the requested selection; the
worker produces a second, exact source-definition approval using the existing
schema/table receipts. Column DDL, bounded backfill, full verification, constraint
checks and API refresh precede exposure. Native apply remains suspended until
the source publication switches to the approved projection after the target DDL
receipt. Retries accept only the previous or desired exact publication. No
column removals, type changes or arbitrary SQL are accepted.

An approved Auth app binding maps at most 32 organization IDs to tenant values.
The worker rechecks account ownership, app deployment and the exact Auth database
binding before a bounded authoritative membership/user-lifecycle SELECT. The
private provider connection reads the customer Auth schema; this fixed query,
like existing endpoint metadata inspection, is outside control-catalog SQLC.
One complete snapshot replaces the derived memberships; removed, deleted and
banned users disappear. A periodic full reconciliation avoids event ordering,
lost-webhook and duplicate-delivery problems. The existing durable reader change
and target receipt serialize and acknowledge each change before source/index work.
Owner controls compare the observed reader-change ID atomically alongside the
configuration revision, so a concurrent automatic grant cannot race deletion.
Connecting or changing a mapping first revokes previous grants. JWT organization
claims never grant access. Each derived grant matches both the verified Auth
issuer and subject. The private target receipt records that issuer atomically
with the memberships, including replay after lost acknowledgement; identical
subjects from another Auth app inherit no rows. Manual tenant changes are
disabled while connected.

Every synchronized RLS predicate includes a worker-maintained expiration, at most
one minute after its proof. An unavailable Auth database or stopped worker cannot
renew it. SQL readers fail closed after expiry; the shared Data API hook returns
503 for unconfirmed membership. Admission/billing closure permits reductions but
cannot add or renew grants. Revocation remains independent of the replication
source. This is bounded eventual revocation, not instantaneous session revocation.

Owners declare up to 16 computed relationships, each joining at most four source
columns to the target's complete primary key. Worker-generated SQL runs as the
invoker, re-reads the source row by its primary key under RLS, joins the target
under RLS, and requires exact tenant equality. A forged composite RPC argument
cannot provide trusted foreign keys. Only receipt-owned functions are replaced;
PUBLIC execution is revoked, supported functions receive the existing exact RPC
opt-in annotation, and bounded read roles need SELECT on both dependencies.
No arbitrary SQL, aggregates or materialized transformations are accepted.

Related reads conservatively require the requested source table and every
reachable declared target to be readable, including top-level relationship RPCs.
A bounded recursive dependency closure handles cycles and nested relationships;
unrelated tables can keep serving during table onboarding. This ensures quarantined
dependencies return 503 instead of partial joined results. Removing relationships
uses another reviewed declaration; dependent tables cannot be retired until the
relationship is removed. Generation cleanup removes receipt-owned functions
before replacing row types. Schema cache refresh reuses durable runtime restart
receipts and must complete before an approved relationship change is applied.
New application roles also change the permission inventory and require another
acknowledged refresh before their related-read contracts are considered current.

## Validation and rollout

Apply the expand migration before capable control-plane and worker binaries.
Keep existing staging/account/capacity/provider gates and production disabled.
Exercise catalog approval races, actual previous-protocol claims, projected
backfill/CDC and lost acknowledgements, authoritative Auth updates/expiry/grace,
joined-read isolation, forged RPC inputs, dependency quarantine, refresh failure
and generation cleanup using native PostgreSQL and the pinned Data API fixture.
Local tests do not qualify a live Neon deployment.
