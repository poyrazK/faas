-- +goose Up
ALTER TABLE workflow_recovery_policy_reviews ADD COLUMN review_decision jsonb CHECK (
 review_decision IS NULL OR (jsonb_typeof(review_decision)='object' AND
 review_decision->>'decision' IN ('approved','dismissed') AND
 review_decision ?& ARRAY['reason','decided_at','actor_kind','actor_id'])
);
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM workflow_recovery_policy_reviews WHERE review_decision IS NOT NULL) THEN
 RAISE EXCEPTION 'cannot downgrade while review decisions are retained'; END IF;
END $$;
ALTER TABLE workflow_recovery_policy_reviews DROP COLUMN review_decision;
-- +goose StatementEnd
