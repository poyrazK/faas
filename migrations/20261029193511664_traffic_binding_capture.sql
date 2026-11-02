-- filename: 20261029193511664_traffic_binding_capture.sql
-- ADR-20261029190349 step 1. Row triggers on every source of a binding claim
-- or hostname reservation record which identities a transaction touched, one
-- tab-separated line each in the transaction-local setting
-- gregale.traffic_binding_touched: domain[+], tenant[+] (hostname), surface,
-- platform, alias[+] (app id, name), app[+], slug (old slug), environment;
-- "+" marks an insert. Updates fire only when a column the claim and
-- reservation queries read changes. apid's binding guards expand the lines to
-- hostnames at commit; every writer fires the triggers, the previous release's
-- included, and a transaction that never reads the setting discards it.
-- migration-compat: expand
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION traffic_binding_capture_append(line text) RETURNS void
LANGUAGE sql VOLATILE AS $$
  SELECT set_config('gregale.traffic_binding_touched',
    coalesce(current_setting('gregale.traffic_binding_touched', true), '') || line || E'\n', true);
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION traffic_binding_capture() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    CASE TG_TABLE_NAME
    WHEN 'custom_domains' THEN
        IF TG_OP = 'INSERT' THEN
            PERFORM traffic_binding_capture_append('domain+' || E'\t' || NEW.domain::text);
        ELSE
            PERFORM traffic_binding_capture_append('domain' || E'\t' || OLD.domain::text);
            IF TG_OP = 'UPDATE' AND NEW.domain IS DISTINCT FROM OLD.domain THEN
                PERFORM traffic_binding_capture_append('domain+' || E'\t' || NEW.domain::text);
            END IF;
        END IF;
    WHEN 'tenant_hostnames' THEN
        IF TG_OP = 'INSERT' THEN
            PERFORM traffic_binding_capture_append('tenant+' || E'\t' || NEW.hostname::text);
        ELSE
            PERFORM traffic_binding_capture_append('tenant' || E'\t' || OLD.hostname::text);
            IF TG_OP = 'UPDATE' AND NEW.hostname IS DISTINCT FROM OLD.hostname THEN
                PERFORM traffic_binding_capture_append('tenant+' || E'\t' || NEW.hostname::text);
            END IF;
        END IF;
    WHEN 'tenant_surfaces' THEN
        PERFORM traffic_binding_capture_append('surface' || E'\t' ||
            CASE WHEN TG_OP = 'DELETE' THEN OLD.id ELSE NEW.id END::text);
    WHEN 'platform_tenants' THEN
        PERFORM traffic_binding_capture_append('platform' || E'\t' ||
            CASE WHEN TG_OP = 'DELETE' THEN OLD.id ELSE NEW.id END::text);
    WHEN 'deployment_aliases' THEN
        IF TG_OP = 'INSERT' THEN
            PERFORM traffic_binding_capture_append('alias+' || E'\t' || NEW.app_id::text || E'\t' || NEW.name);
        ELSE
            PERFORM traffic_binding_capture_append('alias' || E'\t' || OLD.app_id::text || E'\t' || OLD.name);
            IF TG_OP = 'UPDATE' THEN
                PERFORM traffic_binding_capture_append('alias+' || E'\t' || NEW.app_id::text || E'\t' || NEW.name);
            END IF;
        END IF;
    WHEN 'apps' THEN
        IF TG_OP = 'INSERT' THEN
            PERFORM traffic_binding_capture_append('app+' || E'\t' || NEW.id::text);
        ELSIF TG_OP = 'DELETE' THEN
            PERFORM traffic_binding_capture_append('slug' || E'\t' || OLD.slug);
        ELSIF NEW.slug IS DISTINCT FROM OLD.slug THEN
            -- The new slug's primary URL is a newly reserved name.
            PERFORM traffic_binding_capture_append('app+' || E'\t' || NEW.id::text);
            PERFORM traffic_binding_capture_append('slug' || E'\t' || OLD.slug);
        ELSE
            PERFORM traffic_binding_capture_append('app' || E'\t' || NEW.id::text);
        END IF;
    WHEN 'project_environments' THEN
        PERFORM traffic_binding_capture_append('environment' || E'\t' ||
            CASE WHEN TG_OP = 'DELETE' THEN OLD.id ELSE NEW.id END::text);
    ELSE
        NULL;
    END CASE;
    RETURN NULL;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
-- custom_domains: traffic_binding_capture_rows
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   CREATE OR REPLACE TRIGGER traffic_binding_capture_rows
   AFTER INSERT OR DELETE ON custom_domains FOR EACH ROW
   EXECUTE FUNCTION traffic_binding_capture();
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: custom_domains: trigger traffic_binding_capture_rows' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd

