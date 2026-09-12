-- +goose Up
-- +goose StatementBegin

-- Opt-in gateway-side route contract. Legacy apps remain unchanged because
-- the flag defaults off. Explicit route lists are stored alongside the flag;
-- an empty list means the gateway falls back to the imported app OpenAPI doc.
ALTER TABLE apps
  ADD COLUMN IF NOT EXISTS only_declared_routes boolean NOT NULL DEFAULT false,
  ADD COLUMN IF NOT EXISTS declared_routes jsonb NOT NULL DEFAULT '[]'::jsonb;

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1
    FROM pg_catalog.pg_constraint
    WHERE conname = 'apps_declared_routes_array_chk'
      AND conrelid = 'apps'::regclass
  ) THEN
    ALTER TABLE apps
      ADD CONSTRAINT apps_declared_routes_array_chk
      CHECK (jsonb_typeof(declared_routes) = 'array');
  END IF;
END;
$$;

-- Keep gateway app caches coherent when the policy itself changes. The
-- existing app_changed listener already resets the per-app route entry.
CREATE OR REPLACE FUNCTION apps_declared_routes_policy_notify()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  IF NEW.only_declared_routes IS DISTINCT FROM OLD.only_declared_routes
     OR NEW.declared_routes IS DISTINCT FROM OLD.declared_routes THEN
    PERFORM pg_notify('app_changed', NEW.id::text);
  END IF;
  RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS apps_declared_routes_policy_notify_trg ON apps;
CREATE TRIGGER apps_declared_routes_policy_notify_trg
AFTER UPDATE OF only_declared_routes, declared_routes ON apps
FOR EACH ROW
EXECUTE FUNCTION apps_declared_routes_policy_notify();

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TRIGGER IF EXISTS apps_declared_routes_policy_notify_trg ON apps;
DROP FUNCTION IF EXISTS apps_declared_routes_policy_notify();
ALTER TABLE apps DROP CONSTRAINT IF EXISTS apps_declared_routes_array_chk;
ALTER TABLE apps DROP COLUMN IF EXISTS declared_routes;
ALTER TABLE apps DROP COLUMN IF EXISTS only_declared_routes;

-- +goose StatementEnd
