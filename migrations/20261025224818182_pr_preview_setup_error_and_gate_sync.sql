-- filename: 20261025224818182_pr_preview_setup_error_and_gate_sync.sql
-- migration-compat: expand
--
-- ADR-20261026090000: a PR preview set records why githubd could not set up
-- its head (setup_error), a production app's access and egress gates follow
-- onto its live previews, and a torn-down PR preview is synced to GitHub once
-- more. Older writers never set setup_error, and the gate trigger only copies
-- what they already copy at preview creation.
-- The CHECK is added NOT VALID and validated in its own transaction; the
-- backfill commits every batch. Every step is re-runnable.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';

ALTER TABLE pr_preview_sets ADD COLUMN IF NOT EXISTS setup_error text;

-- +goose StatementBegin
DO $setup_error$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'pr_preview_sets'::regclass AND conname = 'pr_preview_sets_setup_error_chk') THEN
    ALTER TABLE pr_preview_sets ADD CONSTRAINT pr_preview_sets_setup_error_chk
      CHECK (setup_error IS NULL OR char_length(setup_error) BETWEEN 1 AND 1000) NOT VALID;
  END IF;
END
$setup_error$;
-- +goose StatementEnd

ALTER TABLE pr_preview_sets VALIDATE CONSTRAINT pr_preview_sets_setup_error_chk;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION sync_preview_access_gates() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    preview_id uuid;
BEGIN
    -- A preview runs unreviewed code on a guessable host; it keeps the
    -- access and egress gates of the production app it previews.
    FOR preview_id IN
        UPDATE apps
        SET require_authn = NEW.require_authn,
            public_auth_mode = NEW.public_auth_mode,
            public_auth_basic = NEW.public_auth_basic,
            public_auth_ip_allowlist = NEW.public_auth_ip_allowlist,
            consumer_auth_mode = NEW.consumer_auth_mode,
            require_signed = NEW.require_signed,
            egress_allowlist = NEW.egress_allowlist,
            visibility = NEW.visibility
        WHERE account_id = NEW.account_id
          AND preview_of_slug = NEW.slug
          AND status <> 'deleted'
        RETURNING id
    LOOP
        PERFORM pg_notify('app_changed', preview_id::text);
    END LOOP;
    RETURN NULL;
END;
$$;
-- +goose StatementEnd

DROP TRIGGER IF EXISTS apps_sync_preview_access_gates ON apps;

CREATE TRIGGER apps_sync_preview_access_gates
    AFTER UPDATE OF require_authn, public_auth_mode, public_auth_basic, public_auth_ip_allowlist,
        consumer_auth_mode, require_signed, egress_allowlist, visibility ON apps
    FOR EACH ROW
    WHEN (NEW.preview_of_slug IS NULL AND NEW.status <> 'deleted' AND (
        OLD.require_authn IS DISTINCT FROM NEW.require_authn OR
        OLD.public_auth_mode IS DISTINCT FROM NEW.public_auth_mode OR
        OLD.public_auth_basic IS DISTINCT FROM NEW.public_auth_basic OR
        OLD.public_auth_ip_allowlist IS DISTINCT FROM NEW.public_auth_ip_allowlist OR
        OLD.consumer_auth_mode IS DISTINCT FROM NEW.consumer_auth_mode OR
        OLD.require_signed IS DISTINCT FROM NEW.require_signed OR
        OLD.egress_allowlist IS DISTINCT FROM NEW.egress_allowlist OR
        OLD.visibility IS DISTINCT FROM NEW.visibility))
    EXECUTE FUNCTION sync_preview_access_gates();

