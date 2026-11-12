# ADR-20261102091500: App health assesses worker pools

- Status: proposed
- Date: 2026-11-02
- Related: ADR-734 (app health), ADR-20261023115741 (platform flags),
  `docs/app-health.md`, `docs/workers.md`

## Context

`GET /v1/apps/{slug}/health` assesses request and service workloads. For a
worker pool it answers `unknown`/`unsupported`: "HTTP serving health does not
assess worker or job execution." `docs/app-health.md` lists worker checks as a
future slice. A worker's failures are the quiet kind: replicas below the pool
minimum, a backlog that stops draining, messages that land in the dead-letter
queue. `gregale worker status` shows the raw numbers but nothing judges them,
so health views, the dashboard and agents cannot tell a stuck worker from an
idle one.

The evidence already exists and is cheap to read: the app's instances (as for
services) and the records of the triggers that feed the worker
(`trigger_records`, with `state`, `received_at` and `last_dispatched_at`).

## Decision

For an app whose `execution_mode` is `worker`, app health assesses three
things and no HTTP readiness or request rate.

1. **Releases**: the same `deployment` and `latest_deployment` checks as every
   app.
2. **Replicas** (`replicas`): running instances of the live releases against
   the pool minimum (`worker_replicas.min`, 0 when unset).
   - At or above a non-zero minimum with at least one running: pass, phase
     `serving`.
   - None running, none required, none starting: pass, phase `idle`; the pool
     scales up when work arrives.
   - Some starting: warning, phase `starting`.
   - None running against a minimum: fail, reason `capacity_below_target`.
   - Fewer than the minimum: warning, reason `capacity_below_target`.
3. **Queue** (`backlog`, `dead_letters`): the newest
   `api.AppHealthWorkerRecordScan` (1000) records of each attached trigger.
   - A pending or retrying message that has waited
     `api.AppHealthWorkerBacklogAgeWarn` (10 minutes): warning.
   - A message dead-lettered within `api.AppHealthWorkerDeadLetterWindow`
     (an hour, by `last_dispatched_at`): warning, pointing at `gregale dlq`.
     Older dead letters are mentioned and pass, so one bad night does not
     degrade a worker forever.
   - A scan that reads the full 1000 records reports its counts as lower
     bounds ("at least").
   - No attached trigger: not applicable; Gregale cannot see that backlog.
   - A failed read: unknown, never an empty queue.

The phases reuse the existing enum, so the API response shape does not change.
The check codes are new values of the OpenAPI `AppHealthCheck.code` enum; they
were added to it in ADR-20261102124306 amendment 1, before either flag was
released.

Jobs stay `unsupported`.

### Rollout

The `app_health_workers` platform flag (default off) releases it per account,
plan or organization. Off, a worker reads `unsupported` exactly as before, so
the change is invisible until it is released. Customer docs
(`docs/app-health.md`) change when the flag is released to every account.

## Consequences

- A stuck or crash-looping worker pool shows `unhealthy` or `degraded` in the
  same place as a failing service, with the number that explains it.
- Reads are bounded: one trigger list and at most 1000 records per trigger,
  inside the existing 10 s collection budget.
- The background history collector and health-change notifications still skip
  workers; including them is step 2, after production shows the verdicts are
  right.

## Steps

1. This ADR, the assessment and the flag (one PR).
2. Release the flag to the owner's account, check the verdicts on production
   workers, then include workers in recorded history and notifications.
3. Customer docs, and release to every account.

## Alternatives considered

- **Judge the dead-letter total.** A worker would stay degraded until someone
  purged old dead letters; the window keeps the signal current.
- **A new `processing` phase.** Clearer wording, but it changes the OpenAPI
  enum and every client that switches on phase; `serving` already means "doing
  its work" for services.
