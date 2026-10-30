# ADR: Object storage reads are admitted from a cached decision, not a locking transaction

Date: 2026-10-30
Status: Accepted
Related: [ADR-627](627-prospective-gateway-storage-safety-accounting.md), [ADR-20261024132844](20261024132844-gateway-metered-object-storage-billing.md), [ADR 20261023223000](20261023223000-object-storage-bucket-scoped-availability.md), [ADR-20261028083324](20261028083324-budget-admission-outage-grace.md)

## Context

Under gateway-safety accounting (production's mode), every S3 `GET` through
`s3-gatewayd` runs three write transactions on the control-plane database
before the body leaves Gregale:

1. `AdmitObjectURL`: locks the bucket's organization and
   `SELECT … FROM accounts … FOR UPDATE`, reads the usage snapshot, checks the
   owner's status, inventory freshness and monthly ceilings, and increments
   the owner's monthly authorizations.
2. `ReserveObjectStorageGatewayRequest`: takes the same locks, checks the
   ceilings again and increments the bucket's monthly request count.
3. `ReserveObjectStorageGatewayEgress`: takes the same locks and reserves the
   response's bytes against the monthly egress ceiling.

The locks make "check the ceiling, then count" exact. They also serialize all
of an account's reads with each other and with every other writer of the
account row, put two to three round trips on a saturated two-vCPU control
plane per request, and fail every read when that database stalls (Hunt H30).
Public bucket reads through `gatewayd-public` run the same locking
admission for anonymous visitors.

Reads do not change what a bucket holds. What they consume is three monthly
safety budgets: requests, egress and authorizations. Customers are not
charged for any of them (ADR-1430 prices stored bytes only); the ceilings
protect the operator from runaway provider and network cost. A ceiling that
can be passed by a few seconds of traffic still does that job.

Authenticated S3 traffic also has no per-second limit: the only bound is the
monthly counters, enforced by those same writes.

## Decision

1. **Reads are admitted from a cached decision.** For `GET` and `HEAD` under
   gateway-safety accounting, the gateway asks the store for a bucket's read
   decision with plain reads and no row locks: whether the owner is active
   and not on an abuse hold, whether the bucket is ready and freshly
   inventoried, and the month's request, egress and authorization counts
   with their plan-tightened ceilings. The decision is the one
   `AdmitObjectURL` computes for a read, so the refusals and error codes are
   unchanged.
2. **Each gateway process caches a decision for up to
   `api.ObjectReadAdmissionTTL` (2 s).** Concurrent misses for one bucket
   share one read. When a refresh fails, an admitting decision keeps serving
   for up to `api.ObjectReadAdmissionMaxStale` (30 s) and then reads fail
   closed, as they do today. A suspension, abuse hold, deletion or stale
   inventory therefore stops reads within 2 s, or 30 s while the database is
   unreachable.
3. **Counts are batched.** The gateway adds each admitted read's request,
   authorization and egress bytes to in-memory per-bucket totals, and checks
   ceilings against the decision's counts plus those totals. A flush every
   `api.ObjectReadUsageFlushInterval` (1 s) writes them in one transaction of
   counter upserts, without owner locks. A failed flush keeps the totals for
   the next one. A gateway that crashes loses at most one interval of counts.
   Ceilings stay exact within a process; across processes they can be
   exceeded by what the others admit within one TTL plus one flush interval.
4. **Each account gets a per-second limit.** Every authenticated S3 request
   takes a token from its bucket's owner's bucket in that process:
   `api.ObjectStorageAccountRequestsPerSecond` (100) with a burst of
   `api.ObjectStorageAccountRequestBurst` (200). An empty bucket answers
   `503 SlowDown`, the code S3 clients retry with backoff.
5. **Writes keep the locking path.** Uploads, copies, deletes and settings
   reserve capacity, take key fences and write receipts, which must stay
   exact; they keep their transactions. So does provider-report accounting,
   and so does a request whose credential carries an organization access
   fence, until step 3 below.

### Steps

1. `s3-gatewayd` `GET`/`HEAD` under gateway safety: cached decisions, batched
   counts, per-account limit (this change).
2. Public bucket reads in `gatewayd-public` use the same admitter.
3. Organization-fenced credentials: cache the fence check per credential and
   membership revision with the same TTL, and move those reads to the cache.
4. Cache resolved credentials and invalidate them on revocation through
   `pg_notify`, so a read needs no database round trip at all. Amended
   below: a 2 s cache for reads, without `pg_notify`.

Resumable inventory scans, which bound how many objects a bucket can hold,
are a separate decision.

## Consequences

- A read no longer locks the account row or writes to the database. A busy
  bucket costs one lock-free read per bucket every 2 s and one batched write
  per process per second.
- Suspensions, abuse holds and bucket deletion reach reads within 2 s instead
  of immediately, and within 30 s during a database outage. Writes still see
  them at once.
- The monthly request, egress and authorization ceilings become approximate:
  N gateway processes can pass one by up to N × (2 s + 1 s) of that bucket's
  read traffic. Egress reservations are no longer crash-safe; a crash loses
  at most 1 s of counts. None of these meters is billed.
- One account can no longer saturate the gateway or the database with reads;
  a client over the limit gets `SlowDown` and backs off.
- Credentials are still resolved per request, and organization-fenced
  credentials still take the locking path, until steps 3 and 4.

## Alternatives considered

- **Keep exact ceilings with an advisory lock per bucket instead of the
  account row.** Removes contention with other account writers but still
  costs three write transactions per read and still fails every read when
  the database stalls.
- **Count in Prometheus or logs and reconcile later.** Moves the ceiling out
  of the request path entirely, but a runaway client would then go unchecked
  until the next reconciliation.
- **Plan-based rate limits.** Deferred until plans price requests; one
  operator constant bounds abuse now.

## Amendment 1 (2026-10-31): step 4 caches credentials for reads, without `pg_notify`

Whether an access key still resolves depends on six tables: the credential,
its bucket, the bucket's organization or account (status, abuse hold,
pending deletion), the issuer's membership, and the environment clone that
owns the bucket. Invalidating a long-lived cache through `pg_notify` would
need triggers on all of them, including `accounts` and `org_memberships`,
which other daemons write constantly, plus a listener and resync handling in
the gateway. The other decisions in this ADR already accept that a
suspension reaches reads within 2 s.

So `s3-gatewayd` caches a resolved credential, with its opened secret, for
`api.ObjectReadAdmissionTTL` (2 s), and serves it for up to
`api.ObjectReadAdmissionMaxStale` (30 s) only while the database is
unreachable. Only `GET` and `HEAD` use the cache. Every other method still
resolves its credential per request, so a revoked credential's writes stop at
once. A key the store no longer resolves is dropped and never served stale.
Signed-URL credentials are not cached either: they carry their own expiry and
issuer checks. A busy credential costs one resolution every 2 s instead of
one per request. The metric is `s3_gateway_credential_resolutions_total{source}`.

Consequence: revoking a credential, deleting its bucket or suspending its
owner stops that credential's reads within 2 s, or 30 s during a database
outage. Its writes stop at once.

