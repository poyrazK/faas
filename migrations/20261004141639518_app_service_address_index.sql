-- ADR-576: account-scoped private service addresses.
--
-- An app's service address is api.ServiceAddressCIDR (198.19.0.0/16) plus
-- apps.service_address_index. Indices are unique per account, never global.
-- New indices come from a per-account cursor that never moves backwards, so
-- neither a tombstone nor a purged app has its index handed out again until
-- the account has used the whole range. Only then is an index reclaimed from
-- a tombstone older than the 24 h quarantine (api.ServiceAddressReuseQuarantine);
-- a tombstone that loses its index receives a fresh one if it is restored.
--
-- Allocation lives in triggers so every apps insert and restore path, present
-- and future, is covered without each Go call site remembering to do it.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE apps ADD COLUMN IF NOT EXISTS service_address_index integer;

ALTER TABLE apps DROP CONSTRAINT IF EXISTS apps_service_address_index_chk;
ALTER TABLE apps ADD CONSTRAINT apps_service_address_index_chk
    CHECK (service_address_index IS NULL OR service_address_index BETWEEN 1 AND 65534);

-- Backfill in creation order, tombstones included so a restore keeps its
-- address. The offset keeps a partially backfilled account collision-free.
WITH ranked AS (
    SELECT a.id,
           coalesce((SELECT max(x.service_address_index) FROM apps x WHERE x.account_id = a.account_id), 0)
             + row_number() OVER (PARTITION BY a.account_id ORDER BY a.created_at, a.id) AS idx
      FROM apps a
     WHERE a.service_address_index IS NULL
)
UPDATE apps a
   SET service_address_index = r.idx
  FROM ranked r
 WHERE a.id = r.id
   AND r.idx <= 65534;

CREATE UNIQUE INDEX IF NOT EXISTS apps_account_service_address_uniq
    ON apps (account_id, service_address_index)
    WHERE service_address_index IS NOT NULL;

CREATE TABLE IF NOT EXISTS app_service_address_cursors (
    account_id uuid PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,
    last_index integer NOT NULL,
    CONSTRAINT app_service_address_cursors_last_index_chk
        CHECK (last_index BETWEEN 0 AND 65534)
);

INSERT INTO app_service_address_cursors (account_id, last_index)
SELECT account_id, max(service_address_index)
  FROM apps
 WHERE service_address_index IS NOT NULL
 GROUP BY account_id
ON CONFLICT (account_id) DO UPDATE
   SET last_index = greatest(app_service_address_cursors.last_index, EXCLUDED.last_index);

CREATE OR REPLACE FUNCTION allocate_app_service_address_index(p_account_id uuid) RETURNS integer
    LANGUAGE plpgsql
    AS $$
DECLARE
    next_index integer;
BEGIN
    -- The upsert's row lock serializes allocations within one account.
    INSERT INTO app_service_address_cursors AS c (account_id, last_index)
    VALUES (p_account_id, 1)
    ON CONFLICT (account_id) DO UPDATE
       SET last_index = c.last_index + 1
     WHERE c.last_index < 65534
    RETURNING c.last_index INTO next_index;
    IF next_index IS NOT NULL THEN
        RETURN next_index;
    END IF;

    -- The account has used the whole range. Take any index that is free or
    -- held only by a tombstone past the quarantine.
    PERFORM 1 FROM app_service_address_cursors WHERE account_id = p_account_id FOR UPDATE;
    SELECT s.i
      INTO next_index
      FROM generate_series(1, 65534) AS s(i)
     WHERE NOT EXISTS (
            SELECT 1
              FROM apps a
             WHERE a.account_id = p_account_id
               AND a.service_address_index = s.i
               AND NOT (a.status = 'deleted'
                        AND coalesce(a.deleted_at, '-infinity'::timestamptz) < now() - interval '24 hours'))
     LIMIT 1;
    IF next_index IS NULL THEN
        -- Exhausted: the app keeps HTTP service calls through the bridge
        -- address; it just has no private TCP address.
        RETURN NULL;
    END IF;
    UPDATE apps
       SET service_address_index = NULL
     WHERE account_id = p_account_id
       AND service_address_index = next_index;
    RETURN next_index;
END;
$$;

CREATE OR REPLACE FUNCTION assign_app_service_address_index() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF NEW.service_address_index IS NULL AND NEW.status <> 'deleted' THEN
        NEW.service_address_index := allocate_app_service_address_index(NEW.account_id);
    ELSIF TG_OP = 'INSERT' AND NEW.service_address_index IS NOT NULL THEN
        -- An explicit index (data repair, tests) must never be handed out
        -- again by the cursor.
        INSERT INTO app_service_address_cursors AS c (account_id, last_index)
        VALUES (NEW.account_id, NEW.service_address_index)
        ON CONFLICT (account_id) DO UPDATE
           SET last_index = greatest(c.last_index, EXCLUDED.last_index);
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS apps_assign_service_address_index_insert ON apps;
CREATE TRIGGER apps_assign_service_address_index_insert
    BEFORE INSERT ON apps
    FOR EACH ROW EXECUTE FUNCTION assign_app_service_address_index();

-- A restore (status leaves 'deleted') whose index was reclaimed meanwhile.
DROP TRIGGER IF EXISTS apps_assign_service_address_index_update ON apps;
CREATE TRIGGER apps_assign_service_address_index_update
    BEFORE UPDATE OF status, service_address_index ON apps
    FOR EACH ROW
    WHEN (NEW.service_address_index IS NULL AND NEW.status <> 'deleted')
    EXECUTE FUNCTION assign_app_service_address_index();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS apps_assign_service_address_index_update ON apps;
DROP TRIGGER IF EXISTS apps_assign_service_address_index_insert ON apps;
DROP FUNCTION IF EXISTS assign_app_service_address_index();
DROP FUNCTION IF EXISTS allocate_app_service_address_index(uuid);
DROP TABLE IF EXISTS app_service_address_cursors;
DROP INDEX IF EXISTS apps_account_service_address_uniq;
ALTER TABLE apps DROP CONSTRAINT IF EXISTS apps_service_address_index_chk;
ALTER TABLE apps DROP COLUMN IF EXISTS service_address_index;
-- +goose StatementEnd
