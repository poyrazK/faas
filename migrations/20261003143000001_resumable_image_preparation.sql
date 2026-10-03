-- +goose Up
CREATE TABLE IF NOT EXISTS deployment_image_preparations (
    deployment_id uuid PRIMARY KEY REFERENCES deployments(id) ON DELETE CASCADE,
    node_name text NOT NULL,
    input_path text NOT NULL CHECK (input_path <> ''),
    input_key text NOT NULL,
    input_bytes bigint NOT NULL CHECK (input_bytes >= 0),
    claim_token uuid NOT NULL,
    phase text NOT NULL CHECK (phase IN ('preparing', 'layer_published', 'scan_complete', 'handed_off')),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS deployment_image_preparations_pending_idx
    ON deployment_image_preparations (updated_at, deployment_id)
    WHERE phase <> 'handed_off';

-- +goose Down
DROP TABLE IF EXISTS deployment_image_preparations;
