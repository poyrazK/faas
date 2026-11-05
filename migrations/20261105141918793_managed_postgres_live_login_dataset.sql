-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE managed_postgres_live_logins
 ADD COLUMN data_resource_id text,
 ADD COLUMN authorized_identity_id text,
 ADD CONSTRAINT managed_postgres_live_logins_data_resource_id_check
  CHECK (data_resource_id IS NULL OR length(data_resource_id) BETWEEN 1 AND 255) NOT VALID,
 ADD CONSTRAINT managed_postgres_live_logins_authorized_identity_id_check
  CHECK (authorized_identity_id IS NULL OR authorized_identity_id ~ '^[a-z0-9_]{1,63}$') NOT VALID,
 ADD CONSTRAINT managed_postgres_live_logins_dataset_pair_check
  CHECK ((data_resource_id IS NULL) = (authorized_identity_id IS NULL)) NOT VALID;

-- +goose Down
SET LOCAL lock_timeout = '5s';
ALTER TABLE managed_postgres_live_logins
 DROP COLUMN authorized_identity_id,
 DROP COLUMN data_resource_id;
