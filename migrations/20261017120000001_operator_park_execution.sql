-- +goose Up
-- Dispatch must be quiesced while replacing legacy scheduler writers.
-- +goose StatementBegin
DO $replay$
BEGIN
 IF to_regclass('operator_park_executions') IS NULL THEN
UPDATE operator_intents SET status = 'outcome_unknown', finished_at = now(),
 error = 'Legacy park execution requires operator reconciliation before another action.'
WHERE kind = 'force_park' AND status = 'running';
 END IF;
END
$replay$;
-- +goose StatementEnd

CREATE TABLE IF NOT EXISTS operator_park_executions (
 intent_id uuid PRIMARY KEY REFERENCES operator_intents(id) ON DELETE RESTRICT,
 target jsonb NOT NULL CHECK (jsonb_typeof(target) = 'object'),
 phase text NOT NULL CHECK (phase IN ('prepared','started','acknowledged','outcome_unknown','completed','failed','cancelled')),
 effect_token uuid CHECK (effect_token IS NULL OR effect_token <> '00000000-0000-0000-0000-000000000000'),
 receipt jsonb CHECK (receipt IS NULL OR jsonb_typeof(receipt) = 'object'),
 stopped_at timestamptz,
 terminal_state text CHECK (terminal_state IS NULL OR terminal_state IN ('parked','stopped')),
 updated_at timestamptz NOT NULL DEFAULT now(),
 CHECK ((receipt IS NULL) = (stopped_at IS NULL)),
 CHECK (receipt IS NULL OR effect_token IS NOT NULL),
 CHECK (phase NOT IN ('acknowledged','completed') OR receipt IS NOT NULL),
 CHECK (phase <> 'started' OR effect_token IS NOT NULL),
 CHECK ((phase = 'completed') = (terminal_state IS NOT NULL))
);
CREATE INDEX IF NOT EXISTS operator_intents_unknown_park_idx ON operator_intents (requested_at,id)
 WHERE status = 'outcome_unknown' AND kind = 'force_park';

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM operator_park_executions)
  OR EXISTS (SELECT 1 FROM operator_intents WHERE kind='force_park' AND status='outcome_unknown') THEN
  RAISE EXCEPTION 'Reconcile and preserve operator park evidence before downgrade';
 END IF;
END $$;
-- +goose StatementEnd
DROP INDEX operator_intents_unknown_park_idx;
DROP TABLE operator_park_executions;
