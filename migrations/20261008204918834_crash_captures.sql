-- filename: 20261008204918834_crash_captures.sql

-- +goose Up
-- +goose StatementBegin
-- ADR-733 crash snapshots. apid owns crash_snapshot_settings (customer
-- intent). A request row comes from gatewayd-internal (HTTP 5xx) or apid
-- (manual); schedd owns every later transition and deletes the capture
-- files at expiry. Captures are never `snapshots` rows, so no wake can
-- restore one; they are opened only as ADR-732 forks.
CREATE TABLE IF NOT EXISTS crash_snapshot_settings (
    app_id uuid PRIMARY KEY REFERENCES apps(id) ON DELETE CASCADE,
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    enabled boolean NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS crash_captures (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    deployment_id uuid NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,
    instance_id uuid NOT NULL,
    trigger text NOT NULL,
    status_code integer,
    route text NOT NULL DEFAULT '',
    status text NOT NULL DEFAULT 'requested',
    storage_key text,
    vmstate_storage_key text,
    fc_version text,
    mem_bytes bigint,
    failure_code text,
    failure_message text,
    requested_at timestamptz NOT NULL,
    captured_at timestamptz,
    finished_at timestamptz,
    expires_at timestamptz,
    updated_at timestamptz NOT NULL,
    CONSTRAINT crash_captures_trigger_chk CHECK (trigger IN ('http_5xx', 'manual')),
    CONSTRAINT crash_captures_status_code_chk CHECK (
        (trigger = 'http_5xx') = (status_code IS NOT NULL)
        AND (status_code IS NULL OR status_code BETWEEN 500 AND 599)
    ),
    CONSTRAINT crash_captures_route_chk CHECK (octet_length(route) <= 512),
    CONSTRAINT crash_captures_status_chk CHECK (
        status IN ('requested', 'capturing', 'ready', 'failed', 'expired')
    ),
    CONSTRAINT crash_captures_ready_shape_chk CHECK (
        status NOT IN ('ready', 'expired')
        OR (storage_key IS NOT NULL AND vmstate_storage_key IS NOT NULL
            AND fc_version IS NOT NULL AND mem_bytes IS NOT NULL
            AND captured_at IS NOT NULL AND expires_at IS NOT NULL)
    ),
    CONSTRAINT crash_captures_keys_chk CHECK (
        (storage_key IS NULL OR octet_length(storage_key) BETWEEN 1 AND 1024)
        AND (vmstate_storage_key IS NULL OR octet_length(vmstate_storage_key) BETWEEN 1 AND 1024)
        AND (fc_version IS NULL OR octet_length(fc_version) BETWEEN 1 AND 64)
        AND (mem_bytes IS NULL OR mem_bytes >= 0)
    ),
    CONSTRAINT crash_captures_failure_shape_chk CHECK (
        (failure_code IS NULL) = (failure_message IS NULL)
        AND (status = 'failed') = (failure_code IS NOT NULL)
        AND (failure_code IS NULL OR octet_length(failure_code) BETWEEN 1 AND 64)
        AND (failure_message IS NULL OR octet_length(failure_message) <= 4096)
    ),
    CONSTRAINT crash_captures_finished_chk CHECK (
        (status IN ('failed', 'expired')) = (finished_at IS NOT NULL)
    ),
    CONSTRAINT crash_captures_order_chk CHECK (
        updated_at >= requested_at
        AND (captured_at IS NULL OR captured_at >= requested_at)
        AND (finished_at IS NULL OR finished_at >= requested_at)
        AND (expires_at IS NULL OR captured_at IS NULL OR expires_at > captured_at)
    )
);

CREATE INDEX IF NOT EXISTS crash_captures_account_app_idx
    ON crash_captures (account_id, app_id, requested_at DESC, id DESC);
CREATE UNIQUE INDEX IF NOT EXISTS crash_captures_one_in_flight_uniq
    ON crash_captures (app_id)
    WHERE status IN ('requested', 'capturing');
CREATE INDEX IF NOT EXISTS crash_captures_claim_idx
    ON crash_captures (requested_at, id)
    WHERE status = 'requested';
CREATE INDEX IF NOT EXISTS crash_captures_expiry_idx
    ON crash_captures (expires_at, id)
    WHERE status = 'ready';

CREATE OR REPLACE FUNCTION enforce_crash_capture_status_transition()
RETURNS trigger
LANGUAGE plpgsql
AS $function$
BEGIN
    IF OLD.status IN ('failed', 'expired') THEN
        IF NEW IS DISTINCT FROM OLD THEN
            RAISE EXCEPTION 'terminal crash capture % is immutable', OLD.id USING ERRCODE = '23514';
        END IF;
        RETURN NEW;
    END IF;
    IF NEW.account_id IS DISTINCT FROM OLD.account_id
        OR NEW.app_id IS DISTINCT FROM OLD.app_id
        OR NEW.deployment_id IS DISTINCT FROM OLD.deployment_id
        OR NEW.instance_id IS DISTINCT FROM OLD.instance_id
        OR NEW.trigger IS DISTINCT FROM OLD.trigger
        OR NEW.status_code IS DISTINCT FROM OLD.status_code
        OR NEW.route IS DISTINCT FROM OLD.route
        OR NEW.requested_at IS DISTINCT FROM OLD.requested_at THEN
        RAISE EXCEPTION 'crash capture % request is immutable', OLD.id USING ERRCODE = '23514';
    END IF;
    IF NEW.status = OLD.status THEN
        RETURN NEW;
    END IF;
    IF NOT (
        (OLD.status = 'requested' AND NEW.status IN ('capturing', 'failed'))
        OR (OLD.status = 'capturing' AND NEW.status IN ('ready', 'failed'))
        OR (OLD.status = 'ready' AND NEW.status = 'expired')
    ) THEN
        RAISE EXCEPTION 'invalid crash capture % transition from % to %',
            OLD.id, OLD.status, NEW.status USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END
$function$;

DROP TRIGGER IF EXISTS crash_captures_status_transition ON crash_captures;
CREATE TRIGGER crash_captures_status_transition
    BEFORE UPDATE ON crash_captures
    FOR EACH ROW EXECUTE FUNCTION enforce_crash_capture_status_transition();

ALTER TABLE app_forks
    ADD COLUMN IF NOT EXISTS crash_capture_id uuid REFERENCES crash_captures(id) ON DELETE SET NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE app_forks DROP COLUMN IF EXISTS crash_capture_id;
DROP TRIGGER IF EXISTS crash_captures_status_transition ON crash_captures;
DROP FUNCTION IF EXISTS enforce_crash_capture_status_transition();
DROP TABLE IF EXISTS crash_captures;
DROP TABLE IF EXISTS crash_snapshot_settings;
-- +goose StatementEnd
