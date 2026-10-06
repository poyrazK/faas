-- ADR-581: private queue receipts retain their own environment identity.
-- +goose Up
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_invocation_queue_binding() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP='UPDATE' THEN
    IF NEW.queue_binding_id IS DISTINCT FROM OLD.queue_binding_id OR
      (OLD.queue_binding_id IS NOT NULL AND (NEW.app_id IS DISTINCT FROM OLD.app_id
        OR NEW.account_id IS DISTINCT FROM OLD.account_id OR NEW.source IS DISTINCT FROM OLD.source
        OR NEW.queue_name IS DISTINCT FROM OLD.queue_name)) THEN
      RAISE EXCEPTION USING ERRCODE='23514', CONSTRAINT='invocation_queue_binding_identity',
        MESSAGE='accepted queue binding identity is immutable';
    END IF;
    RETURN NEW;
  END IF;
  IF NEW.queue_binding_id IS NOT NULL AND NEW.source<>'queue' THEN
    RAISE EXCEPTION USING ERRCODE='23514', CONSTRAINT='invocation_queue_binding_source',
      MESSAGE='binding identity requires a queue invocation';
  END IF;
  IF NEW.environment_id IS NOT NULL AND NEW.source='queue' THEN
    IF NEW.queue_binding_id IS NOT NULL THEN
      RAISE EXCEPTION USING ERRCODE='23514', CONSTRAINT='invocation_private_queue_binding',
        MESSAGE='private queue work cannot capture a public binding';
    END IF;
    RETURN NEW;
  END IF;
  IF NEW.source='queue' THEN
    IF NEW.queue_binding_id IS NULL AND NEW.queue_name<>'' THEN
      SELECT b.id INTO NEW.queue_binding_id FROM queue_bindings b
        WHERE b.app_id=NEW.app_id AND b.account_id=NEW.account_id AND b.queue_name=NEW.queue_name
          AND (b.deployment_scope='' OR (b.deployment_scope=NEW.deployment_scope AND EXISTS
            (SELECT 1 FROM apps a JOIN project_environments e ON e.project_id=a.project_id AND e.account_id=a.account_id
              WHERE a.id=NEW.app_id AND e.id=b.environment_id AND e.slug=b.deployment_scope)))
        ORDER BY (b.deployment_scope<>'') DESC LIMIT 1;
    ELSIF NEW.queue_binding_id IS NULL AND NEW.work_policy_name IS NULL THEN
      SELECT b.id INTO NEW.queue_binding_id FROM queue_bindings b JOIN triggers t ON t.queue_binding_id=b.id
        WHERE b.app_id=NEW.app_id AND b.account_id=NEW.account_id AND b.enabled
          AND (b.deployment_scope='' OR (b.deployment_scope=NEW.deployment_scope AND EXISTS
            (SELECT 1 FROM apps a JOIN project_environments e ON e.project_id=a.project_id AND e.account_id=a.account_id
              WHERE a.id=NEW.app_id AND e.id=b.environment_id AND e.slug=b.deployment_scope)))
          AND b.mode='push' AND b.retired_at IS NULL AND t.enabled AND t.kind='queue' AND t.source='queue'
          AND NOT EXISTS (SELECT 1 FROM triggers other WHERE other.app_id=NEW.app_id
            AND other.kind='queue' AND other.source='queue' AND other.enabled AND other.id<>t.id
            AND (other.queue_binding_scope='' OR (other.queue_binding_scope=NEW.deployment_scope AND EXISTS
              (SELECT 1 FROM apps a JOIN project_environments e ON e.project_id=a.project_id AND e.account_id=a.account_id
                WHERE a.id=NEW.app_id AND e.id=other.queue_binding_environment_id AND e.slug=other.queue_binding_scope))));
    END IF;
    IF NEW.queue_binding_id IS NOT NULL THEN
      PERFORM 1 FROM queue_bindings b WHERE b.id=NEW.queue_binding_id
        AND b.app_id=NEW.app_id AND b.account_id=NEW.account_id
        AND (b.deployment_scope='' OR (b.deployment_scope=NEW.deployment_scope AND EXISTS
            (SELECT 1 FROM apps a JOIN project_environments e ON e.project_id=a.project_id AND e.account_id=a.account_id
              WHERE a.id=NEW.app_id AND e.id=b.environment_id AND e.slug=b.deployment_scope))) FOR SHARE;
      IF NOT FOUND THEN
        RAISE EXCEPTION USING ERRCODE='23514', CONSTRAINT='invocation_queue_binding_tenant',
          MESSAGE='queue binding must belong to the admitted app, account and environment';
      END IF;
    END IF;
  END IF;
  RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION guard_retired_queue_invocation() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE binding_retired_at timestamptz; binding_scope text; binding_environment uuid;
BEGIN
  IF NEW.environment_id IS NOT NULL THEN
    RETURN NEW;
  END IF;
  IF NEW.source='queue' AND (TG_OP='INSERT' OR
    (NEW.state='dispatching' AND OLD.state IS DISTINCT FROM 'dispatching')) THEN
    SELECT b.retired_at,b.deployment_scope,b.environment_id INTO binding_retired_at,binding_scope,binding_environment FROM queue_bindings b
      WHERE b.app_id=NEW.app_id AND b.account_id=NEW.account_id AND
        (b.id=NEW.queue_binding_id OR (NEW.queue_binding_id IS NULL
          AND b.deployment_scope='' AND b.queue_name=NEW.queue_name)) FOR SHARE;
    IF binding_scope<>'' AND NOT EXISTS (SELECT 1 FROM apps a JOIN project_environments e
      ON e.project_id=a.project_id AND e.account_id=a.account_id WHERE a.id=NEW.app_id
        AND e.id=binding_environment AND e.slug=binding_scope) THEN
      RAISE EXCEPTION USING ERRCODE='23514', CONSTRAINT='queue_binding_environment_unavailable',
        MESSAGE='captured queue binding environment is unavailable';
    END IF;
    IF binding_retired_at IS NOT NULL THEN
      RAISE EXCEPTION USING ERRCODE='23514', CONSTRAINT='queue_binding_retired', MESSAGE='queue binding is retired';
    END IF;
  END IF;
  RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose Down
-- Retain accepted ownership guards during rollback.
SELECT 1;
