# ADR-202610151515 · Durable internal static hosting routes

- **Status:** accepted for a separately gated internal pilot; live provider qualification remains required.
- **Date:** 2026-10-15
- **Amends:** [ADR-20261015](20261015-internal-static-hosting.md), for new sites explicitly created in durable mode. Default leased sites retain their contract.

## Problem

Leased routes stop serving after 120 seconds without control-plane renewal and
require about 86,400 KV writes per site per 30-day month. Static delivery should
survive a Gregale API/database outage. Removing expiration from KV alone would
allow a delayed publication to replace newer intent: KV has no conditional
compare-and-swap for this key, and a request may commit after its caller times
out. A process/database lock alone does not fence such a provider request.

## Decision

Keep existing/default sites on leased KV routing. Add the default-false hot
setting `frontend_hosting_durable_routing_enabled`, bootstrapped from
`FAAS_FRONTEND_HOSTING_DURABLE_ROUTING_ENABLED`. It selects durable routing
**only when a new site is created**. The persisted mode never changes when the
flag changes. There is no implicit migration of existing sites.

The Worker has an independent, default-false variable with the same environment
name. With that switch on, it first reads a v2 routing head from the private R2
bucket at `hosting/routes/HOSTNAME.json`. Only an absent head may fall back to
a leased KV route. A disabled, deleted, malformed, or foreign-origin head must
deny serving, including when an old enabled KV entry or cached file exists.
Both protocols still require the original Worker switch and verified internal
Cloudflare Access identity before any route/file cache access.

R2's [conditional PutObject operations](https://developers.cloudflare.com/r2/api/s3/api/#object-level-operations)
atomically check the committed object's ETag or absence. The publisher reads the
head, rejects older/conflicting generations, and uses `If-Match` or
`If-None-Match: *` for every write. Concurrent or delayed requests cannot replace
a newer generation. Equal-generation retries/repair read and acknowledge the
same content without writing again. The canonical head includes the generation,
so consecutive generations have different contents. A deleted head is terminal:
even a higher generation cannot reactivate that hostname. Hostnames contain
fresh site UUIDs; a new site receives a new hostname.

Each durable site has one coalescing `hosting_route_publications` outbox row.
Creation, ready-release membership, promotion, retirement, suspension/resumption,
and deletion update its generation in the same transaction as intent. Publishers
read the site, releases, and generation from one repeatable-read snapshot.
Failed work stays pending across restarts, with a 30-second retry; an old failure
cannot defer a newer generation. Acknowledgements never advance past intent or
erase newer pending work. Unchanged heads are checked every six hours. Each
dispatch pass handles a bounded page with four concurrent publishers and a
30-second budget, so slow durable publications cannot indefinitely delay leased
route renewal. Snapshots read release membership without loading manifests.
Individual provider failures do not block other sites. The existing 30-second controller loop dispatches pending work and
performs artifact cleanup; it does not renew durable heads or write them on every
pass.

## Serving and cleanup

Durable heads have no time-based serving expiration. The Worker caches routing
metadata for at most 60 seconds, and file/manifest cache keys remain scoped to
host, origin where applicable, site, release, path, and digest. Neither warm nor
cold requests call Gregale. R2, Cloudflare Access, and the edge runtime remain
dependencies; this is control-plane independence, not origin/provider outage
tolerance.

`frontend_hosting_enabled` controls management admission. Disabling it expires
leased routes but leaves durable sites serving and keeps reconciliation/cleanup
running. The new creation flag also does not stop existing sites. The original
Worker switch stops all traffic before cache access; its durable-routing switch
is another independent gate. Account-owned `sites suspend` / `sites resume`
operations use revision CAS and publish disabled/enabled heads without changing
the active release. A 202 response acknowledges committed intent; revocation
still needs provider publication and metadata-cache propagation. If publication
is unavailable, the last accepted durable head continues serving; use the
independent Worker emergency switch when traffic must stop.

Retirement records the generation that excludes the release. Cleanup requires
an acknowledged head at that generation or newer, then waits the existing
nine-minute-thirty-second grace **from that acknowledgement**. Database retirement
age alone cannot qualify cleanup during a publication outage. Store deletion
guards repeat the receipt/grace check. A deleting site's catalog is purged only
after its tombstone is acknowledged and aged, including for an empty site.
Already-retiring releases preserve their individual receipt times.

Published routing tombstones are retained after SQL/artifact cleanup. They are
outside the staging/published-release prefixes and must never receive lifecycle
expiration or be deleted by artifact retirement. This leaves one small private
object per historical deleted site, charged as R2 storage. Removing those heads
or restoring provider/database state independently requires an explicit recovery
procedure; it is not ordinary garbage collection.

## Rollout and evidence

Upgrade every apid writer/controller before enabling durable creation. Older
binaries do not maintain the new outbox or cleanup guards and are not a supported
rollback while durable sites exist. The down migration refuses that state.
Retire/delete sites, verify cleanup/edge shutdown, and disable the new edge flag
before reverting the implementation. No cloud resource or runtime flag is
enabled merely by merging this change.

Portable tests exercise conditional creation/update races, terminal deletion,
unchanged repair, memory/PostgreSQL transactional snapshots, retries, CAS,
acknowledgement-based cleanup, admission separation, and warm/cold edge requests
twenty minutes after the last publication. The live qualification runner records
the actual site mode and exercises durable suspension/resumption. It does not
claim a live API/database outage test without that external qualification.

Durable sites perform no leased-KV heartbeat writes. Costs move to R2 head
reads on edge metadata-cache misses (per location), conditional writes on actual
changes, and about 120 periodic control-plane checks per idle site per 30-day
month. These are not free operations. No per-site VM, new daemon, listener,
customer entitlement, or billing model is introduced.

## Publication visibility and qualification

An account-owned internal status endpoint and `sites status` expose accepted
versus acknowledged generation, continuous pending age, bounded failure code,
attempt time, and retry schedule. Coalesced mutations retain pending age while
clearing prior-generation attempt metadata. Older receipts cannot clear newer
pending intent. Leased sites explicitly report untracked publication. A receipt
does not assert that every edge location has refreshed; durable responses also
identify the routing generation used after the normal authorization checks.

Publication/backlog Prometheus metrics use bounded outcomes without tenant or
resource labels and report observation freshness on database failure. Backlog
counts exclude periodic repair of already-acknowledged intent. The controller
reads the indexed pending subset once per pass with a bounded metrics timeout.

The qualification runner can exercise separately opted-in API/database faults,
publication isolation and coalescing, regional cold/warm observations, and actual
dedicated process/SQL footprint samples through an explicitly configured pilot
controller. It requires fault identity, independent bounded restoration, actual
failed API reads or pending provider retries, edge generation/cache evidence,
and recovery before claiming those checks. Provider billing and unobserved
phases stay unqualified. See the
[operator procedure](../internal-frontend-hosting-qualification.md).
