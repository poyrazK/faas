-- +goose Up
-- +goose StatementBegin
-- ADR-141 §Decision 2: tri-state full-rootfs dispatch on deployments.
--   full_rootfs_allow_auto (bool NOT NULL): per-deployment opt-in for
--     the auto-fallback path. Defaults to false so a Free-plan
--     additive widen never silently auto-promotes a customer image to
--     full-rootfs — the apid handler reads api.FullRootfsAllowAutoDefault
--     (Hobby+ : true) and overwrites this column at INSERT time when
--     the request omits the field.
--   full_rootfs_override (bool NULL): per-deployment explicit override.
--     NULL  = honor auto + plan gate (today-equivalent for Free plan
--             without override; auto-fallback for Hobby+).
--     TRUE  = force full-rootfs even on Free plan.
--     FALSE = force today-equivalent failure even on Hobby+.
--
-- Tri-state is expressed as a NULLABLE bool rather than a 3-value enum
-- / text column so the wire field maps 1:1 to the Go *bool in
-- state.Deployment.FullRootfsOverride (ADR-141 §Decision 2).
ALTER TABLE deployments
  ADD COLUMN IF NOT EXISTS full_rootfs_allow_auto boolean NOT NULL DEFAULT false,
  ADD COLUMN IF NOT EXISTS full_rootfs_override   boolean;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE deployments
  DROP COLUMN IF EXISTS full_rootfs_override,
  DROP COLUMN IF EXISTS full_rootfs_allow_auto;
-- +goose StatementEnd
