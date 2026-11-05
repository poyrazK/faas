-- filename: 20261027215348829_edge_rule_match_host_scope.sql
-- ADR-20261027163626 rollout step 2. edge_rules.match_host_scope holds the
-- part of a rule's match_host that every hostname it can match must end with.
-- The per-host traffic binding guard uses it to find the route rules that can
-- match a changed hostname. Nothing reads it yet.
--   exact host            -> the host itself, lower-cased
--   pattern               -> the labels after the first dot that follows the
--                            pattern's last wildcard ('promo-*.apps.example'
--                            -> 'apps.example'), or '' when no full label
--                            follows it ('*', 'api.*')
--   "@app"                -> NULL: it covers only the rule's own app's hosts
-- Wildcards are path.Match's * ? [ ] \ plus the SQL LIKE % _ that legacy
-- selectors keep (ADR-570), so the scope is never narrower than the match.
-- A trigger fills the column on every write, including the previous
-- release's, so it is correct before and after the keyset backfill below.
-- migration-compat: expand
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '500ms';

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION edge_rule_match_host_scope(match_host text) RETURNS text
LANGUAGE sql IMMUTABLE STRICT PARALLEL SAFE AS $$
  SELECT CASE
    WHEN host = '@app' THEN NULL
    WHEN host !~ '[][*?%_\\]' THEN host
    WHEN strpos(tail, '.') = 0 THEN ''
    ELSE substr(tail, strpos(tail, '.') + 1)
  END
  FROM (SELECT host, regexp_replace(host, '^.*[][*?%_\\]', '') AS tail
          FROM (SELECT lower(btrim(match_host)) AS host) normalized) parts
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION edge_rules_set_match_host_scope() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    NEW.match_host_scope := edge_rule_match_host_scope(NEW.match_host);
    RETURN NEW;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
-- edge_rules: nullable column, no default, no rewrite
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   ALTER TABLE edge_rules ADD COLUMN IF NOT EXISTS match_host_scope text;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: edge_rules: add match_host_scope' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd

-- +goose StatementBegin
-- edge_rules: the trigger also recomputes a direct write to the scope
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   CREATE OR REPLACE TRIGGER edge_rules_match_host_scope
   BEFORE INSERT OR UPDATE OF match_host, match_host_scope ON edge_rules
   FOR EACH ROW EXECUTE FUNCTION edge_rules_set_match_host_scope();
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: edge_rules: trigger edge_rules_match_host_scope' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd

-- +goose StatementBegin
-- edge_rules: keyset backfill, one committed batch at a time
DO $backfill$
DECLARE
  last_id uuid := '00000000-0000-0000-0000-000000000000';
  ids uuid[];
BEGIN
  LOOP
    PERFORM set_config('lock_timeout', '5s', true);
    WITH batch AS (
      SELECT id FROM edge_rules WHERE id > last_id ORDER BY id LIMIT 1000
    ), scoped AS (
      UPDATE edge_rules SET match_host_scope = edge_rule_match_host_scope(edge_rules.match_host)
        FROM batch WHERE edge_rules.id = batch.id
         AND edge_rules.match_host_scope IS DISTINCT FROM edge_rule_match_host_scope(edge_rules.match_host)
    )
    SELECT array_agg(id ORDER BY id) INTO ids FROM batch;
    EXIT WHEN ids IS NULL;
    last_id := ids[cardinality(ids)];
    COMMIT;
  END LOOP;
END
$backfill$;
-- +goose StatementEnd

-- Route candidates by scope (ADR-20261027163626 decision 3).
CREATE INDEX CONCURRENTLY IF NOT EXISTS edge_rules_route_match_host_scope_idx
    ON edge_rules (match_host_scope) WHERE enabled AND kind = 'route';

-- +goose Down
SELECT 1;
