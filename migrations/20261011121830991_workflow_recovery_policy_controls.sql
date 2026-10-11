-- +goose Up
CREATE TABLE workflow_recovery_policy_controls (
 id uuid PRIMARY KEY,
 app_id uuid NOT NULL REFERENCES workflow_recovery_policies(app_id) ON DELETE CASCADE,
 policy_version bigint NOT NULL CHECK (policy_version > 0),
 operation text NOT NULL CHECK (operation IN ('pause','resume')),
 reason text NOT NULL CHECK (length(reason) BETWEEN 1 AND 512),
 actor_kind text NOT NULL CHECK (actor_kind IN ('account','api_key','system','internal')),
 actor_id text NOT NULL CHECK (length(actor_id) > 0),
 controlled_at timestamptz NOT NULL
);
CREATE INDEX workflow_recovery_policy_controls_app_idx ON workflow_recovery_policy_controls(app_id,controlled_at);

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM workflow_recovery_policy_controls) THEN
  RAISE EXCEPTION 'cannot remove retained recovery policy control audit evidence';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE workflow_recovery_policy_controls;
