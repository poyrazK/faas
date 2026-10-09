-- +goose Up
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION notify_deployment_lifecycle_capture_changed()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP = 'DELETE' THEN
    PERFORM pg_notify('app_openapi_doc_changed', json_build_object('app_id', OLD.app_id, 'deployment_id', OLD.deployment_id)::text);
    RETURN OLD;
  END IF;
  PERFORM pg_notify('app_openapi_doc_changed', json_build_object('app_id', NEW.app_id, 'deployment_id', NEW.deployment_id)::text);
  RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS deployment_lifecycle_capture_changed ON deployment_openapi_docs;
CREATE TRIGGER deployment_lifecycle_capture_changed
AFTER INSERT OR UPDATE OR DELETE ON deployment_openapi_docs
FOR EACH ROW EXECUTE FUNCTION notify_deployment_lifecycle_capture_changed();
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER IF EXISTS deployment_lifecycle_capture_changed ON deployment_openapi_docs;
DROP FUNCTION IF EXISTS notify_deployment_lifecycle_capture_changed();
