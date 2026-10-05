-- +goose Up
-- +goose StatementBegin
-- ADR-598: one recovery child per failed keyed execution. Child retention
-- must not permit a duplicate POST to execute that parent again.
CREATE TABLE invocation_keyed_replays (
  parent_invocation_id uuid PRIMARY KEY REFERENCES invocations(id) ON DELETE CASCADE,
  replay_invocation_id uuid NOT NULL UNIQUE,
  CHECK (parent_invocation_id <> replay_invocation_id)
);
-- +goose StatementEnd

-- +goose Down
DROP TABLE invocation_keyed_replays;
