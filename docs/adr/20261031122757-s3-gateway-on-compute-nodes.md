# ADR: The S3 gateway runs on every compute node with a capped port share, and plans limit monthly egress

Date: 2026-10-31
Status: Accepted
Amends: [ADR-1343](1343-app-edge-on-compute-nodes.md) (compute nodes hold object-storage credentials and serve public object routes locally), [ADR-1430](1430-plan-entitlements-from-october-2026-cogs-report.md) (paid plans get a plan egress ceiling)
Related: [ADR-20261030075301](20261030075301-object-storage-reads-off-the-database.md), [ADR 20261022111812](20261022111812-seal-domains-per-reader.md), [ADR-20261029174627](20261029174627-object-storage-backend-pools.md), [ADR-20261024132844](20261024132844-gateway-metered-object-storage-billing.md), [ADR-1320](1320-object-storage-plan-allowances-ceilings-and-overage-cap.md), [ADR-627](627-prospective-gateway-storage-safety-accounting.md)

## Context

Every byte of every customer object crosses one `s3-gatewayd` on the control
plane:

- It runs on the control-plane VM (two vCPUs, GCP) in `faas-cp.slice` with
  `MemoryMax=512M`, behind Cloudflare and the control plane's Caddy. The
  registry declares the `proxied` transfer profile (64 MiB per request), and
  production allows four concurrent uploads and a 256 MiB spool.
- Every object byte pays GCP egress, while ADR-1430 makes customer egress
  free.
- The compute edge forwards public object reads and uploads to the control
  plane's gatewayd-public (ADR-1343), because compute nodes hold no
  storage-provider credentials. Those bytes cross the control plane too.
- It is a single point of failure for object storage.

The customer endpoint `s3.gregale.dev` and the gateway's features
(conditional writes, checksums, durability receipts, key fences, bucket CORS,
encryption and Object Lock handling) stay. Customers therefore cannot talk to
the provider directly, and every byte goes through a gateway Gregale runs.

That gateway has to fit the unit economics. ADR-1430 prices stored
GiB-months only, and egress is free. The COGS report behind it costed
provider storage with no gateway tier. A gateway's cost follows traffic, not
stored bytes, and dedicated machines would add a fixed floor (two, for
redundancy) before the first customer. Plans also have no egress ceiling of
their own (`ObjectStoragePlanLimits.MaxMonthlyEgressBytes` is zero on every
plan), so a storage-light, traffic-heavy account is bounded only by the
operator's safety policy.

Two things make replicas possible now:

- Since ADR-20261030075301 a gateway read makes no per-request database call.
  Decisions, fenced-credential checks and credential resolutions are cached
  per process, and counts are flushed in batches. Writes still run four to
  five control-plane transactions per PUT or part. That is a separate
  decision.
- Compute nodes have unmetered ports (1 Gbit/s on the current Hetzner nodes),
  and every compute node already runs a replica of the cell's Cloudflare
  Tunnel connector (ADR-1343).

## Decision

### 1. Every compute node runs the gateway

- `s3-gatewayd` runs on every compute node of a cell and listens on loopback
  only (`127.0.0.1:8084`). gatewayd-public stays the only public listener on a
  node.
- The compute variant of the unit drops `Requires=postgresql.service`. It
  reads the control-plane database through `/etc/faas/compute-db.env`, like
  the other compute daemons, with the existing pool of two connections.
