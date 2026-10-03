-- Publish target cache entries are process-local. Notify apid after commits
-- that can change target membership, readiness, overflow, or node metadata.

-- +goose Up
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

drop trigger if exists managed_realtime_channel_route_targets_routes_trg on managed_realtime_channel_routes;
create trigger managed_realtime_channel_route_targets_routes_trg
    after insert or update or delete on managed_realtime_channel_routes
    for each statement execute function managed_realtime_channel_route_targets_notify();

drop trigger if exists managed_realtime_channel_route_targets_node_state_trg on managed_realtime_channel_route_node_state;
create trigger managed_realtime_channel_route_targets_node_state_trg
    after insert or update or delete on managed_realtime_channel_route_node_state
    for each statement execute function managed_realtime_channel_route_targets_notify();

drop trigger if exists managed_realtime_channel_route_targets_generation_trg on managed_realtime_channel_route_generation;
create trigger managed_realtime_channel_route_targets_generation_trg
    after insert or update or delete on managed_realtime_channel_route_generation
    for each statement execute function managed_realtime_channel_route_targets_notify();

drop trigger if exists managed_realtime_channel_route_targets_overflow_trg on managed_realtime_channel_route_overflow;
create trigger managed_realtime_channel_route_targets_overflow_trg
    after insert or update or delete on managed_realtime_channel_route_overflow
    for each statement execute function managed_realtime_channel_route_targets_notify();

drop trigger if exists managed_realtime_channel_route_targets_overflow_channels_trg on managed_realtime_channel_route_overflow_channels;
create trigger managed_realtime_channel_route_targets_overflow_channels_trg
    after insert or update or delete on managed_realtime_channel_route_overflow_channels
    for each statement execute function managed_realtime_channel_route_targets_notify();

drop trigger if exists managed_realtime_channel_route_targets_compute_nodes_write_trg on compute_nodes;
create trigger managed_realtime_channel_route_targets_compute_nodes_write_trg
    after insert or delete on compute_nodes
    for each statement execute function managed_realtime_channel_route_targets_notify();

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
drop trigger if exists managed_realtime_channel_route_targets_overflow_channels_trg on managed_realtime_channel_route_overflow_channels;
drop trigger if exists managed_realtime_channel_route_targets_overflow_trg on managed_realtime_channel_route_overflow;
drop trigger if exists managed_realtime_channel_route_targets_generation_trg on managed_realtime_channel_route_generation;
drop trigger if exists managed_realtime_channel_route_targets_node_state_trg on managed_realtime_channel_route_node_state;
drop trigger if exists managed_realtime_channel_route_targets_routes_trg on managed_realtime_channel_routes;

-- +goose StatementBegin
drop function if exists managed_realtime_channel_route_targets_notify();
-- +goose StatementEnd
