-- +goose Up
-- Account-scoped legacy replay lineage must not scan the invocation table for
-- each selected event root. Modern lineage already has replay_root_idx.
CREATE INDEX invocations_replay_parent_lookup_idx ON invocations
(account_id,replayed_from_invocation_id,created_at DESC,id DESC)
WHERE replayed_from_invocation_id IS NOT NULL;

-- +goose Down
DROP INDEX invocations_replay_parent_lookup_idx;
