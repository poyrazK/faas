-- filename: 20260922174832118_org_activity_timeline.sql

-- +goose Up
-- +goose StatementBegin
-- Customer-facing, organization-wide infrastructure history. This is a
-- curated read model rather than a second security audit log: emitters copy
-- only safe labels and identifiers into it, never credentials or secret
-- values. Deliberately FK-free so deleting an app, deployment, account, or
-- project cannot erase the history that explains what happened.
create table if not exists org_activity (
    id bigint generated always as identity primary key,
    org_id uuid not null,
    occurred_at timestamptz not null default now(),
    kind text not null,
    actor_type text not null,
    actor_account_id uuid,
    actor_label text not null,
    resource_type text not null,
    resource_id text,
    resource_label text not null,
    app_id uuid,
    project_id uuid,
    deployment_id uuid,
    data jsonb not null default '{}'::jsonb,
    source_type text not null,
    source_id text not null,
    constraint org_activity_kind_chk
        check (kind ~ '^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)+$'),
    constraint org_activity_actor_type_chk
        check (actor_type in ('user', 'api_key', 'github', 'system', 'operator')),
    constraint org_activity_data_object_chk
        check (jsonb_typeof(data) = 'object'),
    constraint org_activity_source_unique
        unique (org_id, source_type, source_id)
);

create index if not exists org_activity_timeline_idx
    on org_activity (org_id, occurred_at desc, id desc);

create index if not exists org_activity_app_timeline_idx
    on org_activity (org_id, app_id, occurred_at desc, id desc)
    where app_id is not null;

comment on table org_activity is
    'Curated organization activity timeline; safe display facts only, FK-free and append-only';
comment on column org_activity.data is
    'Non-secret display metadata. Environment variable values and credentials are forbidden.';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
drop table if exists org_activity;
-- +goose StatementEnd
