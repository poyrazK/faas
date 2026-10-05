-- +goose Up
CREATE TABLE alert_rollback_actions (
 fire_id uuid PRIMARY KEY REFERENCES alert_deliveries(id) ON DELETE CASCADE,
 app_id uuid REFERENCES apps(id) ON DELETE CASCADE,
 status text NOT NULL CHECK(status IN ('pending','blocked','complete','failed')),
 receipt jsonb NOT NULL CHECK(jsonb_typeof(receipt)='object' AND octet_length(receipt::text)<=16384),
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 CHECK(receipt ?& ARRAY['id','rule_id','account_id','app_id','status','fired_at','updated_at']
 AND receipt->>'id'=fire_id::text AND receipt->>'status'=status
 AND coalesce(nullif(receipt->>'app_id','')::uuid,'00000000-0000-0000-0000-000000000000'::uuid)=coalesce(app_id,'00000000-0000-0000-0000-000000000000'::uuid))
);
CREATE INDEX alert_rollback_pending ON alert_rollback_actions(updated_at,fire_id) WHERE status IN ('pending','blocked');
CREATE INDEX alert_rollback_app ON alert_rollback_actions(app_id,updated_at DESC,fire_id);

-- +goose Down
DROP TABLE alert_rollback_actions;
