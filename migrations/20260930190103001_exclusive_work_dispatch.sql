-- filename: 20260930190103001_exclusive_work_dispatch.sql
-- +goose Up
ALTER TABLE exclusive_work_operations
  ADD COLUMN due_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  ADD COLUMN attempts integer NOT NULL DEFAULT 0 CHECK (attempts>=0),
  ADD COLUMN quota_reserved boolean NOT NULL DEFAULT false,
  ADD CONSTRAINT exclusive_work_quota_shape CHECK (NOT quota_reserved OR state='running');
ALTER TABLE instances ADD COLUMN exclusive_capture_blocked boolean NOT NULL DEFAULT false;
CREATE INDEX exclusive_work_due_idx ON exclusive_work_operations(due_at) WHERE state='pending';

-- +goose StatementBegin
CREATE FUNCTION exclusive_work_release_quota() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF OLD.quota_reserved AND NEW.state<>'running' THEN
    UPDATE account_async_quota SET current_inflight=greatest(0,current_inflight-1),updated_at=clock_timestamp()
      WHERE account_id=OLD.account_id;
    NEW.quota_reserved := false;
  END IF;
  RETURN NEW;
END;
$$;
CREATE TRIGGER exclusive_work_release_quota BEFORE UPDATE ON exclusive_work_operations
  FOR EACH ROW EXECUTE FUNCTION exclusive_work_release_quota();
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER exclusive_work_release_quota ON exclusive_work_operations;
DROP FUNCTION exclusive_work_release_quota();
DROP INDEX exclusive_work_due_idx;
ALTER TABLE instances DROP COLUMN exclusive_capture_blocked;
ALTER TABLE exclusive_work_operations DROP CONSTRAINT exclusive_work_quota_shape,
  DROP COLUMN quota_reserved,DROP COLUMN attempts,DROP COLUMN due_at;
