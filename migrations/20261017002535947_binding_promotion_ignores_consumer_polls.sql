-- filename: 20261017002535947_binding_promotion_ignores_consumer_polls.sql

-- Every queue consumer poll rewrites last_poll_at and last_success_at, about
-- three times a second per consumer, and both columns were watched facts:
-- production-us queue workers reached promotion revision 2.6 million, so a
-- bindings-gated promotion was invalidated by the next poll (hunt #10,
-- H10-111). Poll freshness is already enforced by the promotion fence's
-- ValidUntil (api.QueueConsumerMaxPollAge). A new consumer error still
-- bumps the revision; recovering from one only makes promotion more
-- permissive, so it need not.

-- +goose Up
-- +goose StatementBegin
DROP TRIGGER IF EXISTS binding_promotion_revision ON trigger_consumer_health;
CREATE TRIGGER binding_promotion_revision AFTER INSERT OR UPDATE OR DELETE ON trigger_consumer_health
 FOR EACH ROW EXECUTE FUNCTION capture_binding_promotion_revision('trigger_id,last_error_at');
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS binding_promotion_revision ON trigger_consumer_health;
CREATE TRIGGER binding_promotion_revision AFTER INSERT OR UPDATE OR DELETE ON trigger_consumer_health
 FOR EACH ROW EXECUTE FUNCTION capture_binding_promotion_revision('trigger_id,last_poll_at,last_success_at,last_error_at');
-- +goose StatementEnd
