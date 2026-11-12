-- filename: 20261111110038171_object_inventory_quiet_mode.sql
-- migration-compat: expand
-- +goose Up
-- ADR-20261110151628 step 4: the object_inventory_quiet runtime switch, read
-- where admission and the inventory sweep read bucket usage. apid validates
-- and applies the key; only an applied value counts, so a pending change or
-- a value apid refused reads as off (900 s everywhere), and so does anything
-- but off, shadow or on.
SET LOCAL lock_timeout = '5s';

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION object_inventory_quiet_mode() RETURNS text
    LANGUAGE sql STABLE
    AS $$
SELECT COALESCE((
  SELECT e.effective_value #>> '{}' FROM runtime_config_entries e
  WHERE e.config_key='object_inventory_quiet' AND e.scope='global' AND e.scope_id='' AND e.status='applied'
    AND jsonb_typeof(e.effective_value)='string' AND e.effective_value #>> '{}' IN ('off','shadow','on')
), 'off')
$$;
-- +goose StatementEnd

-- +goose Down
DROP FUNCTION IF EXISTS object_inventory_quiet_mode();