-- A torn-down PR preview's latest deployment is synced once more, so githubd
-- marks its GitHub deployment inactive and its PR comment torn down.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION enqueue_preview_teardown_github_update() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    latest uuid;
BEGIN
    SELECT id INTO latest FROM deployments
    WHERE app_id = NEW.id AND kind = 'preview'
    ORDER BY created_at DESC, id DESC
    LIMIT 1;
    IF latest IS NOT NULL THEN
        INSERT INTO github_check_updates (deployment_id)
        VALUES (latest)
        ON CONFLICT (deployment_id) DO UPDATE SET
          generation = github_check_updates.generation + 1,
          status = 'pending',
          attempts = 0,
          next_attempt_at = now(),
          last_error = '',
          processed_at = NULL,
          updated_at = now();
    END IF;
    RETURN NULL;
END;
$$;
-- +goose StatementEnd

DROP TRIGGER IF EXISTS apps_preview_teardown_github_update ON apps;

CREATE TRIGGER apps_preview_teardown_github_update
    AFTER UPDATE OF status ON apps
    FOR EACH ROW
    WHEN (OLD.status IS DISTINCT FROM 'deleted' AND NEW.status = 'deleted'
        AND NEW.preview_of_slug IS NOT NULL AND coalesce(NEW.preview_pr_number, 0) > 0)
    EXECUTE FUNCTION enqueue_preview_teardown_github_update();

-- Previews created before the gates were copied (and before this trigger)
-- take their production app's gates now, a batch per transaction.
-- +goose StatementBegin
DO $backfill$
DECLARE
    changed uuid[];
    preview_id uuid;
BEGIN
    LOOP
        WITH batch AS (
            SELECT preview.id, parent.require_authn, parent.public_auth_mode, parent.public_auth_basic,
                   parent.public_auth_ip_allowlist, parent.consumer_auth_mode, parent.require_signed,
                   parent.egress_allowlist, parent.visibility
            FROM apps preview
            JOIN apps parent ON parent.account_id = preview.account_id
                AND parent.slug = preview.preview_of_slug
                AND parent.preview_of_slug IS NULL
                AND parent.status <> 'deleted'
            WHERE preview.preview_of_slug IS NOT NULL
              AND preview.status <> 'deleted'
              AND (preview.require_authn, preview.public_auth_mode, preview.public_auth_basic,
                   preview.public_auth_ip_allowlist, preview.consumer_auth_mode, preview.require_signed,
                   preview.egress_allowlist, preview.visibility)
                  IS DISTINCT FROM
                  (parent.require_authn, parent.public_auth_mode, parent.public_auth_basic,
                   parent.public_auth_ip_allowlist, parent.consumer_auth_mode, parent.require_signed,
                   parent.egress_allowlist, parent.visibility)
            LIMIT 500
        ), updated AS (
            UPDATE apps
            SET require_authn = batch.require_authn,
                public_auth_mode = batch.public_auth_mode,
                public_auth_basic = batch.public_auth_basic,
                public_auth_ip_allowlist = batch.public_auth_ip_allowlist,
                consumer_auth_mode = batch.consumer_auth_mode,
                require_signed = batch.require_signed,
                egress_allowlist = batch.egress_allowlist,
                visibility = batch.visibility
            FROM batch
            WHERE apps.id = batch.id
            RETURNING apps.id
        )
        SELECT coalesce(array_agg(id), '{}') INTO changed FROM updated;
        EXIT WHEN cardinality(changed) = 0;
        FOREACH preview_id IN ARRAY changed LOOP
            PERFORM pg_notify('app_changed', preview_id::text);
        END LOOP;
        COMMIT;
    END LOOP;
END
$backfill$;
-- +goose StatementEnd

RESET lock_timeout;

-- +goose Down
SET lock_timeout = '5s';
DROP TRIGGER IF EXISTS apps_preview_teardown_github_update ON apps;
DROP FUNCTION IF EXISTS enqueue_preview_teardown_github_update();
DROP TRIGGER IF EXISTS apps_sync_preview_access_gates ON apps;
DROP FUNCTION IF EXISTS sync_preview_access_gates();
ALTER TABLE pr_preview_sets DROP CONSTRAINT IF EXISTS pr_preview_sets_setup_error_chk;
ALTER TABLE pr_preview_sets DROP COLUMN IF EXISTS setup_error;
RESET lock_timeout;
