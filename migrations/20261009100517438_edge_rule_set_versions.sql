-- filename: 20261009100517438_edge_rule_set_versions.sql
--
-- ADR-905 §2: versioned edge-rule sets. Every committed change to an app's
-- edge rules records the app's whole rule set as a new per-app version, so
-- operators can list prior policy, restore it, and send If-Match on edits.
--
-- A deferred constraint trigger takes the snapshot at commit, so every write
-- path (API, manifest, route policy, OpenAPI apply, rollback) is covered and
-- a multi-statement transaction yields one version: the later row triggers in
-- the same commit see an identical digest and skip. The newest 100 versions
-- per app are retained.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS edge_rule_set_versions (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    app_id       uuid NOT NULL,
    version      integer NOT NULL CHECK (version > 0),
    rules        jsonb NOT NULL CHECK (jsonb_typeof(rules) = 'array'),
    rules_sha256 text NOT NULL CHECK (rules_sha256 ~ '^[0-9a-f]{64}$'),
    rule_count   integer NOT NULL CHECK (rule_count >= 0),
    created_at   timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT edge_rule_set_versions_app_version_key UNIQUE (app_id, version)
);

-- The snapshot shape: every column needed to recreate a rule, including its
-- ID and created_at (tie-break order), excluding updated_at (bookkeeping).
CREATE OR REPLACE FUNCTION edge_rule_set_snapshot(target uuid)
RETURNS jsonb AS $$
    SELECT COALESCE(jsonb_agg(to_jsonb(r) ORDER BY r.id), '[]'::jsonb)
    FROM (
        SELECT id, account_id, app_id, match_host, match_path, match_methods,
               match_headers, priority, enabled, kind, action, validate_mode,
               cors_preset_id, manifest_key, name, description, expires_at,
               created_at
        FROM edge_rules
        WHERE app_id = target
    ) r;
$$ LANGUAGE sql STABLE;

CREATE OR REPLACE FUNCTION edge_rules_record_set_version()
RETURNS trigger AS $$
DECLARE
    target   uuid := COALESCE(NEW.app_id, OLD.app_id);
    snapshot jsonb;
    digest   text;
    latest   record;
BEGIN
    -- Serialize version numbering per app across concurrent commits.
    PERFORM pg_advisory_xact_lock(hashtext('edge_rule_set_versions'), hashtext(target::text));
    snapshot := edge_rule_set_snapshot(target);
    digest := encode(sha256(convert_to(snapshot::text, 'UTF8')), 'hex');
    SELECT version, rules_sha256 INTO latest
    FROM edge_rule_set_versions
    WHERE app_id = target
    ORDER BY version DESC
    LIMIT 1;
    IF FOUND AND latest.rules_sha256 = digest THEN
        RETURN NULL;
    END IF;
    INSERT INTO edge_rule_set_versions (app_id, version, rules, rules_sha256, rule_count)
    VALUES (target, COALESCE(latest.version, 0) + 1, snapshot, digest, jsonb_array_length(snapshot));
    DELETE FROM edge_rule_set_versions
    WHERE app_id = target
      AND version <= COALESCE(latest.version, 0) + 1 - 100;
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS edge_rules_record_set_version_trg ON edge_rules;
CREATE CONSTRAINT TRIGGER edge_rules_record_set_version_trg
    AFTER INSERT OR UPDATE OR DELETE ON edge_rules
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION edge_rules_record_set_version();

-- Seed version 1 for apps that already have rules, so the first change after
-- this migration can be rolled back to the pre-migration policy.
INSERT INTO edge_rule_set_versions (app_id, version, rules, rules_sha256, rule_count)
SELECT s.app_id, 1, s.snapshot,
       encode(sha256(convert_to(s.snapshot::text, 'UTF8')), 'hex'),
       jsonb_array_length(s.snapshot)
FROM (
    SELECT DISTINCT app_id, edge_rule_set_snapshot(app_id) AS snapshot
    FROM edge_rules
) s
ON CONFLICT (app_id, version) DO NOTHING;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS edge_rules_record_set_version_trg ON edge_rules;
DROP FUNCTION IF EXISTS edge_rules_record_set_version();
DROP FUNCTION IF EXISTS edge_rule_set_snapshot(uuid);
DROP TABLE IF EXISTS edge_rule_set_versions;
-- +goose StatementEnd
