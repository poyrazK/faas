-- filename: 20261005115846571_plain_invocation_replays.sql

-- +goose Up
-- +goose StatementBegin
-- ADR-601: atomic parent-based deduplication for unkeyed handler replay.
-- No child foreign key: child retention cannot authorize another execution.
CREATE TABLE IF NOT EXISTS invocation_plain_replays (
  parent_invocation_id uuid PRIMARY KEY REFERENCES invocations(id) ON DELETE CASCADE,
  replay_invocation_id uuid NOT NULL UNIQUE,
  replay_created_at timestamptz NOT NULL,
  CHECK (parent_invocation_id <> replay_invocation_id)
);

-- Adopt the latest retained direct child from the trusted ADR-597 ledger.
-- Historical forks remain visible; guest headers and unlinked legacy tenant
-- replays cannot establish a durable recovery identity.
INSERT INTO invocation_plain_replays (parent_invocation_id, replay_invocation_id, replay_created_at)
SELECT DISTINCT ON (p.id) p.id, c.id, c.created_at
FROM invocations p JOIN invocations c ON c.replayed_from_invocation_id=p.id
WHERE p.work_policy_name IS NULL AND p.queue_binding_id IS NULL AND p.queue_name=''
  AND c.work_policy_name IS NULL AND c.queue_binding_id IS NULL AND c.queue_name=''
  AND c.source='replay' AND c.account_id=p.account_id AND c.app_id=p.app_id
  AND c.deployment_scope=p.deployment_scope
  AND c.platform_tenant_id IS NOT DISTINCT FROM p.platform_tenant_id
  AND c.replay_root_invocation_id=coalesce(p.replay_root_invocation_id,p.id)
  AND c.replay_root_created_at=coalesce(p.replay_root_created_at,p.created_at)
  AND c.created_at>=p.created_at AND c.id<>p.id
ORDER BY p.id, c.created_at DESC, c.id DESC
ON CONFLICT DO NOTHING;
-- +goose StatementEnd

-- +goose Down
DROP TABLE IF EXISTS invocation_plain_replays;
