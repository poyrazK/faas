# ADR-20261101085552: Request records stay off the control-plane database

- **Status:** proposed
- **Date:** 2026-11-01
- **Amends:** ADR-634 and ADR-1521 (request-ID journal), ADR-127 (production
  debugger: `request_telemetry` and its HTTP log projection). It is the next
  Stage 1 step after ADR-1521 and ADR-1520 (rate-limit token leasing) toward
  the scaling roadmap's rule: no control-plane round trip per request.
- **Decision:** the two control-plane writes that still grow with request rate
  change shape.
  1. **The request-ID journal lives on the compute node that served the
     request.**
     - gatewayd-internal appends each journal record to segment files in its
       state directory (`/var/lib/faas-gatewayd-internal`). There is one file
       per 10 minutes of received time (`GatewayRequestJournalSegmentMinutes`)
       holding CRC-checked zstd frames, plus an index sorted by key hash that is
       written when the segment closes. Ten minutes keeps the in-memory sort at
       close to about 7 MB at 1,000 req/s; an hour would be about 43 MB inside a
       gateway whose soft memory limit is 384 MiB. A record keeps today's fields (account,
       app, request ID, trace ID, received at, expires at) and today's rule:
       a plan without the debugger entitlement writes nothing.
     - A segment is removed once every record in it has passed `expires_at`
       (3, 7 or 14 days by plan). A per-node disk budget in `pkg/api/limits.go`
       bounds the directory. Past the budget the oldest hour goes first, and
       `gateway_request_id_journal_evicted_total` counts it.
     - apid resolves a public request ID through the `RequestIDLookups`
       service it serves beside `RequestTelemetry`. Each gateway that keeps a
       local journal holds one stream open to apid over its existing
       request-telemetry connection: its first message names its node, then
       apid sends questions (account, app, identifier, window) down the stream
       and the gateway answers each one. Compute nodes take no new inbound port
       or certificate, and apid knows exactly which gateways are connected.
       apid asks every connected gateway at once, with a one-second deadline
       each, and takes the newest hit. A gateway that does not answer is
       logged as an incomplete lookup. The private mTLS listener is the trust
       boundary, as for the other gateway telemetry services. The debug lookup
       asks the nodes only after the control-plane table misses.
     - Request IDs the platform mints carry their minting time: the first 48
       bits are Unix milliseconds (UUIDv7 layout), the rest random, still 32
       hex characters. For such IDs apid narrows the window to that time ±1
       minute. Client-supplied IDs keep the full retention window.
     - Once every gateway writes locally, the control-plane table
       `request_id_journal` receives no new rows. During that overlap apid
       reads it as well as the nodes. After its last row expires (14 days), a
       contract migration drops it.
  2. **Request telemetry ships one row per bucket per minute.**
     - The publisher keeps each minute's buckets open and ships the minute
       once, 5 s after it closes. Today it collapses each 5-second batch on
       its own, so a key that stays busy for a whole minute is written up to
       12 times. The bucket key, the bucket widths and the row shape do not
       change, so the ~45 queries that read `request_telemetry` and its HTTP
       log projection in `log_events` stay as they are.
     - A per-gateway bound on open keys lives in `pkg/api/limits.go`. Past the
       bound, the oldest open minute ships early, as a batch does today.
     - Exact rows (`preserveExact`, resolved scenario targets) still ship on
       the next flush.
  3. **Out of scope here:** per-request control-plane work that is already
     bounded or has its own decision. That covers informational audit rows (one
     per rule or instance per minute since #6197), app errors
     (ADR-20261031104048), rate-limit tokens (leased, ADR-1520) and usage
     events (per instance-minute).
- **Why:** the 2-core control plane is the next ceiling once the compute
  edge carries ingress. On 2026-10-31 (rc.312) a stepped load test from the
  public internet served 400 req/s cleanly and about 500 at the limit.
  `pg_stat_statements` on gregale-cp, comparing ~290 req/s on three warm apps
  against an equal idle window, showed what traffic adds:

  | Statement | Added per second | Exec ms per second |
  |---|---|---|
  | `RecordRequestIDJournalBatch` | 8.1 batches (≈290 rows) | 44.5 |
  | `insert into events` (before #6197) | 96 | 39.1 |
  | `insert into log_events` (HTTP projection) | 23 | 17.8 |
  | `InsertRequestTelemetry` | 23 | 13.0 |
  | `ConsumeTrafficRateTokens` | 16 | 4.1 |

  Postgres CPU rose from 0.32 to 0.52 cores (0.31 to 0.67 in an earlier run).
  That is 0.7–1.2 ms per request on a host that also runs apid and schedd, so
  the control plane saturates near 1,000 req/s.

  The journal is the only write that adds one row per request. At 1,000 req/s
  on Scale it would hold about 1.2 billion rows (14 days) on the
  control-plane disk, and it is read only by the debug request-ID lookup.
  Telemetry rows grow with distinct keys per 5-second batch rather than per
  minute, so the same busy minute is paid for repeatedly.
- **Consequences:**
  - A request-ID lookup becomes a fan-out across compute gateways. It takes as
    long as the slowest node, bounded at one second. A request served by an
    unreachable or retired node does not resolve, and the answer says so.
    ADR-634 already accepts journal loss; sampled telemetry and traces still
    cover those requests.
  - apid gains a read-only lookup path into compute gateways, carried on the
    connection gateways already open to it.
  - Compute disk carries the journal, estimated at ~60 bytes per record with
    its index. 1,000 req/s for 14 days is roughly 70 GB across the fleet, and
    the per-node budget caps it.
  - Telemetry and HTTP log rows appear up to ~65 s after the request instead
    of ~5 s; the dashboards that read them are minute-grained. A gateway crash
    or restart loses the open minute, not one batch, which widens ADR-634's
    loss allowance for telemetry.
  - Mixed versions: the service is additive. An apid that predates it answers
    `Unimplemented`, and the gateway retries every five minutes. apid reads the
    control-plane table first throughout. Gateways keep sending journal
    batches to apid until `FAAS_REQUEST_ID_JOURNAL_LOCAL=1` (default off),
    which is turned on only once every apid serves the lookups. The publisher change sits behind
    `FAAS_REQUEST_TELEMETRY_MINUTE_COLLAPSE` (default off).
- **Rollout:** one step per release, each followed by the 300 → 600 req/s
  step test and the `pg_stat_statements` load-vs-idle diff.
  1. Telemetry minute collapse.
  2. Time-prefixed request IDs.
  3. The node-local store, the `RequestIDLookups` stream and apid's fan-out,
     reading both sources.
  4. Local journal writes behind `FAAS_REQUEST_ID_JOURNAL_LOCAL`.
  5. 14 days after the switch is on everywhere, drop `request_id_journal`.

  Target: fewer than 0.05 control-plane statements per warm request, and
  Postgres CPU flat as request rate rises.
- **Rejected alternatives:**
  - Compressed per-minute journal rows in control-plane Postgres. This keeps
    apid the only writer and needs no new RPC. But the control plane still
    stores every ID, and a lookup without a time hint scans up to 20,000 rows
    per app and gateway over 14 days.
  - Journal segments in object storage. They would survive node loss, but
    gatewayd-internal holds no storage credentials, and the debugger would
    then depend on the object store. The segment format leaves room to ship
    closed segments there later.
  - Sampling the journal: ADR-1521 keeps the promise that every request ID of
    an entitled plan resolves.
  - A larger control-plane host: it moves the ceiling, not the slope.
- **Open question for the owner:** node-local versus object storage for
  journal durability. Node-local is proposed first because it adds no new
  dependency or credential.