- The node keeps the gateway's RAM and CPU out of tenant admission:
  `api.ComputeObjectGatewayReserveMB` (512, the unit's `MemoryMax`) and
  `api.ComputeObjectGatewayCPUHoldbackMillicores` (1,000, sized by the load
  test in step 5 below), next to `ComputeEdgeDaemonReserveMB`.

### 2. `s3.gregale.dev` reaches the nodes through its own tunnel

- Each cell gets a second remotely managed tunnel for `s3.<domain>`. Every
  compute node runs its connector, `faas-cloudflared-s3`, with service
  `http://127.0.0.1:8084`.
- The connector is bound to the local gateway (`BindsTo=` and readiness on
  the gateway's control listener). A node whose gateway is down leaves the
  tunnel, and Cloudflare sends its requests to the other replicas. The app
  tunnel is not affected.
- The transfer profile stays `proxied`: 64 MiB per request, and multipart
  for larger objects.

### 3. The gateway takes at most a fixed share of the node's port

- Each gateway limits its transfer to `api.ObjectGatewayNodePortSharePercent`
  (30) of the node's port speed, per direction.
- Uploads and downloads draw on one budget. Every byte a gateway moves
  crosses the port once each way (in from the provider and out to the
  client for a read, the reverse for a write), so one budget of the share
  keeps both directions within it.
- The manifest declares each node's port speed. Ansible renders the share
  into the registry as `transfer.max_bytes_per_second`.
- Over the share, transfers slow down through TCP backpressure; they do not
  fail. App traffic keeps the rest of the port. Time a request body waits
  for the share does not count against the client's minimum upload rate.
- `s3_gateway_transfer_throttled_seconds_total{direction}` counts the
  wall-clock seconds during which a transfer waited for the share.

### 4. Compute nodes hold storage credentials, scoped to the gateway

This amends ADR-1343's "compute nodes never receive storage-provider
credentials".

- **Seal domain.** A compute node receives only the storage seal-domain
  identity (`domains/storage.age`), and only once that domain has contracted
  (the contract step of ADR 20261022111812 §6). It never receives
  `fleet.age` or `host.age` for the gateway. A gateway on a compute node
  refuses to start with a fleet identity.
- **Provider credential.** The gateway's provider credential is separate
  from apid's, which alone creates and deletes buckets. It allows object
  operations (get, put, copy, delete, versions, tagging, retention and legal
  hold), listing and multipart, and the bucket settings the S3 API forwards
  (versioning, encryption and Object Lock configuration). It does not allow
  bucket create or delete, CORS, policy or ACL. On OVH this is a separate S3
  user with that policy. The registry gains a per-backend gateway credential
  (`gateway_access_key_env`, `gateway_secret_key_env`) that s3-gatewayd and
  gatewayd-public use, and apid keeps the bucket-admin one.
- **Usage exports stay on the control plane.** A compute gateway runs no
  provider usage export, so it never holds the provider's account API keys
  (OVH application key, secret and consumer key), which reach more than
  storage.
- **Blast radius.** Root on a compute node can read and write every object
  in the cell's backends and open every S3 credential. The node already holds
  `compute-db.env`, which gives row DML on the shared schema. Tenant code
  runs jailed under a unique uid (spec §11) and needs host root to read
  either.

### 5. The compute edge serves public object routes itself

Once the node holds the gateway credential, gatewayd-public in compute edge
mode serves public object reads and uploads locally, with the cached
admission from ADR-20261030075301 step 2. It no longer forwards them to the
control plane. This removes the last per-byte path through the control plane.

### 6. Plans limit monthly egress

This amends ADR-1430's "paid plans keep the operator safety policy as their
egress ceiling".

- `ObjectStoragePlanLimits.MaxMonthlyEgressBytes` is
  `api.ObjectStorageEgressPerIncludedByte` (10) times the plan's included
  storage:

  | Plan | Included storage | Monthly egress limit |
  |---|---|---|
  | Free | 1 GiB | 10 GiB |
  | Hobby | 10 GiB | 100 GiB |
  | Pro | 50 GiB | 500 GiB |
  | Scale | 200 GiB | 2,000 GiB |

- Egress stays unpriced; this is a limit, not a meter.
- At the limit, the account's requests other than deletes are refused until
  the month resets, as at the operator's egress ceiling today: admission
  checks the month's egress before every request, not only reads. The error
  names the limit, the observed value and the docs URL, through
  `s3.gregale.dev` as well as the API.
- The count is the gateway-safety egress counter that already exists. Public
  reads Cloudflare serves from cache never reach a gateway and do not count.
- The limits live in `pkg/api/limits.go` and are generated into
  `docs/plans.md`.

### 7. Move to dedicated gateway machines at a traffic threshold

- `FaasS3GatewayPortShareSaturated` fires when a cell's gateways spend more
  than 10% of a week's seconds at their port share.
- The operator then moves that cell to dedicated gateway machines:
  - no tenant VMs on them;
  - in the same provider region as the cell's buckets;
  - `s3.gregale.dev` DNS-only, with the `direct` transfer profile.
- Their count, placement and TLS are decided by a later ADR, written when the
  alert first fires. The traffic that fires it is large enough to pay for
  them.

### Steps

1. Plan egress limits: `pkg/api/limits.go`, `docs/plans.md` and the customer
   object-storage limits page. Independent of the rest; can ship first.
2. Gateway credential split in the registry, and the storage seal domain
   contracted.
3. Port-share limiter, metric and alert.
4. Ansible:
   - `s3_gateway_service` in the compute play, with the compute unit variant;
   - the `faas-cloudflared-s3` connector;
   - manifest `object_storage.gateway_on_compute` and per-node port speed;
   - the two reserves: the node join runs `gregalectl compute-nodes sizing
     --edge --object-gateway`, which registers the node's tenant capacity
     without them.
5. Load test on one node, as the acceptance gate. Measure small GETs per
   second, large-GET throughput at the share, and PUTs per second for one
   account. It passes when:
   - the gateway holds its share;
   - the node's `gateway_wake_latency_seconds` p95 stays within noise;
   - control-plane Postgres stays under its CPU alert.

   The test reaches the compute gateways on a pilot hostname
   (`faas_object_gateway_public_endpoint`, the s3 tunnel's only ingress
   until the cutover) while `s3.<domain>` stays on the control plane.
   Procedure and readout: `docs/runbooks/S3GatewayComputeLoadTest.md`.
6. Cutover of `s3.gregale.dev` from the control-plane origin to the s3
   tunnel, one pilot first (as in `docs/runbooks/AppEdgeCutover.md`). The
   control-plane gateway stays as a fallback for one release, then leaves
   the route. It keeps running the provider usage exports until they move
   to apid.
7. The compute edge serves public object routes locally.

## Consequences

- **Cost.** No new machines.
  - Each node gives up 512 MB of tenant RAM, about €1.10 a month at the
    COGS report's €0.003 per GB-RAM-hour, plus one core of tenant CPU
    admission and up to 30% of its port.
  - Bytes cost nothing on unmetered ports, and GCP egress for object bytes
    ends.
- **Capacity.**
  - At a 1 Gbit/s port, each node carries about 37 MB/s per direction,
    about 100 TB a month if it ran at its share continuously. The fleet's
    capacity grows with its nodes.
  - Reads need no per-request database call. CPU and the provider's
    per-bucket rate (OVH: 300 PUT/s and 900 GET/s by default) bound them.
- **Writes.** Writes stay bound by control-plane Postgres: four to five
  transactions per PUT or part, and a pool of two per gateway. The estimate
  is 100–200 writes per second fleet-wide. Moving write state off the
  control-plane database is the next decision.
- **Per-customer cost is bounded.** On dedicated machines, at roughly €0.5–1
  per TB carried, the egress limit caps a Scale account at about €2 a month
  of gateway cost and a Pro account at about €0.50.
- **Several processes.** With N gateways:
  - the monthly ceilings are approximate by N × (2 s + 1 s) of a bucket's
    reads (ADR-20261030075301);
  - one account can make up to N × 100 requests per second;
  - upload slots and spool are per node.
- **Load spread.** Cloudflare decides how traffic spreads across tunnel
  replicas. The per-node share protects tenants whatever the split.
- **Cloudflare stays in the byte path while gateways run on compute nodes.**
  Its request body limit is already covered by the 64 MiB cap. Its CDN
  terms on bulk non-web content are a risk at volume. The dedicated-machine
  step leaves Cloudflare's proxy.
- **Security.** Compute nodes hold object-storage credentials (§4).
- **New customer-visible limit.** A customer over their plan's egress limit
  can only delete until the month ends. Letting writes continue would need
  admission to skip the egress check for requests that send no body; that
  changes the operator ceiling too and is left to a later decision.

## Alternatives considered

- **Dedicated gateway machines now.** A fixed floor of at least two machines
  before the first customer. Deferred to the threshold in §7.
- **Customers use the provider directly**, with per-bucket provider users.
  No byte path at all, but it gives up `s3.gregale.dev` and the gateway's
  features.
- **The gateway as a Cloudflare Worker.** Cloudflare would carry the bytes,
  but it reverses the decision to keep Cloudflare to safety only and means
  rewriting the gateway.
- **A bigger control-plane VM.** It keeps GCP egress on every byte, the
  control plane's saturation and the single point of failure.
- **Price egress.** Contradicts ADR-1430's zero egress price and the rule
  that resource-holding features get plan limits, not meters.
