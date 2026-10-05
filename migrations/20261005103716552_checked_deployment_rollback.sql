-- +goose Up
-- +goose StatementBegin
CREATE TABLE deployment_rollback_operations (
 id uuid PRIMARY KEY,
 app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
 scope text NOT NULL CHECK(scope ~ '^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$'),
 target_deployment_id uuid NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,
 current_deployment_id uuid NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,
 status text NOT NULL CHECK(status IN ('preparing','ready','blocked','routing','complete','failed')),
 receipt jsonb NOT NULL CHECK(jsonb_typeof(receipt)='object' AND octet_length(receipt::text)<=16384),
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 CHECK(target_deployment_id<>current_deployment_id),
 CHECK(receipt ?& ARRAY['id','app_id','scope','target_deployment_id','current_deployment_id','status'] AND receipt->>'id'=id::text AND receipt->>'app_id'=app_id::text AND receipt->>'scope'=scope
   AND receipt->>'target_deployment_id'=target_deployment_id::text
   AND receipt->>'current_deployment_id'=current_deployment_id::text AND receipt->>'status'=status)
);
CREATE UNIQUE INDEX deployment_rollback_active_scope ON deployment_rollback_operations(app_id,scope)
 WHERE status NOT IN ('complete','failed');
CREATE INDEX deployment_rollback_pending ON deployment_rollback_operations(updated_at,id)
 WHERE status NOT IN ('complete','failed');
CREATE FUNCTION guard_checked_rollback_traffic() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.status='live' AND NEW.traffic_percent>(CASE WHEN OLD.status='live' THEN OLD.traffic_percent ELSE 0 END)
 AND EXISTS(SELECT 1 FROM deployment_rollback_operations r WHERE r.target_deployment_id=NEW.id AND r.status NOT IN ('complete','failed')
   AND (r.status NOT IN ('ready','blocked') OR r.id::text IS DISTINCT FROM current_setting('faas.checked_rollback_request',true))) THEN
  RAISE EXCEPTION 'checked rollback operation required' USING ERRCODE='23514',CONSTRAINT='checked_rollback_required';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER checked_rollback_traffic BEFORE UPDATE OF status,traffic_percent ON deployments
 FOR EACH ROW EXECUTE FUNCTION guard_checked_rollback_traffic();
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER checked_rollback_traffic ON deployments;
DROP FUNCTION guard_checked_rollback_traffic();
DROP TABLE deployment_rollback_operations;