-- +goose StatementBegin
-- custom_domains: traffic_binding_capture_update
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   CREATE OR REPLACE TRIGGER traffic_binding_capture_update
   AFTER UPDATE OF domain, app_id, environment_id, app_id_redirect, verified_at ON custom_domains FOR EACH ROW
   WHEN (OLD.domain IS DISTINCT FROM NEW.domain OR OLD.app_id IS DISTINCT FROM NEW.app_id OR OLD.environment_id IS DISTINCT FROM NEW.environment_id
         OR OLD.app_id_redirect IS DISTINCT FROM NEW.app_id_redirect OR (OLD.verified_at IS NULL) <> (NEW.verified_at IS NULL))
   EXECUTE FUNCTION traffic_binding_capture();
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: custom_domains: trigger traffic_binding_capture_update' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd

-- +goose StatementBegin
-- tenant_hostnames: traffic_binding_capture_rows
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   CREATE OR REPLACE TRIGGER traffic_binding_capture_rows
   AFTER INSERT OR DELETE ON tenant_hostnames FOR EACH ROW
   EXECUTE FUNCTION traffic_binding_capture();
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: tenant_hostnames: trigger traffic_binding_capture_rows' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd

-- +goose StatementBegin
-- tenant_hostnames: traffic_binding_capture_update
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   CREATE OR REPLACE TRIGGER traffic_binding_capture_update
   AFTER UPDATE OF hostname, surface_id, verified_at ON tenant_hostnames FOR EACH ROW
   WHEN (OLD.hostname IS DISTINCT FROM NEW.hostname OR OLD.surface_id IS DISTINCT FROM NEW.surface_id
         OR (OLD.verified_at IS NULL) <> (NEW.verified_at IS NULL))
   EXECUTE FUNCTION traffic_binding_capture();
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: tenant_hostnames: trigger traffic_binding_capture_update' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd

-- +goose StatementBegin
-- tenant_surfaces: traffic_binding_capture_rows
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   CREATE OR REPLACE TRIGGER traffic_binding_capture_rows
   AFTER INSERT OR DELETE ON tenant_surfaces FOR EACH ROW
   EXECUTE FUNCTION traffic_binding_capture();
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: tenant_surfaces: trigger traffic_binding_capture_rows' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd

-- +goose StatementBegin
-- tenant_surfaces: traffic_binding_capture_update
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   CREATE OR REPLACE TRIGGER traffic_binding_capture_update
   AFTER UPDATE OF app_id, account_id, platform_tenant_id, status ON tenant_surfaces FOR EACH ROW
   WHEN (OLD.app_id IS DISTINCT FROM NEW.app_id OR OLD.account_id IS DISTINCT FROM NEW.account_id
         OR OLD.platform_tenant_id IS DISTINCT FROM NEW.platform_tenant_id OR OLD.status IS DISTINCT FROM NEW.status)
   EXECUTE FUNCTION traffic_binding_capture();
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: tenant_surfaces: trigger traffic_binding_capture_update' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd

-- +goose StatementBegin
-- platform_tenants: traffic_binding_capture_rows
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   CREATE OR REPLACE TRIGGER traffic_binding_capture_rows
   AFTER DELETE ON platform_tenants FOR EACH ROW
   EXECUTE FUNCTION traffic_binding_capture();
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: platform_tenants: trigger traffic_binding_capture_rows' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd

-- +goose StatementBegin
-- platform_tenants: traffic_binding_capture_update
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   CREATE OR REPLACE TRIGGER traffic_binding_capture_update
   AFTER UPDATE OF status ON platform_tenants FOR EACH ROW
   WHEN (OLD.status IS DISTINCT FROM NEW.status)
   EXECUTE FUNCTION traffic_binding_capture();
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: platform_tenants: trigger traffic_binding_capture_update' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd

-- +goose StatementBegin
-- deployment_aliases: traffic_binding_capture_rows
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   CREATE OR REPLACE TRIGGER traffic_binding_capture_rows
   AFTER INSERT OR DELETE ON deployment_aliases FOR EACH ROW
   EXECUTE FUNCTION traffic_binding_capture();
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: deployment_aliases: trigger traffic_binding_capture_rows' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd

-- +goose StatementBegin
-- deployment_aliases: traffic_binding_capture_update
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   CREATE OR REPLACE TRIGGER traffic_binding_capture_update
   AFTER UPDATE OF name, app_id ON deployment_aliases FOR EACH ROW
   WHEN (OLD.name IS DISTINCT FROM NEW.name OR OLD.app_id IS DISTINCT FROM NEW.app_id)
   EXECUTE FUNCTION traffic_binding_capture();
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: deployment_aliases: trigger traffic_binding_capture_update' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd

