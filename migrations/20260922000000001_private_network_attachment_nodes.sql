-- +goose Up
-- +goose StatementBegin

-- Durable per-node health for private-network attachment convergence. The
-- node id is text rather than a foreign key because provider connectors and
-- rolling upgrades may report identities that are not present in the local
-- compute_nodes projection yet.
CREATE TABLE IF NOT EXISTS app_private_network_attachment_nodes (
  account_id    uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  app_id        uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
  network_id    text NOT NULL,
  node_id       text NOT NULL,
  fabric_status text NOT NULL DEFAULT '',
  fabric_detail text NOT NULL DEFAULT '',
  route_status  text NOT NULL DEFAULT '',
  route_detail  text NOT NULL DEFAULT '',
  observed_at   timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (app_id, node_id),
  CONSTRAINT app_private_network_attachment_nodes_status_chk CHECK (
    fabric_status IN ('', 'ready', 'error') AND
    route_status IN ('', 'ready', 'error')
  )
);

CREATE INDEX IF NOT EXISTS app_private_network_attachment_nodes_account_idx
  ON app_private_network_attachment_nodes (account_id, network_id, app_id, node_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS app_private_network_attachment_nodes_account_idx;
DROP TABLE IF EXISTS app_private_network_attachment_nodes;

-- +goose StatementEnd
