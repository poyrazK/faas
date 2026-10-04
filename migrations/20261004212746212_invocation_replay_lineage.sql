-- filename: 20261004212746212_invocation_replay_lineage.sql

-- +goose Up
-- +goose StatementBegin
-- ADR-583: retain the root without a foreign key so intermediate/root
-- execution retention cannot sever the lineage of surviving replays.
ALTER TABLE invocations ADD COLUMN replay_root_invocation_id uuid;
ALTER TABLE invocations ADD COLUMN replay_root_created_at timestamptz;
ALTER TABLE invocations ADD CONSTRAINT invocations_replay_lineage_check CHECK (
  replay_root_invocation_id IS NULL OR (
    source='replay' AND replayed_from_invocation_id IS NOT NULL
    AND replayed_from_invocation_id<>id AND replay_root_invocation_id<>id
    AND replay_root_created_at IS NOT NULL
  )
);
CREATE INDEX invocations_replay_root_idx
  ON invocations (account_id, replay_root_invocation_id, created_at DESC, id DESC)
  WHERE replay_root_invocation_id IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX invocations_replay_root_idx;
ALTER TABLE invocations DROP CONSTRAINT invocations_replay_lineage_check;
ALTER TABLE invocations DROP COLUMN replay_root_invocation_id;
ALTER TABLE invocations DROP COLUMN replay_root_created_at;
-- +goose StatementEnd
