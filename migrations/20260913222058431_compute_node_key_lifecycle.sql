-- +goose Up
-- +goose StatementBegin

-- Capacity-report keys are credentials, not an append-only history. Keep one
-- current key and, during an explicit rotation, one previous key for a short
-- overlap. Older rollout-generated keys are removed so they cannot remain
-- trusted indefinitely.
ALTER TABLE compute_node_keys
    ADD COLUMN IF NOT EXISTS key_state text,
    ADD COLUMN IF NOT EXISTS valid_until timestamptz,
    ADD COLUMN IF NOT EXISTS revoked_at timestamptz;

WITH ranked AS (
    SELECT compute_node_id,
           key_id,
           row_number() OVER (
               PARTITION BY compute_node_id
               ORDER BY created_at DESC, key_id DESC
           ) AS position
      FROM compute_node_keys
)
DELETE FROM compute_node_keys k
 USING ranked r
 WHERE k.compute_node_id = r.compute_node_id
   AND k.key_id = r.key_id
   AND k.key_state IS NULL
   AND r.position > 2;

WITH ranked AS (
    SELECT compute_node_id,
           key_id,
           row_number() OVER (
               PARTITION BY compute_node_id
               ORDER BY created_at DESC, key_id DESC
           ) AS position
      FROM compute_node_keys
)
UPDATE compute_node_keys k
   SET key_state = CASE WHEN r.position = 1 THEN 'current' ELSE 'overlap' END,
       valid_until = CASE WHEN r.position = 1 THEN NULL ELSE now() + interval '24 hours' END,
       revoked_at = NULL
  FROM ranked r
 WHERE k.compute_node_id = r.compute_node_id
   AND k.key_id = r.key_id
   AND k.key_state IS NULL;

ALTER TABLE compute_node_keys
    ALTER COLUMN key_state SET DEFAULT 'current',
    ALTER COLUMN key_state SET NOT NULL;

ALTER TABLE compute_node_keys
    DROP CONSTRAINT IF EXISTS compute_node_keys_state_check;
ALTER TABLE compute_node_keys
    ADD CONSTRAINT compute_node_keys_state_check CHECK (
        (key_state = 'current' AND valid_until IS NULL AND revoked_at IS NULL)
        OR
        (key_state = 'overlap' AND valid_until IS NOT NULL AND revoked_at IS NULL)
        OR
        (key_state = 'revoked' AND revoked_at IS NOT NULL)
    );

CREATE UNIQUE INDEX IF NOT EXISTS compute_node_keys_one_current_idx
    ON compute_node_keys (compute_node_id)
    WHERE key_state = 'current';

CREATE INDEX IF NOT EXISTS compute_node_keys_usable_idx
    ON compute_node_keys (compute_node_id, key_id, valid_until)
    WHERE key_state IN ('current', 'overlap') AND revoked_at IS NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS compute_node_keys_usable_idx;
DROP INDEX IF EXISTS compute_node_keys_one_current_idx;
ALTER TABLE compute_node_keys
    DROP CONSTRAINT IF EXISTS compute_node_keys_state_check,
    DROP COLUMN IF EXISTS revoked_at,
    DROP COLUMN IF EXISTS valid_until,
    DROP COLUMN IF EXISTS key_state;
-- +goose StatementEnd