-- +goose StatementBegin
-- apps: traffic_binding_capture_rows
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   CREATE OR REPLACE TRIGGER traffic_binding_capture_rows
   AFTER INSERT OR DELETE ON apps FOR EACH ROW
   EXECUTE FUNCTION traffic_binding_capture();
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: apps: trigger traffic_binding_capture_rows' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd

-- +goose StatementBegin
-- apps: traffic_binding_capture_update
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   CREATE OR REPLACE TRIGGER traffic_binding_capture_update
   AFTER UPDATE OF slug, status, visibility, account_id, project_id ON apps FOR EACH ROW
   WHEN (OLD.slug IS DISTINCT FROM NEW.slug OR OLD.status IS DISTINCT FROM NEW.status OR OLD.visibility IS DISTINCT FROM NEW.visibility
         OR OLD.account_id IS DISTINCT FROM NEW.account_id OR OLD.project_id IS DISTINCT FROM NEW.project_id)
   EXECUTE FUNCTION traffic_binding_capture();
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: apps: trigger traffic_binding_capture_update' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd

-- +goose StatementBegin
-- project_environments: traffic_binding_capture_rows
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   CREATE OR REPLACE TRIGGER traffic_binding_capture_rows
   AFTER INSERT OR DELETE ON project_environments FOR EACH ROW
   EXECUTE FUNCTION traffic_binding_capture();
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: project_environments: trigger traffic_binding_capture_rows' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd

-- +goose StatementBegin
-- project_environments: traffic_binding_capture_update
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   CREATE OR REPLACE TRIGGER traffic_binding_capture_update
   AFTER UPDATE OF account_id, project_id ON project_environments FOR EACH ROW
   WHEN (OLD.account_id IS DISTINCT FROM NEW.account_id OR OLD.project_id IS DISTINCT FROM NEW.project_id)
   EXECUTE FUNCTION traffic_binding_capture();
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: project_environments: trigger traffic_binding_capture_update' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd

RESET lock_timeout;

-- +goose Down
SET lock_timeout = '5s';

-- +goose StatementBegin
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   DROP TRIGGER IF EXISTS traffic_binding_capture_rows ON custom_domains;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: custom_domains: drop trigger traffic_binding_capture_rows' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   DROP TRIGGER IF EXISTS traffic_binding_capture_update ON custom_domains;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: custom_domains: drop trigger traffic_binding_capture_update' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   DROP TRIGGER IF EXISTS traffic_binding_capture_rows ON tenant_hostnames;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: tenant_hostnames: drop trigger traffic_binding_capture_rows' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   DROP TRIGGER IF EXISTS traffic_binding_capture_update ON tenant_hostnames;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: tenant_hostnames: drop trigger traffic_binding_capture_update' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   DROP TRIGGER IF EXISTS traffic_binding_capture_rows ON tenant_surfaces;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: tenant_surfaces: drop trigger traffic_binding_capture_rows' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   DROP TRIGGER IF EXISTS traffic_binding_capture_update ON tenant_surfaces;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: tenant_surfaces: drop trigger traffic_binding_capture_update' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   DROP TRIGGER IF EXISTS traffic_binding_capture_rows ON platform_tenants;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: platform_tenants: drop trigger traffic_binding_capture_rows' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   DROP TRIGGER IF EXISTS traffic_binding_capture_update ON platform_tenants;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: platform_tenants: drop trigger traffic_binding_capture_update' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   DROP TRIGGER IF EXISTS traffic_binding_capture_rows ON deployment_aliases;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: deployment_aliases: drop trigger traffic_binding_capture_rows' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   DROP TRIGGER IF EXISTS traffic_binding_capture_update ON deployment_aliases;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: deployment_aliases: drop trigger traffic_binding_capture_update' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   DROP TRIGGER IF EXISTS traffic_binding_capture_rows ON apps;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: apps: drop trigger traffic_binding_capture_rows' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   DROP TRIGGER IF EXISTS traffic_binding_capture_update ON apps;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: apps: drop trigger traffic_binding_capture_update' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   DROP TRIGGER IF EXISTS traffic_binding_capture_rows ON project_environments;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: project_environments: drop trigger traffic_binding_capture_rows' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   DROP TRIGGER IF EXISTS traffic_binding_capture_update ON project_environments;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: project_environments: drop trigger traffic_binding_capture_update' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd

DROP FUNCTION IF EXISTS traffic_binding_capture();
DROP FUNCTION IF EXISTS traffic_binding_capture_append(text);
RESET lock_timeout;
