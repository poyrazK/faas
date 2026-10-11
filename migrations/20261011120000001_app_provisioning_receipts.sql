-- ADR-797: creation receipts survive app removal; directory intent pins dispatch.
-- +goose Up
CREATE TABLE regional_app_provision_receipts (
 provision_id uuid PRIMARY KEY CHECK(provision_id <> '00000000-0000-0000-0000-000000000000'),
 global_app_id uuid NOT NULL CHECK(global_app_id <> '00000000-0000-0000-0000-000000000000'),
 cell_id uuid NOT NULL CHECK(cell_id <> '00000000-0000-0000-0000-000000000000'),
 region text NOT NULL CHECK(region ~ '^[a-z][a-z0-9]*(-[a-z0-9]+)+$'),
 account_id uuid NOT NULL CHECK(account_id <> '00000000-0000-0000-0000-000000000000'),
 app_id uuid NOT NULL CHECK(app_id <> '00000000-0000-0000-0000-000000000000'),
 app_slug text NOT NULL CHECK(app_slug ~ '^[a-z0-9][a-z0-9-]{1,38}[a-z0-9]$'),
 request jsonb NOT NULL CHECK(jsonb_typeof(request)='object' AND octet_length(request::text) <= 4096),
 created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(global_app_id,cell_id), UNIQUE(app_id)
);
CREATE TABLE global_app_provisioning (
 global_app_id uuid NOT NULL REFERENCES global_apps(id),
 owner_id uuid NOT NULL REFERENCES global_directory_owners(id),
 region text NOT NULL CHECK(region ~ '^[a-z][a-z0-9]*(-[a-z0-9]+)+$'),
 provision_id uuid NOT NULL UNIQUE CHECK(provision_id <> '00000000-0000-0000-0000-000000000000'),
 cell_id uuid NOT NULL REFERENCES global_directory_cells(id),
 cell_generation bigint NOT NULL CHECK(cell_generation > 0),
 api_base text NOT NULL CHECK(api_base ~ '^https://[^/?#]+$'),
 regional_account_id uuid NOT NULL CHECK(regional_account_id <> '00000000-0000-0000-0000-000000000000'),
 request jsonb NOT NULL CHECK(jsonb_typeof(request)='object' AND octet_length(request::text) <= 4096),
 status text NOT NULL DEFAULT 'planned' CHECK(status IN ('planned','attached','cancelled')),
 PRIMARY KEY(global_app_id,region)
);
-- +goose StatementBegin
CREATE FUNCTION guard_regional_app_provision_receipt() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP <> 'INSERT' THEN
  RAISE EXCEPTION 'retain immutable regional app provisioning receipt' USING ERRCODE='23514';
 END IF;
 IF NOT EXISTS(SELECT 1 FROM control_plane_cells WHERE id=NEW.cell_id AND compute_region=NEW.region) OR
    NOT EXISTS(SELECT 1 FROM apps WHERE id=NEW.app_id AND account_id=NEW.account_id AND slug=NEW.app_slug AND compute_region=NEW.region) THEN
  RAISE EXCEPTION 'regional app provisioning identity conflicts' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END; $$;
CREATE FUNCTION guard_global_app_provisioning() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 PERFORM assert_global_directory_scope();
 IF TG_OP IN ('DELETE','TRUNCATE') THEN
  RAISE EXCEPTION 'retain global app provisioning intent' USING ERRCODE='23514';
 END IF;
 IF TG_OP='INSERT' AND (NEW.status<>'planned' OR NOT EXISTS(
  SELECT 1 FROM global_apps a JOIN global_directory_owners o ON o.id=a.owner_id
  JOIN global_directory_cells c ON c.id=NEW.cell_id
  WHERE a.id=NEW.global_app_id AND a.owner_id=NEW.owner_id AND o.account_id IS NOT NULL
  AND c.region=NEW.region AND c.generation=NEW.cell_generation AND c.api_base=NEW.api_base AND c.enabled
 )) THEN
  RAISE EXCEPTION 'global app provisioning identity conflicts' USING ERRCODE='23514';
 END IF;
 IF TG_OP='UPDATE' AND (ROW(NEW.global_app_id,NEW.owner_id,NEW.region,NEW.provision_id,NEW.cell_id,NEW.cell_generation,NEW.api_base,NEW.regional_account_id,NEW.request)
  IS DISTINCT FROM ROW(OLD.global_app_id,OLD.owner_id,OLD.region,OLD.provision_id,OLD.cell_id,OLD.cell_generation,OLD.api_base,OLD.regional_account_id,OLD.request)
  OR (OLD.status='cancelled' AND NEW.status<>'cancelled') OR (OLD.status='attached' AND NEW.status='planned')) THEN
  RAISE EXCEPTION 'global app provisioning intent is immutable' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END; $$;
CREATE FUNCTION cancel_detached_app_provisioning() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 UPDATE global_app_provisioning SET status='cancelled' WHERE global_app_id=OLD.global_app_id AND region=OLD.region;
 RETURN OLD;
END; $$;
-- +goose StatementEnd
CREATE TRIGGER regional_app_provision_guard BEFORE INSERT OR UPDATE OR DELETE ON regional_app_provision_receipts FOR EACH ROW EXECUTE FUNCTION guard_regional_app_provision_receipt();
CREATE TRIGGER regional_app_provision_truncate_guard BEFORE TRUNCATE ON regional_app_provision_receipts FOR EACH STATEMENT EXECUTE FUNCTION guard_regional_app_provision_receipt();
CREATE TRIGGER global_app_provision_guard BEFORE INSERT OR UPDATE OR DELETE ON global_app_provisioning FOR EACH ROW EXECUTE FUNCTION guard_global_app_provisioning();
CREATE TRIGGER global_app_provision_truncate_guard BEFORE TRUNCATE ON global_app_provisioning FOR EACH STATEMENT EXECUTE FUNCTION guard_global_app_provisioning();
CREATE TRIGGER cancel_detached_app_provision AFTER DELETE ON global_app_targets FOR EACH ROW EXECUTE FUNCTION cancel_detached_app_provisioning();
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM global_app_provisioning) OR EXISTS(SELECT 1 FROM regional_app_provision_receipts) THEN
  RAISE EXCEPTION 'cannot downgrade retained app provisioning receipts';
 END IF;
END; $$;
-- +goose StatementEnd
DROP TRIGGER cancel_detached_app_provision ON global_app_targets;
DROP TABLE global_app_provisioning, regional_app_provision_receipts;
DROP FUNCTION cancel_detached_app_provisioning(), guard_global_app_provisioning(), guard_regional_app_provision_receipt();
