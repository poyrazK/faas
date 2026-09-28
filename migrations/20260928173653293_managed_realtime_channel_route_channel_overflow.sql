-- Route overflow is isolated to the channels whose rows cannot fit under
-- the endpoint-wide route-row budget. Existing endpoint markers came from
-- the broadcast-only behavior and must remain broad until a full rebuild.

-- +goose Up
alter table managed_realtime_channel_route_overflow
    add column if not exists overflow_all boolean not null default true;

create table if not exists managed_realtime_channel_route_overflow_channels (
    endpoint_id     uuid not null references managed_realtime_endpoints(id) on delete cascade,
    channel         text not null check (length(channel) between 1 and 256),
    next_rebuild_at timestamptz not null default now() + interval '5 minutes',
    primary key (endpoint_id, channel)
);

create index if not exists managed_realtime_channel_route_overflow_channels_retry_idx
    on managed_realtime_channel_route_overflow_channels (next_rebuild_at, endpoint_id);

-- +goose Down
insert into managed_realtime_channel_route_overflow
	(endpoint_id, rebuild_generation, rebuilding, next_rebuild_at, overflow_all)
select distinct endpoint_id, 0, false, now(), true
  from managed_realtime_channel_route_overflow_channels
on conflict (endpoint_id) do update set
	overflow_all = true,
	rebuilding = false,
	next_rebuild_at = now(),
	rebuild_started_at = null;

drop index if exists managed_realtime_channel_route_overflow_channels_retry_idx;
drop table if exists managed_realtime_channel_route_overflow_channels;
alter table managed_realtime_channel_route_overflow
    drop column if exists overflow_all;
