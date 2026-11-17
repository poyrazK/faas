-- filename: 20261112193138262_queue_binding_revive.sql
-- migration-compat: expand
-- ADR-20261112200000: deleting a queue binding retires it and holds its
-- messages, and its name and queue name stay reserved, but no customer path
-- could release them: the name was burned for the app's lifetime and its
-- held messages could never drain. Re-creating a binding with the retired
-- binding's exact name and queue name now revives it. The guard admits only
-- that one transition: retired_at cleared, nothing else changed, in a
-- transaction that named this binding in gregale.queue_binding_revive. The
-- revived binding then takes the new settings through the ordinary update.

-- +goose Up
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.guard_queue_binding_retirement() RETURNS trigger
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
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.guard_queue_binding_retirement() RETURNS trigger
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
