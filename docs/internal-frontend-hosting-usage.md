# Internal hosting storage and cost estimates

This extension remains under the existing default-off hosting API, operator/MFA
checks and operations-console build flag. It does not enable customer billing or
collect visitor request counts. See [ADR-202610161126](adr/202610161126-hosting-account-storage-reservations.md).

## Account limits

| Resource | Internal pilot cap |
|---|---|
| Reserved release artifact storage | 1 GiB per account |
| Pending upload reservations | 4 per account |
| File / release payload | Existing 32 MiB / 100 MiB |
| Files / retained releases | Existing 1,000 per release / 10 per site |

Each release reserves `2 × manifest file bytes + 512 KiB`: room for staging,
published files and a maximum-size manifest. Empty files still reserve manifest
space. No cross-release deduplication is assumed. All projects, environments and
configured origins in the same account share the cap.

The reservation starts **before** upload URLs can be issued. Finalization changes
a pending release to ready, freeing its pending slot without releasing storage.
Ready payload includes all verified ready releases, including rollback targets.
Promoting or rolling back does not allocate more bytes. Retiring a release or
requesting site deletion does not free storage: the existing publication and
upload/cache grace, artifact removal and catalog deletion must finish first.
Failed cleanup retains the reservation and retries through the existing controller.
Pending releases do not expire automatically by default; finish or retire abandoned uploads. The separate [retention opt-in](internal-frontend-hosting-retention.md) can retire idle uploads and expired never-published CI releases under an explicitly enabled per-site policy.

Capacity admission locks the account before the site and checks usage in the same
transaction as release insertion, including concurrent deployments to other sites.
A refused request allocates no release or upload grant. HTTP 409 problems use
`hosting_storage_quota_exceeded` or `hosting_pending_upload_quota_exceeded`, with
`limit`, `observed` (attempted usage) and `docs_url`. Existing retained-release/site
quota errors keep their existing code. Refresh before retrying uncertain intents.

Disable hosting API management/admission during the apid rollout; keep the
independent Worker serving switches unchanged for durable sites. Legacy leased
sites follow the existing API admission/renewal switch and may stop serving
during management shutdown; use a maintenance window or durable routing. Upgrade every hosting API reader
and writer before re-enabling management. Older SQLC readers use `SELECT *` and
cannot scan the new release column during a mixed-version rollout. The migration backfills
existing manifests without eviction. Existing over-quota accounts remain readable
and can retire/clean up releases; new admission waits for capacity. Old release
writers cannot populate the new non-null accounting column and must be upgraded.
Drain release rows before downgrade. Flags, existing serving and rollback behavior
are otherwise unchanged by this milestone.

## Read usage

`GET /v1/internal/hosting/usage` returns only the authenticated operator's account:

- `reserved_bytes`, `storage_limit_bytes`: admitted artifact reservation/cap.
- `payload_bytes`: declared payload across all retained releases.
- `ready_payload_bytes`: payload of releases whose publication was verified.
- `retiring_reserved_bytes`: reservation awaiting artifact/catalog cleanup.
- `pending_uploads`, `pending_uploads_limit`: unfinalized release slots/cap.
- `releases`, `account_id`, `observed_at`: retained catalog count and observation.

The route uses read scope, MFA, operator authorization and the main hosting flag.
It returns `Cache-Control: no-store` and reaches PostgreSQL only. Capabilities also
advertise `account_storage_max_bytes` and `account_pending_uploads_max`.

The operations dashboard polls this view and offers explicit refresh. A failed
read is marked unavailable, never presented as zero usage. The API enforces caps
regardless of stale dashboard data or a client bypassing the interface.

These are **catalog accounting** figures, not an inventory of R2. Pending files
may be missing, publication can be partial, and staging has its own lifecycle.
The reservation conservatively covers both copies while their release exists.
Permanent routing/domain tombstones, manual/orphan objects and object versions
are outside this quota; it is not a physical ceiling on the entire provider
bucket. Do not enable object versioning or unrelated writers on the pilot bucket.
Pending slots are not network concurrency, a daily upload-byte meter or operation
counts. Existing signed-URL expiry/size and API/publisher limits still apply.

## Estimate provider usage cost

The dashboard's expandable calculator keeps assumed request/CPU counts separate
from the catalog observation. Storage assumes the current reserved bytes are held
for a complete month, with decimal GB units for provider pricing (quota displays
use binary MiB/GiB). Enter R2 Class A and Class B operation counts separately from
Worker requests and CPU milliseconds: cache hits, retries, publication, metadata
repair and multiple object reads prevent a one-to-one mapping between these.

Public usage-rate snapshot checked on 2026-10-16:

| Usage | Rate in USD |
|---|---|
| Standard R2 storage | $0.015 / GB-month |
| R2 Class A | $4.50 / million operations |
| R2 Class B | $0.36 / million operations |
| Workers Paid requests | $0.30 / million |
| Workers Paid CPU | $0.02 / million CPU milliseconds |

Sources: [R2 pricing](https://developers.cloudflare.com/r2/pricing/) and
[Workers pricing](https://developers.cloudflare.com/workers/platform/pricing/).
The calculator uses integer USD millicents; displayed cents round upward.
It rounds R2 storage up to whole GB-months and each operation class to whole
millions, matching the provider’s aggregate billing units. It reports gross usage
**before** included allowances. Provider rounding/allowances apply to the shared
Cloudflare account, so this is not a per-Gregale-account bill allocation. It excludes the shared $5/month Workers Paid base, SaaS/Access/domain
charges, taxes and Gregale infrastructure. Other workloads share account-wide
allowances. Consult provider billing for actual charged usage. Nothing here
changes subscriptions, customer entitlements or invoices.

Portable validation includes real PostgreSQL and memory lifecycle/concurrency,
provider-cleanup failure retention, migration backfill and refusal of old writers,
API gates, integer cost arithmetic, and the local browser workflow. None of that
is evidence of live traffic, provider resource usage or actual invoices.
