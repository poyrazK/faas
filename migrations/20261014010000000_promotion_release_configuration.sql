-- +goose Up
CREATE TABLE project_environment_promotion_release_config (
 promotion_id uuid PRIMARY KEY REFERENCES project_environment_promotions(id) ON DELETE CASCADE,
 payload jsonb NOT NULL CHECK (jsonb_typeof(payload)='object' AND payload ? 'hash' AND jsonb_typeof(payload->'hash')='string' AND payload->>'hash' ~ '^[a-f0-9]{64}$')
);
-- +goose StatementBegin
CREATE FUNCTION protect_promotion_release_configuration() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='UPDATE' OR EXISTS(SELECT 1 FROM project_environment_promotions WHERE id=OLD.promotion_id) THEN
  RAISE EXCEPTION 'promotion release configuration follows promotion lifetime' USING ERRCODE='23514';
 END IF;
 RETURN OLD;
END $$;
-- +goose StatementEnd
CREATE TRIGGER immutable_promotion_release_configuration BEFORE UPDATE OR DELETE ON project_environment_promotion_release_config
FOR EACH ROW EXECUTE FUNCTION protect_promotion_release_configuration();

-- +goose Down
DROP TABLE project_environment_promotion_release_config;
DROP FUNCTION protect_promotion_release_configuration();
