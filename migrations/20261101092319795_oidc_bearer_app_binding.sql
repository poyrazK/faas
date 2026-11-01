-- filename: 20261101092319795_oidc_bearer_app_binding.sql
-- migration-compat: expand

-- ADR-20261101100000: a deploy bearer minted under a trust policy Gregale
-- created is bound to the one app it was exchanged for, an app bound to the
-- token's repository. The column is nullable with no default, so adding it is
-- metadata-only; an older apid writes no binding, which keeps its bearers
-- account-wide as before. A bearer lives at most an hour, so no foreign key:
-- a deleted app is refused when the bearer names it.

-- +goose Up
SET LOCAL lock_timeout = '5s';

ALTER TABLE oidc_exchanged_tokens ADD COLUMN IF NOT EXISTS app_id uuid;

-- +goose Down
SET LOCAL lock_timeout = '5s';

ALTER TABLE oidc_exchanged_tokens DROP COLUMN IF EXISTS app_id;
