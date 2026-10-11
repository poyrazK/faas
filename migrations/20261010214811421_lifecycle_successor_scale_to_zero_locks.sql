-- filename: 20261010214811421_lifecycle_successor_scale_to_zero_locks.sql

-- Production-us rc.251: every park and wake flips apps.status between
-- 'active' and 'evicted_cold', which fired lifecycle_successor_apps and took
-- `accounts ... FOR UPDATE` while the wake transition held the app row. Every
-- request-ID journal and instance insert takes FOR KEY SHARE on the account
-- (FK) and then on the app, so a burst of requests to a parked app deadlocked
-- the wake (SQLSTATE 40P01): 150 concurrent requests all failed with 500 or
-- 504, and the account row serialized all of the account's telemetry behind
-- each wake.
--
-- A scale-to-zero status flip changes nothing a route lifecycle approval
-- depends on, so it no longer invalidates approvals or takes the lock.
-- Real lifecycle changes lock the account FOR NO KEY UPDATE: it still
-- conflicts with the approval writer's FOR UPDATE on the same row
-- (LockRoutePolicyAccount), so invalidation stays serialized with approval,
-- but it no longer conflicts with foreign-key checks.

-- +goose Up
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION invalidate_lifecycle_successor_routes() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE oldrow jsonb; newrow jsonb; affected uuid; owner uuid; hostname text;
 watched text[] := ARRAY['status','slug','manifest','visibility','org_id','project_id','account_id',
                         'only_declared_routes','declared_routes','consumer_auth_mode','maintenance_mode'];
BEGIN
 IF TG_OP<>'INSERT' THEN oldrow:=to_jsonb(OLD); END IF;
 IF TG_OP<>'DELETE' THEN newrow:=to_jsonb(NEW); END IF;
 IF TG_TABLE_NAME='tenant_hostnames' THEN
  UPDATE route_lifecycle_approvals r SET invalidated_at=clock_timestamp() WHERE r.invalidated_at IS NULL AND EXISTS(SELECT 1 FROM jsonb_array_elements(r.receipt->'mappings') m WHERE split_part(m->>'successor_url','/',3) IN (oldrow->>'hostname',newrow->>'hostname'));
  RETURN NULL;
 END IF;
 IF TG_TABLE_NAME='apps' AND TG_OP='UPDATE' AND
    (SELECT jsonb_object_agg(k, CASE WHEN k='status' AND v #>> '{}' IN ('active','evicted_cold') THEN '"scale_to_zero"'::jsonb ELSE v END)
       FROM jsonb_each(oldrow) e(k,v) WHERE k=ANY(watched))
    IS NOT DISTINCT FROM
    (SELECT jsonb_object_agg(k, CASE WHEN k='status' AND v #>> '{}' IN ('active','evicted_cold') THEN '"scale_to_zero"'::jsonb ELSE v END)
       FROM jsonb_each(newrow) e(k,v) WHERE k=ANY(watched)) THEN
  RETURN NULL;
 END IF;
 FOR affected IN SELECT DISTINCT id FROM (VALUES(coalesce(oldrow->>'app_id',CASE WHEN TG_TABLE_NAME='apps' THEN oldrow->>'id' END)::uuid),(coalesce(newrow->>'app_id',CASE WHEN TG_TABLE_NAME='apps' THEN newrow->>'id' END)::uuid)) ids(id) WHERE id IS NOT NULL LOOP
  SELECT account_id INTO owner FROM apps WHERE id=affected;
  PERFORM 1 FROM accounts WHERE id=owner FOR NO KEY UPDATE;
  UPDATE route_lifecycle_approvals r SET invalidated_at=clock_timestamp() WHERE r.invalidated_at IS NULL AND
   ((TG_TABLE_NAME<>'deployments' AND r.app_id=affected) OR EXISTS(SELECT 1 FROM jsonb_array_elements(r.receipt->'mappings') m WHERE m->>'successor_app_id'=affected::text AND (TG_TABLE_NAME<>'deployments' OR affected<>r.app_id)));
 END LOOP;
 RETURN NULL;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION invalidate_lifecycle_successor_routes() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE oldrow jsonb; newrow jsonb; affected uuid; owner uuid; hostname text;
BEGIN
 IF TG_OP<>'INSERT' THEN oldrow:=to_jsonb(OLD); END IF;
 IF TG_OP<>'DELETE' THEN newrow:=to_jsonb(NEW); END IF;
 IF TG_TABLE_NAME='tenant_hostnames' THEN
  UPDATE route_lifecycle_approvals r SET invalidated_at=clock_timestamp() WHERE r.invalidated_at IS NULL AND EXISTS(SELECT 1 FROM jsonb_array_elements(r.receipt->'mappings') m WHERE split_part(m->>'successor_url','/',3) IN (oldrow->>'hostname',newrow->>'hostname'));
  RETURN NULL;
 END IF;
 FOR affected IN SELECT DISTINCT id FROM (VALUES(coalesce(oldrow->>'app_id',CASE WHEN TG_TABLE_NAME='apps' THEN oldrow->>'id' END)::uuid),(coalesce(newrow->>'app_id',CASE WHEN TG_TABLE_NAME='apps' THEN newrow->>'id' END)::uuid)) ids(id) WHERE id IS NOT NULL LOOP
  SELECT account_id INTO owner FROM apps WHERE id=affected;
  PERFORM 1 FROM accounts WHERE id=owner FOR UPDATE;
  UPDATE route_lifecycle_approvals r SET invalidated_at=clock_timestamp() WHERE r.invalidated_at IS NULL AND
   ((TG_TABLE_NAME<>'deployments' AND r.app_id=affected) OR EXISTS(SELECT 1 FROM jsonb_array_elements(r.receipt->'mappings') m WHERE m->>'successor_app_id'=affected::text AND (TG_TABLE_NAME<>'deployments' OR affected<>r.app_id)));
 END LOOP;
 RETURN NULL;
END $$;
-- +goose StatementEnd
