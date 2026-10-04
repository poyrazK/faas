-- filename: 20261004141640931_compute_node_service_address_ready.sql
--
-- ADR-530: when this node's vmmd began creating namespaces that admit guest
-- TCP to private service addresses. Service DNS hands a service address only
-- to an instance that started at or after this instant on its node; older
-- instances keep the tenant-bridge answer. vmmd keeps the earliest stamp
-- across restarts while the switch stays on, and clears it when the switch
-- is turned off. NULL means the node is not service-address capable.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE compute_nodes ADD COLUMN IF NOT EXISTS service_address_ready_at timestamptz;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE compute_nodes DROP COLUMN IF EXISTS service_address_ready_at;
-- +goose StatementEnd
