-- filename: 20261103093241320_issue_attribution_review_fence.sql
-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
-- Older apid writers know nothing about attribution_version. If one changes
-- verified identities during a rolling upgrade, schedule another review.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION mark_issue_attribution_for_review() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.attribution_version = OLD.attribution_version AND
    (NEW.verified_consumer_id, NEW.verified_platform_tenant_id)
      IS DISTINCT FROM (OLD.verified_consumer_id, OLD.verified_platform_tenant_id) THEN
  NEW.attribution_version := 1;
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
DROP TRIGGER IF EXISTS issue_attribution_review_fence ON issue_events;
CREATE TRIGGER issue_attribution_review_fence BEFORE UPDATE OF verified_consumer_id, verified_platform_tenant_id ON issue_events
 FOR EACH ROW EXECUTE FUNCTION mark_issue_attribution_for_review();
-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TRIGGER IF EXISTS issue_attribution_review_fence ON issue_events;
DROP FUNCTION IF EXISTS mark_issue_attribution_for_review();
