-- filename: 20260922133000001_events_sidecar_health_idx.sql
-- +goose Up
-- +goose StatementBegin

-- Sidecar health transitions are queried through ListEventsBySidecar along
-- with init-exit and restart events. Keep the existing two-kind partial index
-- unchanged for replay safety, and add a focused companion index for the new
-- lifecycle kind so the closed-kind query remains indexable.
CREATE INDEX IF NOT EXISTS events_sidecar_health_name_idx
    ON events ((data->>'sidecar_name'))
    WHERE kind = 'wake.sidecar_health';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS public.events_sidecar_health_name_idx;
-- +goose StatementEnd
