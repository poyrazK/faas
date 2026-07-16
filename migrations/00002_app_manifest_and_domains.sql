-- +goose Up
-- +goose StatementBegin

-- M5 follow-up (spec §4.2 / Appendix A): adds the schema pieces apid needs
-- for source-tarball deploys, the runner-scaffold function rewrites, and
-- custom-domain TXT verification.

-- Distinguish deploy inputs (spec §9). Stored on the deployment row so
-- imaged/builderd can branch off it without re-reading the request body.
alter table deployments
  add column kind text not null default 'image'
    check (kind in ('image', 'tarball', 'dockerfile')),
  add column source_path text,
  add column source_bytes bigint,
  add column handler text,
  add column log_path text;

-- The app-level manifest (pkg/api.AppManifest) — runner scaffold, env vars,
-- healthz path — is what the guest-init consumes. Stored as jsonb for forward
-- compatibility; validated against the manifest contract at deploy time.
alter table apps
  add column manifest jsonb not null default '{}'::jsonb;

-- Custom-domain TXT-challenge token. apid generates a random hex string,
-- the customer publishes it at _faas-verify.<domain>, apid polls and sets
-- verified_at when it matches. We keep the expected value so we can re-poll.
alter table custom_domains
  add column challenge_token text not null default '',
  add column app_id_redirect uuid references apps(id);

-- Index for the verifier goroutine's "list unverified domains" query.
create index custom_domains_unverified_idx
  on custom_domains (domain) where verified_at is null;

-- crons — exact spec shape from §5 / Appendix A line 515.
create table if not exists crons (
  id uuid primary key default gen_random_uuid(),
  app_id uuid not null references apps(id),
  schedule text not null,
  path text not null default '/',
  enabled bool not null default true,
  created_at timestamptz not null default now()
);
create index crons_app_idx on crons (app_id) where enabled;

-- The events audit log (spec §6.1). Append-only; schedd writes state
-- transitions, apid writes customer-intent transitions.
create table if not exists events (
  id bigint generated always as identity primary key,
  at timestamptz not null default now(),
  actor text not null,
  kind text not null,
  subject uuid,
  data jsonb
);
create index events_subject_idx on events (subject, at desc);

-- usage_minutes lives in 00001_init.sql. Add a per-month aggregation view
-- so GET /v1/usage can answer directly from the spec §10 contract.
create or replace view usage_monthly as
  select
    account_id,
    app_id,
    date_trunc('month', minute) as month,
    sum(mb_seconds) as mb_seconds,
    sum(requests) as requests
  from usage_minutes
  group by account_id, app_id, date_trunc('month', minute);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
drop view if exists usage_monthly;
drop table if exists crons;
drop index if exists custom_domains_unverified_idx;
alter table custom_domains
  drop column if exists app_id_redirect,
  drop column if exists challenge_token;
alter table apps drop column if exists manifest;
alter table deployments
  drop column if exists log_path,
  drop column if exists handler,
  drop column if exists source_bytes,
  drop column if exists source_path,
  drop column if exists kind;
drop table if exists events;
-- +goose StatementEnd
