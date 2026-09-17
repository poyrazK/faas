-- +goose Up
-- +goose StatementBegin

-- Safe, bounded edit-to-live receipts for the remote developer loop. The
-- deployment key makes CLI retries idempotent and the app FK gives the
-- account-scoped app lifecycle ownership of its history.
CREATE TABLE IF NOT EXISTS developer_sync_history (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    deployment_id uuid NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,
    status text NOT NULL CHECK (status IN ('live', 'failed')),
    edit_to_live_ms bigint NOT NULL CHECK (edit_to_live_ms >= 0 AND edit_to_live_ms <= 3600000),
    slo_target_ms bigint NOT NULL CHECK (slo_target_ms > 0 AND slo_target_ms <= 3600000),
    within_slo boolean NOT NULL,
    phases jsonb NOT NULL DEFAULT '[]'::jsonb
        CHECK (jsonb_typeof(phases) = 'array' AND octet_length(phases::text) <= 8192),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (app_id, deployment_id)
);

CREATE INDEX IF NOT EXISTS developer_sync_history_app_created_idx
    ON developer_sync_history (app_id, created_at DESC, id DESC);

-- +goose StatementEnd

-- +goose Down
-- Forward-only: removing edit-to-live history would erase customer-facing DX
-- evidence. Retain the table and let a future retention migration own cleanup.
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd
