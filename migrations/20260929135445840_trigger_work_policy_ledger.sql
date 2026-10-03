-- filename: 20260929135445840_trigger_work_policy_ledger.sql

-- +goose Up
-- +goose StatementBegin
DO $$ BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint
        WHERE conname = 'triggers_id_app_uniq' AND conrelid = 'triggers'::regclass) THEN
        ALTER TABLE triggers ADD CONSTRAINT triggers_id_app_uniq UNIQUE (id, app_id);
    END IF;
END $$;

CREATE TABLE IF NOT EXISTS trigger_work_bindings (
    trigger_id uuid PRIMARY KEY,
    app_id uuid NOT NULL,
    policy_name text NOT NULL,
    key_selector text NOT NULL CHECK (length(key_selector) BETWEEN 1 AND 256),
    fairness_key_selector text NOT NULL DEFAULT ''
        CHECK (length(fairness_key_selector) <= 256),
    FOREIGN KEY (trigger_id, app_id) REFERENCES triggers(id, app_id) ON DELETE CASCADE,
    FOREIGN KEY (app_id, policy_name) REFERENCES app_work_policies(app_id, name)
);

ALTER TABLE trigger_records
    ADD COLUMN IF NOT EXISTS work_policy_name text,
    ADD COLUMN IF NOT EXISTS work_key_digest bytea,
    ADD COLUMN IF NOT EXISTS work_sequence bigint,
    ADD COLUMN IF NOT EXISTS work_policy_revision bigint,
    ADD COLUMN IF NOT EXISTS work_fairness_digest bytea,
    ADD COLUMN IF NOT EXISTS work_fairness_limit integer,
    ADD COLUMN IF NOT EXISTS work_expires_at timestamptz;
ALTER TABLE trigger_records DROP CONSTRAINT IF EXISTS trigger_records_work_lane_check;
ALTER TABLE trigger_records ADD CONSTRAINT trigger_records_work_lane_check CHECK (
    (work_policy_name IS NULL AND work_key_digest IS NULL
     AND work_sequence IS NULL AND work_policy_revision IS NULL
     AND work_fairness_digest IS NULL AND work_fairness_limit IS NULL
     AND work_expires_at IS NULL)
    OR
    (work_policy_name IS NOT NULL
     AND work_policy_name ~ '^[a-z][a-z0-9-]{0,62}$'
     AND work_key_digest IS NOT NULL AND length(work_key_digest) = 32
     AND work_sequence IS NOT NULL AND work_sequence > 0
     AND work_policy_revision IS NOT NULL AND work_policy_revision > 0
     AND ((work_fairness_digest IS NULL AND work_fairness_limit IS NULL)
       OR (work_fairness_digest IS NOT NULL AND length(work_fairness_digest) = 32
         AND work_fairness_limit IS NOT NULL
         AND work_fairness_limit BETWEEN 1 AND 1000)))
);

CREATE INDEX IF NOT EXISTS trigger_records_work_lane_active_idx
    ON trigger_records (work_policy_name, work_key_digest, work_sequence)
    WHERE work_policy_name IS NOT NULL
      AND state IN ('pending', 'retry', 'claimed');
CREATE INDEX IF NOT EXISTS trigger_records_work_fairness_active_idx
    ON trigger_records (work_policy_name, work_fairness_digest)
    WHERE work_fairness_digest IS NOT NULL AND state = 'claimed';
CREATE INDEX IF NOT EXISTS trigger_records_work_expiring_idx
    ON trigger_records (work_expires_at)
    WHERE work_expires_at IS NOT NULL AND state IN ('pending', 'retry');
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS trigger_records_work_expiring_idx;
DROP INDEX IF EXISTS trigger_records_work_fairness_active_idx;
DROP INDEX IF EXISTS trigger_records_work_lane_active_idx;
ALTER TABLE trigger_records DROP CONSTRAINT IF EXISTS trigger_records_work_lane_check;
ALTER TABLE trigger_records
    DROP COLUMN IF EXISTS work_expires_at,
    DROP COLUMN IF EXISTS work_fairness_limit,
    DROP COLUMN IF EXISTS work_fairness_digest,
    DROP COLUMN IF EXISTS work_policy_revision,
    DROP COLUMN IF EXISTS work_sequence,
    DROP COLUMN IF EXISTS work_key_digest,
    DROP COLUMN IF EXISTS work_policy_name;
DROP TABLE IF EXISTS trigger_work_bindings;
ALTER TABLE triggers DROP CONSTRAINT IF EXISTS triggers_id_app_uniq;
-- +goose StatementEnd
