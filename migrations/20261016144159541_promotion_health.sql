-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
CREATE TABLE IF NOT EXISTS project_environment_promotion_health (
 promotion_id uuid PRIMARY KEY REFERENCES project_environment_promotions(id) ON DELETE CASCADE,
 generation bigint NOT NULL CHECK (generation >= 1),
 phase text NOT NULL CHECK (phase IN ('preflight','cutover','verifying','finalizing','rolling_back','succeeded','failed','conflicted','rollback_failed')),
 payload jsonb NOT NULL CHECK (jsonb_typeof(payload)='object' AND payload ? 'policy' AND jsonb_typeof(payload->'policy')='object' AND payload->>'phase'=phase AND (payload->>'generation')::bigint=generation AND octet_length(payload::text) <= 32768),
 lease_token uuid,
 lease_until timestamptz NOT NULL DEFAULT '-infinity'
);
CREATE INDEX IF NOT EXISTS project_environment_promotion_health_pending ON project_environment_promotion_health(promotion_id)
 WHERE phase IN ('preflight','cutover','verifying','finalizing','rolling_back');
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION protect_promotion_health_policy() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.payload->'policy' IS DISTINCT FROM OLD.payload->'policy' THEN
  RAISE EXCEPTION 'promotion health policy is immutable' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE OR REPLACE TRIGGER immutable_promotion_health_policy BEFORE UPDATE ON project_environment_promotion_health
FOR EACH ROW EXECUTE FUNCTION protect_promotion_health_policy();
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION protect_promotion_health_status() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE health_phase text;
BEGIN
 SELECT phase INTO health_phase FROM project_environment_promotion_health WHERE promotion_id=OLD.id;
 IF health_phase IS NOT NULL AND (
  (NEW.status='running' AND OLD.status<>'running') OR
  (NEW.status='succeeded' AND health_phase<>'succeeded') OR
  (NEW.status='failed' AND health_phase NOT IN ('preflight','failed','conflicted','rollback_failed'))
 ) THEN
  RAISE EXCEPTION 'promotion status is owned by destination health' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE OR REPLACE TRIGGER promotion_health_owns_status BEFORE UPDATE OF status ON project_environment_promotions
FOR EACH ROW EXECUTE FUNCTION protect_promotion_health_status();

-- +goose Down
DROP TRIGGER promotion_health_owns_status ON project_environment_promotions;
DROP FUNCTION protect_promotion_health_status();
DROP TABLE project_environment_promotion_health;
DROP FUNCTION protect_promotion_health_policy();
