-- +goose Up
-- +goose StatementBegin

-- One table for every event-shaped surface introduced in Move 1
-- (roadmap: HTTP-only → event-driven FaaS, packaging change only):
--   'async_invoke'    POST /v1/apps/{slug}/invoke/async     202 + id
--   'queue'           POST .../queues/invocations:send      per-app FIFO
--   'delayed_task'    POST /v1/apps/{slug}/delayed-tasks    {payload, scheduled_at}
--   'cron'            emitted by schedd's cron loop (was wake-only pre-Move 1)
--
-- Sharing one table (instead of one table per surface) keeps ONE drain tick
-- draining ALL event sources in O(rows_due), ONE pg_notify channel
-- ('invocation_due') per "ready to dispatch", and ONE partial index for the
-- hot scan. A `source` discriminator splits them; payloads stay opaque
-- (jsonb) so the runner envelope carries them unchanged.
--
-- state values for the lifecycle:
--   pending    — initial; cap checks happen here; drain considers eligible
--   dispatching— claimed by the drain; holds a lease_expires_at
--   completed  — drain finished successfully; terminal
--   failed     — drain hit a permanent error; terminal
--   cancelled  — customer DELETEd a pending delayed_task; terminal

create table invocations (
  id              uuid primary key default gen_random_uuid(),
  app_id          uuid not null references apps(id),
  account_id      uuid not null references accounts(id),
  source          text not null
                    check (source in ('async_invoke','queue','delayed_task','cron')),
  state           text not null default 'pending'
                    check (state in ('pending','dispatching','completed','failed','cancelled')),
  -- payload is the request body the runner sees. jsonb so the platform does
  -- not have to know its shape (function source code parses it).
  payload         jsonb not null default '{}',
  -- headers propagate into the synthetic HTTP request (x-faas-invocation-id
  -- plus any user-supplied trip headers).
  headers         jsonb not null default '{}',
  -- Due ordering: for async_invoke / cron / queue 'due_at' is creation time
  -- (FIFO within source); for delayed_task it is the customer-supplied
  -- scheduled_at, capped to now()+MaxDelayedWindowDays. The drain tick
  -- scans WHERE state='pending' AND due_at <= now().
  due_at          timestamptz not null default now(),
  -- Synthetic HTTP envelope the runner sees. Both default to a request
  -- shape; cron writes these explicitly.
  method          text not null default 'POST',
  path            text not null default '/',
  -- Per-source bookkeeping — NULL except where the source requires it.
  cron_id         uuid references crons(id),                 -- source='cron' only
  scheduled_at    timestamptz,                               -- source='delayed_task' only
  -- Customer-supplied callback for delayed_task completion (best-effort).
  ack_url         text,
  -- terminal payload / error captured after dispatch. never read on the hot path.
  result          jsonb,
  -- Long-poll receive lease (queue :receive). When claimed, holds for 30s.
  lease_expires_at timestamptz,
  received_at     timestamptz,
  completed_at    timestamptz,
  -- The instance handle the drain handed the invocation to (NULL on
  -- insert; stamped at ClaimInvocation when state→dispatching). pkg/meter
  -- joins invocations against usage_minutes on this column to count
  -- requests per (instance, minute) for billing.
  instance_id     text,
  -- Per-dispatch attempt counter. Bounded by the drain tick's retry policy.
  attempts        int  not null default 0,
  last_error      text,
  created_at      timestamptz not null default now()
);

-- Hot drain scan: WHERE state='pending' AND due_at <= now() ORDER BY due_at
-- LIMIT 64 — index-backed, no heap visit on tick. Partial index omits
-- terminal rows so the index stays small as the ledger grows.
create index if not exists invocations_due_idx
  on invocations (due_at)
  where state = 'pending';

-- Per-app depth scan. apid's POST .../queues/invocations:send cap-check
-- and the drain's MaxDelayedTasksPerApp re-check both run:
--   select count(*) where app_id=$1 and state='pending' and source=$2
-- to fire against this index.
create index if not exists invocations_app_pending_idx
  on invocations (app_id, source, state)
  where state in ('pending','dispatching');

-- Dashboard "next due" queries for delayed-task views.
create index if not exists invocations_delayed_idx
  on invocations (app_id, scheduled_at)
  where source = 'delayed_task';

-- Per-instance join: pkg/meter.SampleAndRoll reads
--   select count(*) from invocations where instance_id=$1
--                                          and due_at within minute
--                                          and state='dispatching'
-- to set usage_minutes.requests on each rolling minute. Partial index keeps
-- it cheap; instance_id is NULL on the inbound INSERT path and is stamped
-- by the drain's claim step (state→dispatching).
create index if not exists invocations_instance_idx
  on invocations (instance_id, due_at)
  where state = 'dispatching';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
drop index if exists invocations_instance_idx;
drop index if exists invocations_delayed_idx;
drop index if exists invocations_app_pending_idx;
drop index if exists invocations_due_idx;
drop table if exists invocations cascade;
-- +goose StatementEnd
