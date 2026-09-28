-- Persist each node's realtime process and route revision with its snapshot so
-- successive apid replicas can share the disconnect refresh cursor.

-- +goose Up
alter table managed_realtime_channel_route_node_state
    add column if not exists realtime_process_instance_id text,
    add column if not exists realtime_route_revision text;

-- +goose Down
alter table managed_realtime_channel_route_node_state
    drop column if exists realtime_route_revision,
    drop column if exists realtime_process_instance_id;
