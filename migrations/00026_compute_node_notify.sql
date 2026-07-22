-- +goose Up
-- +goose StatementBegin

-- Issue #98 / ADR-028: pg_notify channel compute_node_changed. vmmd's
-- self-registration UpsertComputeNode and schedd's watchdog
-- SetComputeNodeActive both write to compute_nodes; gatewayd listens
-- on this channel to drop its per-node *grpc.ClientConn cache entry
-- when the row mutates (overlay IP rotated, node drained). The
-- payload is JSON {"node_id":"<uuid>","active":<bool>}; gatewayd
-- keys its eviction map by node_id so it can drop the cache entry
-- regardless of which writer triggered the notification.
--
-- Why a separate channel from app_changed / instance_changed:
-- gatewayd's existing channels deal in app/app-id semantics — the
-- routing cache and the per-app wake gate. compute_node_changed
-- deals in node-id semantics — the per-node vmmd client cache.
-- Splitting the channels lets gatewayd route the eviction to a
-- different in-memory map without coupling the two caches.

create or replace function compute_node_notify() returns trigger
language plpgsql as $$
declare
    payload jsonb;
begin
    payload := jsonb_build_object(
        'node_id', new.id::text,
        'active', new.active
    );
    perform pg_notify('compute_node_changed', payload::text);
    return new;
end;
$$;

drop trigger if exists compute_node_changed_trg on compute_nodes;
create trigger compute_node_changed_trg
    after insert or update on compute_nodes
    for each row execute function compute_node_notify();

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

drop trigger if exists compute_node_changed_trg on compute_nodes;
drop function if exists compute_node_notify();

-- +goose StatementEnd