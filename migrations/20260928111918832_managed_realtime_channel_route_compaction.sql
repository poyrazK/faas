-- filename: 20260928111918832_managed_realtime_channel_route_compaction.sql

-- Generation-fenced snapshots let apid prune stale node membership without
-- racing Subscribe, and let capped indexes rebuild safely before re-enabling.

-- +goose Up
alter table managed_realtime_channel_route_overflow
    add column if not exists rebuild_generation bigint not null default 0,
    add column if not exists rebuilding boolean not null default false,
    add column if not exists next_rebuild_at timestamptz not null default now(),
    add column if not exists rebuild_started_at timestamptz;

create table if not exists managed_realtime_channel_route_generation (
    singleton   boolean primary key default true check (singleton),
    generation  bigint not null default 0 check (generation >= 0)
);

insert into managed_realtime_channel_route_generation (singleton, generation)
values (true, 0)
on conflict (singleton) do nothing;

create table if not exists managed_realtime_channel_route_node_state (
    node_id             uuid primary key references compute_nodes(id) on delete cascade,
    snapshot_generation bigint not null default 0 check (snapshot_generation >= 0),
    updated_at           timestamptz not null default now()
);

create index if not exists managed_realtime_channel_routes_node_id_idx
    on managed_realtime_channel_routes (node_id, endpoint_id);

-- Older routing deployments may already have accumulated over-cap indexes
-- before compaction existed. Disable and clear them so the first snapshot
-- pass can rebuild them under the same generation fence as future overflow.
insert into managed_realtime_channel_route_overflow
	(endpoint_id, rebuild_generation, rebuilding, next_rebuild_at)
select routes.endpoint_id, 0, false, now()
  from managed_realtime_channel_routes routes
 group by routes.endpoint_id
having count(*) > 10000
on conflict (endpoint_id) do nothing;

delete from managed_realtime_channel_routes routes
 using managed_realtime_channel_route_overflow overflow
 where overflow.endpoint_id = routes.endpoint_id;

update managed_realtime_channel_route_generation
   set generation = generation + 1
 where singleton = true
   and exists (select 1 from managed_realtime_channel_route_overflow);

update managed_realtime_channel_route_overflow
   set rebuild_generation = (
       select generation from managed_realtime_channel_route_generation where singleton = true
   ),
       next_rebuild_at = now();

-- +goose Down
drop table if exists managed_realtime_channel_route_node_state;
drop table if exists managed_realtime_channel_route_generation;
drop index if exists managed_realtime_channel_routes_node_id_idx;
alter table managed_realtime_channel_route_overflow
    drop column if exists rebuild_started_at,
    drop column if exists next_rebuild_at,
    drop column if exists rebuilding,
    drop column if exists rebuild_generation;
