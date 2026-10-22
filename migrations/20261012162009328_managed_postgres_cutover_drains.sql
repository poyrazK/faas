-- filename: 20261012162009328_managed_postgres_cutover_drains.sql
-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS managed_postgres_cutover_drains (
 id uuid PRIMARY KEY,
 cutover_id uuid NOT NULL,
 app_id uuid NOT NULL,
 deployment_id uuid NOT NULL,
 instance_id uuid NOT NULL,
 node_id uuid NOT NULL,
 wake_id uuid NOT NULL,
 owner_node_id uuid NOT NULL,
 owner_revision bigint NOT NULL CHECK (owner_revision > 0),
 fenced_at timestamptz NOT NULL,
 state text NOT NULL DEFAULT 'pending' CHECK (state IN ('pending','retired')),
 native_generation uuid,
 kernel_boot_id uuid,
 retired_at timestamptz,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 CHECK ((state='pending' AND native_generation IS NULL AND kernel_boot_id IS NULL AND retired_at IS NULL)
  OR (state='retired' AND native_generation IS NOT NULL AND kernel_boot_id IS NOT NULL AND retired_at IS NOT NULL)),
 CONSTRAINT managed_postgres_cutover_drain_identity_key UNIQUE (cutover_id,instance_id,node_id,wake_id,owner_node_id,owner_revision,fenced_at),
 CHECK (native_generation IS NULL OR native_generation<>'00000000-0000-0000-0000-000000000000'),
 CHECK (kernel_boot_id IS NULL OR kernel_boot_id<>'00000000-0000-0000-0000-000000000000')
);
-- No parent FKs: deletion/decommissioning must not erase receipt identity or
-- block the lifecycle owner's account deletion and terminal retention paths.
CREATE INDEX IF NOT EXISTS managed_postgres_cutover_drains_pending_idx ON managed_postgres_cutover_drains(owner_node_id,id) WHERE state='pending';
CREATE OR REPLACE FUNCTION guard_managed_postgres_cutover_drain() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF ROW(NEW.id,NEW.cutover_id,NEW.app_id,NEW.deployment_id,NEW.instance_id,NEW.node_id,NEW.wake_id,NEW.owner_node_id,NEW.owner_revision,NEW.fenced_at,NEW.created_at)
  IS DISTINCT FROM ROW(OLD.id,OLD.cutover_id,OLD.app_id,OLD.deployment_id,OLD.instance_id,OLD.node_id,OLD.wake_id,OLD.owner_node_id,OLD.owner_revision,OLD.fenced_at,OLD.created_at)
  OR (OLD.state='retired' AND NEW IS DISTINCT FROM OLD) THEN
  RAISE EXCEPTION 'drain identity and receipts are immutable' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS managed_postgres_cutover_drain_guard ON managed_postgres_cutover_drains;
CREATE TRIGGER managed_postgres_cutover_drain_guard BEFORE UPDATE ON managed_postgres_cutover_drains
 FOR EACH ROW EXECUTE FUNCTION guard_managed_postgres_cutover_drain();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM managed_postgres_cutover_drains) THEN
  RAISE EXCEPTION 'drain receipts must be preserved before rollback';
 END IF;
END $$;
DROP TABLE managed_postgres_cutover_drains;
DROP FUNCTION guard_managed_postgres_cutover_drain();
-- +goose StatementEnd
