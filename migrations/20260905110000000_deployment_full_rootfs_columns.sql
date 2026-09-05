-- +goose Up
-- +goose StatementBegin
-- ADR-141: persist the per-deployment policy for arbitrary OCI fallback.
ALTER TABLE deployments
  ADD COLUMN IF NOT EXISTS full_rootfs_allow_auto boolean NOT NULL DEFAULT false,
  ADD COLUMN IF NOT EXISTS full_rootfs_override boolean;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE deployments
  DROP COLUMN IF EXISTS full_rootfs_override,
  DROP COLUMN IF EXISTS full_rootfs_allow_auto;
-- +goose StatementEnd
