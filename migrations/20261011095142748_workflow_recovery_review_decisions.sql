-- +goose Up
-- +goose StatementBegin
-- The complete feature marker survives later migrations. Reconciliation must
-- preserve retained evidence and must not restore an earlier, narrower constraint.
DO $replay_guard$
BEGIN
 IF EXISTS (SELECT 1 FROM pg_attribute WHERE attrelid='workflow_recovery_policy_reviews'::regclass AND attname='review_decision' AND NOT attisdropped) THEN
  RETURN;
 END IF;
ALTER TABLE workflow_recovery_policy_reviews ADD COLUMN review_decision jsonb CHECK (
 review_decision IS NULL OR (jsonb_typeof(review_decision)='object' AND
 review_decision->>'decision' IN ('approved','dismissed') AND
 review_decision ?& ARRAY['reason','decided_at','actor_kind','actor_id'])
);
END
$replay_guard$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM workflow_recovery_policy_reviews WHERE review_decision IS NOT NULL) THEN
 RAISE EXCEPTION 'cannot downgrade while review decisions are retained'; END IF;
END $$;
ALTER TABLE workflow_recovery_policy_reviews DROP COLUMN review_decision;
-- +goose StatementEnd
