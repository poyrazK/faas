-- ADR-797: globally provisioned apps must not borrow independent region limits.
-- +goose Up
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION assert_regional_runtime_quota_open(app_arg uuid) RETURNS void
LANGUAGE plpgsql AS $$
BEGIN
 IF app_arg IS NULL THEN RETURN; END IF;
 IF current_setting('transaction_isolation') <> 'read committed' THEN
  RAISE EXCEPTION 'regional runtime admission requires read committed isolation'
   USING ERRCODE='23514',CONSTRAINT='regional_runtime_quota_frozen';
 END IF;
 PERFORM pg_advisory_xact_lock(hashtextextended('gregale-regional-runtime-quota:' || app_arg::text,0));
 IF EXISTS(SELECT 1 FROM regional_runtime_quota_heads WHERE app_id=app_arg) OR
    EXISTS(SELECT 1 FROM regional_app_provision_receipts WHERE app_id=app_arg) THEN
  RAISE EXCEPTION 'regional runtime quota admission is frozen'
   USING ERRCODE='23514',CONSTRAINT='regional_runtime_quota_frozen';
 END IF;
END; $$;
-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 LOCK TABLE regional_app_provision_receipts IN ACCESS EXCLUSIVE MODE;
 IF EXISTS(SELECT 1 FROM regional_app_provision_receipts) THEN
  RAISE EXCEPTION 'cannot downgrade the runtime fence for provisioned apps';
 END IF;
END; $$;
CREATE OR REPLACE FUNCTION assert_regional_runtime_quota_open(app_arg uuid) RETURNS void
LANGUAGE plpgsql AS $$
BEGIN
 IF app_arg IS NULL THEN RETURN; END IF;
 IF current_setting('transaction_isolation') <> 'read committed' THEN
  RAISE EXCEPTION 'regional runtime admission requires read committed isolation'
   USING ERRCODE='23514',CONSTRAINT='regional_runtime_quota_frozen';
 END IF;
 PERFORM pg_advisory_xact_lock(hashtextextended('gregale-regional-runtime-quota:' || app_arg::text,0));
 IF EXISTS(SELECT 1 FROM regional_runtime_quota_heads WHERE app_id=app_arg) THEN
  RAISE EXCEPTION 'regional runtime quota admission is frozen'
   USING ERRCODE='23514',CONSTRAINT='regional_runtime_quota_frozen';
 END IF;
END; $$;
-- +goose StatementEnd
