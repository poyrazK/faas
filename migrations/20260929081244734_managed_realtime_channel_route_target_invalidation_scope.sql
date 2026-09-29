-- Scope common route invalidations to the affected endpoint and channel.
-- Global target changes continue to use an empty JSON object payload.

-- +goose Up
-- +goose StatementBegin
create or replace function managed_realtime_channel_route_targets_notify()
returns trigger
language plpgsql as $$
begin
    if tg_table_name in ('managed_realtime_channel_routes', 'managed_realtime_channel_route_overflow_channels') then
        if tg_op = 'DELETE' then
            perform pg_notify(
                'managed_realtime_channel_route_targets_changed',
                json_build_object('endpoint_id', old.endpoint_id::text, 'channel', old.channel)::text
            );
        elsif tg_op = 'INSERT' then
            perform pg_notify(
                'managed_realtime_channel_route_targets_changed',
                json_build_object('endpoint_id', new.endpoint_id::text, 'channel', new.channel)::text
            );
        else
            perform pg_notify(
                'managed_realtime_channel_route_targets_changed',
                json_build_object('endpoint_id', old.endpoint_id::text, 'channel', old.channel)::text
            );
            if (old.endpoint_id, old.channel) is distinct from (new.endpoint_id, new.channel) then
                perform pg_notify(
                    'managed_realtime_channel_route_targets_changed',
                    json_build_object('endpoint_id', new.endpoint_id::text, 'channel', new.channel)::text
                );
            end if;
        end if;
    elsif tg_table_name = 'managed_realtime_channel_route_overflow' then
        if tg_op = 'DELETE' then
            perform pg_notify(
                'managed_realtime_channel_route_targets_changed',
                json_build_object('endpoint_id', old.endpoint_id::text)::text
            );
        elsif tg_op = 'INSERT' then
            perform pg_notify(
                'managed_realtime_channel_route_targets_changed',
                json_build_object('endpoint_id', new.endpoint_id::text)::text
            );
        else
            perform pg_notify(
                'managed_realtime_channel_route_targets_changed',
                json_build_object('endpoint_id', old.endpoint_id::text)::text
            );
            if old.endpoint_id is distinct from new.endpoint_id then
                perform pg_notify(
                    'managed_realtime_channel_route_targets_changed',
                    json_build_object('endpoint_id', new.endpoint_id::text)::text
                );
            end if;
        end if;
    else
        perform pg_notify('managed_realtime_channel_route_targets_changed', '{}');
    end if;
    return null;
end;
$$;
-- +goose StatementEnd

drop trigger if exists managed_realtime_channel_route_targets_routes_trg on managed_realtime_channel_routes;
create trigger managed_realtime_channel_route_targets_routes_trg
    after insert or update or delete on managed_realtime_channel_routes
    for each row execute function managed_realtime_channel_route_targets_notify();

drop trigger if exists managed_realtime_channel_route_targets_node_state_trg on managed_realtime_channel_route_node_state;
drop trigger if exists managed_realtime_channel_route_targets_node_state_write_trg on managed_realtime_channel_route_node_state;
drop trigger if exists managed_realtime_channel_route_targets_node_state_update_trg on managed_realtime_channel_route_node_state;
create trigger managed_realtime_channel_route_targets_node_state_write_trg
    after insert or delete on managed_realtime_channel_route_node_state
    for each row execute function managed_realtime_channel_route_targets_notify();
create trigger managed_realtime_channel_route_targets_node_state_update_trg
    after update on managed_realtime_channel_route_node_state
    for each row
    when (old.node_id is distinct from new.node_id
       or old.snapshot_generation is distinct from new.snapshot_generation)
    execute function managed_realtime_channel_route_targets_notify();

drop trigger if exists managed_realtime_channel_route_targets_generation_trg on managed_realtime_channel_route_generation;
drop trigger if exists managed_realtime_channel_route_targets_generation_write_trg on managed_realtime_channel_route_generation;
drop trigger if exists managed_realtime_channel_route_targets_generation_update_trg on managed_realtime_channel_route_generation;
create trigger managed_realtime_channel_route_targets_generation_write_trg
    after insert or delete on managed_realtime_channel_route_generation
    for each row execute function managed_realtime_channel_route_targets_notify();
create trigger managed_realtime_channel_route_targets_generation_update_trg
    after update on managed_realtime_channel_route_generation
    for each row
    when (old.generation is distinct from new.generation)
    execute function managed_realtime_channel_route_targets_notify();

drop trigger if exists managed_realtime_channel_route_targets_overflow_trg on managed_realtime_channel_route_overflow;
drop trigger if exists managed_realtime_channel_route_targets_overflow_write_trg on managed_realtime_channel_route_overflow;
drop trigger if exists managed_realtime_channel_route_targets_overflow_update_trg on managed_realtime_channel_route_overflow;
create trigger managed_realtime_channel_route_targets_overflow_write_trg
    after insert or delete on managed_realtime_channel_route_overflow
    for each row execute function managed_realtime_channel_route_targets_notify();
