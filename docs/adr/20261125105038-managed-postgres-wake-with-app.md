# ADR-20261125105038: Start a managed PostgreSQL database when its app wakes

Status: Accepted

Extends ADR-155 (managed PostgreSQL), ADR-20261112160000 (neond holds the
Neon key) and ADR-20261121120000 (live policy, customer suspend timeout).

## Context

A scale-to-zero app and its scale-to-zero Neon database go idle separately.
After both have suspended, the next request wakes the app (snapshot restore,
<350 ms p95 for the platform interval), and only the app's first query wakes
the database: Neon starts the compute on connection, which takes about
300–500 ms. The two cold starts run one after the other, so the first request
after idle pays both.

Starting the database's compute when the app starts waking overlaps the two.
The cost is that a wake that never touches the database (a health check, a
static page, a cron that does not query) still starts the compute, which then
bills at least one suspend timeout at the minimum size: about 0.008 CU-hours
at the 120 s default and 0.25 CU.

## Decision

Starting the database with its app is a per-database opt-in, off by default
(owner, 2026-11-25), so no customer's bill changes unless they choose it:

```sh
gregale postgres wake-with-app set orders-db on
gregale postgres wake-with-app set orders-db off
```

- **Storage.** `managed_postgres_wake_with_app` has one row per opted-in
  database; no row is off. The row also records when the database was last
  started for a wake. apid is the only writer; the row goes with the
  database. It is outside the database's spec, like the customer suspend
  timeout, so resize and compute-policy journals never see it.
- **API.** `PUT /v1/postgres/databases/{id}/wake-with-app` with
  `{"enabled": true|false}` (and the org route). Database views carry
  `wake_with_app`. Only a ready scale-to-zero database on a backend that can
  start a compute may turn it on (`409 wake_with_app_unsupported`
  otherwise); an always-on database is always awake.
- **Trigger.** schedd already announces every instance transition on
  `instance_changed` (`{"instance_id","app_id","state"}`), committed with the
  transition. Each apid listens; on a `waking`, `cold_booting` or `running`
  instance of an app, it claims, in one statement, each opted-in, ready,
  scale-to-zero database bound to the app (live binding) that it has not
  started in the last 60 seconds, and stamps it. Only the replica whose claim
  wins calls the provider, so replicas never repeat a start. schedd and the
  gateway are unchanged and never call the provider.
- **Provider.** An optional `ComputeStarter` provider interface. The Neon
  adapter reads the project's read-write endpoint and calls
  `POST /projects/{project}/endpoints/{endpoint}/start` through neond, which
  gains that route (KindWrite). The call is not paced as background work: a
  customer request is waiting on it. A held (disabled) or already running
  compute is left alone. The native Firecracker backend has no such call and
  does not offer the setting.
- **Failure.** A start is best effort: a failure, a refusal or a lost
  notification only means the database starts on the app's first query, as
  it does today. Starts are counted in
  `apid_managed_postgres_wake_starts_total{outcome}`.

The 60-second gap equals the shortest suspend timeout a customer can choose,
so a busy app sends Neon at most one start a minute per database. Neon's API
limit is shared with lifecycle calls; this bounds the extra load by the
number of opted-in databases, not by the request rate.

## Consequences

- Rollout order: neond (the route) before apid. An apid that calls an older
  neond gets the route refused, counts the start as failed and changes
  nothing else.
- An older apid ignores the table, so turning the setting on during a
  rollout only takes effect on the new apid.
- `app` wakes include ones no request asked for (a prewarm, a cron); an
  opted-in database starts on those too. The customer docs say so.
- The cost of an unneeded start shows in the database's usage like any other
  compute; nothing is billed separately.
