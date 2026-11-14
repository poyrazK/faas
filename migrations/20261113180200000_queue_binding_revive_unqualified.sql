-- filename: 20261113180200000_queue_binding_revive_unqualified.sql
-- migration-compat: expand
-- ADR-20261113180000: 20261112193138262 replaced
-- public.guard_queue_binding_retirement, which is the live function in a
-- database whose objects live in public, but not the copy in a schema that
-- holds its own objects, as each Postgres test schema does: there a
-- re-created binding's revive was still refused. This redefines the
-- function, unqualified, with the same body, so it lands in the schema the
-- migrations run in. In public it changes nothing.

-- +goose Up
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_queue_binding_retirement() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
 IF TG_OP='DELETE' THEN
  IF OLD.retired_at IS NOT NULL AND EXISTS(SELECT 1 FROM apps WHERE id=OLD.app_id) THEN
   RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='queue_binding_retirement_identity',MESSAGE='retired queue identity requires explicit recovery';
  END IF;
  RETURN OLD;
 END IF;
 IF OLD.retired_at IS NOT NULL AND NEW.retired_at IS NULL
  AND (to_jsonb(NEW)-'retired_at'-'updated_at')=(to_jsonb(OLD)-'retired_at'-'updated_at')
  AND environment_gitops_queue_recovery_authorized(OLD.id) THEN
  RETURN NEW;
 END IF;
 IF OLD.retired_at IS NOT NULL AND NEW.retired_at IS NULL
  AND (to_jsonb(NEW)-'retired_at'-'updated_at')=(to_jsonb(OLD)-'retired_at'-'updated_at')
  AND OLD.environment_id IS NULL
  AND current_setting('gregale.queue_binding_revive', true)=OLD.id::text THEN
  RETURN NEW;
 END IF;
 IF OLD.retired_at IS NOT NULL AND
  (NEW.retired_at IS DISTINCT FROM OLD.retired_at OR NEW.id IS DISTINCT FROM OLD.id
   OR NEW.account_id IS DISTINCT FROM OLD.account_id OR NEW.app_id IS DISTINCT FROM OLD.app_id
   OR NEW.name IS DISTINCT FROM OLD.name OR NEW.queue_name IS DISTINCT FROM OLD.queue_name
   OR NEW.mode IS DISTINCT FROM OLD.mode OR NEW.workload_class IS DISTINCT FROM OLD.workload_class
   OR NEW.enabled IS DISTINCT FROM OLD.enabled OR NEW.max_concurrency IS DISTINCT FROM OLD.max_concurrency
   OR NEW.retry_policy IS DISTINCT FROM OLD.retry_policy) THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='queue_binding_retirement_identity',MESSAGE='retired queue binding requires explicit recovery';
 END IF;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose Down
-- The body is unchanged in public; nothing to restore.
SELECT 1;
