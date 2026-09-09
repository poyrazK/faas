-- +goose Up
-- Issue #756: account export walks these append-only histories with stable
-- (timestamp, id) keyset cursors. The id suffix both breaks timestamp ties and
-- lets PostgreSQL satisfy each page without a sort as histories grow.
CREATE INDEX IF NOT EXISTS deployments_app_created_id_idx
    ON deployments (app_id, created_at DESC, id DESC);

CREATE INDEX IF NOT EXISTS gdpr_requests_account_requested_id_idx
    ON gdpr_requests (account_id, requested_at DESC, id DESC);

CREATE INDEX IF NOT EXISTS events_subject_at_id_idx
    ON events (subject, at DESC, id DESC);

-- +goose Down
DROP INDEX IF EXISTS events_subject_at_id_idx;
DROP INDEX IF EXISTS gdpr_requests_account_requested_id_idx;
DROP INDEX IF EXISTS deployments_app_created_id_idx;