create trigger managed_realtime_channel_route_targets_overflow_update_trg
    after update on managed_realtime_channel_route_overflow
    for each row
    when (old.endpoint_id is distinct from new.endpoint_id)
    execute function managed_realtime_channel_route_targets_notify();

drop trigger if exists managed_realtime_channel_route_targets_overflow_channels_trg on managed_realtime_channel_route_overflow_channels;
drop trigger if exists managed_realtime_channel_route_targets_overflow_channels_write_trg on managed_realtime_channel_route_overflow_channels;
drop trigger if exists managed_realtime_channel_route_targets_overflow_channels_update_trg on managed_realtime_channel_route_overflow_channels;
create trigger managed_realtime_channel_route_targets_overflow_channels_write_trg
    after insert or delete on managed_realtime_channel_route_overflow_channels
    for each row execute function managed_realtime_channel_route_targets_notify();
create trigger managed_realtime_channel_route_targets_overflow_channels_update_trg
    after update on managed_realtime_channel_route_overflow_channels
    for each row
    when (old.endpoint_id is distinct from new.endpoint_id
       or old.channel is distinct from new.channel)
    execute function managed_realtime_channel_route_targets_notify();

drop trigger if exists managed_realtime_channel_route_targets_compute_nodes_write_trg on compute_nodes;
create trigger managed_realtime_channel_route_targets_compute_nodes_write_trg
    after insert or delete on compute_nodes
    for each row execute function managed_realtime_channel_route_targets_notify();

drop trigger if exists managed_realtime_channel_route_targets_compute_nodes_update_trg on compute_nodes;
create trigger managed_realtime_channel_route_targets_compute_nodes_update_trg
    after update on compute_nodes
    for each row
    when (old.active is distinct from new.active
       or old.name is distinct from new.name
       or old.gateway_target_url is distinct from new.gateway_target_url)
    execute function managed_realtime_channel_route_targets_notify();

-- +goose Down
drop trigger if exists managed_realtime_channel_route_targets_compute_nodes_update_trg on compute_nodes;
drop trigger if exists managed_realtime_channel_route_targets_compute_nodes_write_trg on compute_nodes;
drop trigger if exists managed_realtime_channel_route_targets_overflow_channels_update_trg on managed_realtime_channel_route_overflow_channels;
drop trigger if exists managed_realtime_channel_route_targets_overflow_channels_write_trg on managed_realtime_channel_route_overflow_channels;
drop trigger if exists managed_realtime_channel_route_targets_overflow_update_trg on managed_realtime_channel_route_overflow;
drop trigger if exists managed_realtime_channel_route_targets_overflow_write_trg on managed_realtime_channel_route_overflow;
drop trigger if exists managed_realtime_channel_route_targets_generation_update_trg on managed_realtime_channel_route_generation;
drop trigger if exists managed_realtime_channel_route_targets_generation_write_trg on managed_realtime_channel_route_generation;
drop trigger if exists managed_realtime_channel_route_targets_node_state_update_trg on managed_realtime_channel_route_node_state;
drop trigger if exists managed_realtime_channel_route_targets_node_state_write_trg on managed_realtime_channel_route_node_state;
drop trigger if exists managed_realtime_channel_route_targets_routes_trg on managed_realtime_channel_routes;

-- +goose StatementBegin
create or replace function managed_realtime_channel_route_targets_notify()
returns trigger
language plpgsql as $$
begin
    perform pg_notify('managed_realtime_channel_route_targets_changed', 'invalidate');
    return null;
end;
$$;
-- +goose StatementEnd

create trigger managed_realtime_channel_route_targets_routes_trg
    after insert or update or delete on managed_realtime_channel_routes
    for each statement execute function managed_realtime_channel_route_targets_notify();
create trigger managed_realtime_channel_route_targets_node_state_trg
    after insert or update or delete on managed_realtime_channel_route_node_state
    for each statement execute function managed_realtime_channel_route_targets_notify();
create trigger managed_realtime_channel_route_targets_generation_trg
    after insert or update or delete on managed_realtime_channel_route_generation
    for each statement execute function managed_realtime_channel_route_targets_notify();
create trigger managed_realtime_channel_route_targets_overflow_trg
    after insert or update or delete on managed_realtime_channel_route_overflow
    for each statement execute function managed_realtime_channel_route_targets_notify();
create trigger managed_realtime_channel_route_targets_overflow_channels_trg
    after insert or update or delete on managed_realtime_channel_route_overflow_channels
    for each statement execute function managed_realtime_channel_route_targets_notify();
create trigger managed_realtime_channel_route_targets_compute_nodes_write_trg
    after insert or delete on compute_nodes
    for each statement execute function managed_realtime_channel_route_targets_notify();
create trigger managed_realtime_channel_route_targets_compute_nodes_update_trg
    after update on compute_nodes
    for each row
    when (old.active is distinct from new.active
       or old.name is distinct from new.name
       or old.gateway_target_url is distinct from new.gateway_target_url)
    execute function managed_realtime_channel_route_targets_notify();
