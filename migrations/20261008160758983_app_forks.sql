-- filename: 20261008160758983_app_forks.sql

-- +goose Up
-- +goose StatementBegin
-- ADR-732: production fork intent. apid writes one row per request; schedd
-- claims queued rows under a lease, restores the app's newest capture into a
-- quarantined non-serving instance, and expires the row at expires_at. The
-- public API stays behind FAAS_APP_FORKS until the scheduler path lands.
CREATE TABLE IF NOT EXISTS app_forks (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    deployment_id uuid NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,
    requested_by text NOT NULL,
    status text NOT NULL DEFAULT 'queued',
    ttl_seconds integer NOT NULL,
    expires_at timestamptz NOT NULL,
    snapshot_id uuid,
    instance_id uuid,
    lease_token uuid,
    lease_owner text,
    lease_expires_at timestamptz,
    cancel_requested_at timestamptz,
    failure_code text,
    failure_message text,
    started_at timestamptz,
    finished_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT app_forks_requested_by_chk CHECK (
        octet_length(requested_by) BETWEEN 1 AND 256
    ),
    CONSTRAINT app_forks_status_chk CHECK (
        status IN ('queued', 'restoring', 'running', 'expired', 'cancelled', 'failed')
    ),
    CONSTRAINT app_forks_ttl_chk CHECK (ttl_seconds BETWEEN 60 AND 86400),
    CONSTRAINT app_forks_expires_chk CHECK (
        expires_at = created_at + make_interval(secs => ttl_seconds)
    ),
    CONSTRAINT app_forks_lease_shape_chk CHECK (
        (lease_token IS NULL AND lease_owner IS NULL AND lease_expires_at IS NULL)
        OR
        (lease_token IS NOT NULL AND lease_owner IS NOT NULL AND lease_expires_at IS NOT NULL)
    ),
    CONSTRAINT app_forks_lease_status_chk CHECK (
        (status IN ('restoring', 'running')) = (lease_token IS NOT NULL)
    ),
    CONSTRAINT app_forks_lease_owner_chk CHECK (
        lease_owner IS NULL OR octet_length(lease_owner) BETWEEN 1 AND 256
    ),
    CONSTRAINT app_forks_instance_chk CHECK (
        (status = 'running') <= (instance_id IS NOT NULL AND snapshot_id IS NOT NULL)
    ),
    CONSTRAINT app_forks_failure_shape_chk CHECK (
        (failure_code IS NULL) = (failure_message IS NULL)
        AND (failure_code IS NULL OR status = 'failed')
        AND (status <> 'failed' OR failure_code IS NOT NULL)
        AND (failure_code IS NULL OR octet_length(failure_code) BETWEEN 1 AND 64)
        AND (failure_message IS NULL OR octet_length(failure_message) <= 4096)
    ),
    CONSTRAINT app_forks_timestamps_chk CHECK (
        (status IN ('queued', 'restoring') AND started_at IS NULL AND finished_at IS NULL)
        OR
        (status = 'running' AND started_at IS NOT NULL AND finished_at IS NULL)
        OR
        (status IN ('expired', 'cancelled', 'failed') AND finished_at IS NOT NULL)
    ),
    CONSTRAINT app_forks_lifecycle_order_chk CHECK (
        updated_at >= created_at
        AND (started_at IS NULL OR started_at >= created_at)
        AND (finished_at IS NULL OR finished_at >= created_at)
        AND (started_at IS NULL OR finished_at IS NULL OR finished_at >= started_at)
        AND (cancel_requested_at IS NULL OR cancel_requested_at >= created_at)
        AND (lease_expires_at IS NULL OR lease_expires_at > created_at)
    )
);

CREATE INDEX IF NOT EXISTS app_forks_account_app_created_idx
    ON app_forks (account_id, app_id, created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS app_forks_claim_idx
    ON app_forks (created_at, id)
    WHERE status = 'queued' AND cancel_requested_at IS NULL;
CREATE INDEX IF NOT EXISTS app_forks_active_expiry_idx
    ON app_forks (expires_at, id)
    WHERE status IN ('queued', 'restoring', 'running');

CREATE OR REPLACE FUNCTION enforce_app_fork_status_transition()
RETURNS trigger
LANGUAGE plpgsql
AS $function$
BEGIN
    IF OLD.status IN ('expired', 'cancelled', 'failed') THEN
        IF NEW IS DISTINCT FROM OLD THEN
            RAISE EXCEPTION 'terminal app fork % is immutable', OLD.id USING ERRCODE = '23514';
        END IF;
        RETURN NEW;
    END IF;

    IF NEW.account_id IS DISTINCT FROM OLD.account_id
        OR NEW.app_id IS DISTINCT FROM OLD.app_id
        OR NEW.deployment_id IS DISTINCT FROM OLD.deployment_id
        OR NEW.requested_by IS DISTINCT FROM OLD.requested_by
        OR NEW.ttl_seconds IS DISTINCT FROM OLD.ttl_seconds
        OR NEW.expires_at IS DISTINCT FROM OLD.expires_at
        OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
        RAISE EXCEPTION 'app fork % intent is immutable', OLD.id USING ERRCODE = '23514';
    END IF;

    IF NEW.status = OLD.status THEN
        RETURN NEW;
    END IF;

    IF NOT (
        (OLD.status = 'queued' AND NEW.status IN ('restoring', 'expired', 'cancelled'))
        OR
        (OLD.status = 'restoring' AND NEW.status IN ('queued', 'running', 'expired',
                                                     'cancelled', 'failed'))
        OR
        (OLD.status = 'running' AND NEW.status IN ('expired', 'cancelled', 'failed'))
    ) THEN
        RAISE EXCEPTION 'invalid app fork % transition from % to %',
            OLD.id, OLD.status, NEW.status USING ERRCODE = '23514';
    END IF;

    RETURN NEW;
END
$function$;

DROP TRIGGER IF EXISTS app_forks_status_transition ON app_forks;
CREATE TRIGGER app_forks_status_transition
    BEFORE UPDATE ON app_forks
    FOR EACH ROW EXECUTE FUNCTION enforce_app_fork_status_transition();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS app_forks_status_transition ON app_forks;
DROP FUNCTION IF EXISTS enforce_app_fork_status_transition();
DROP TABLE IF EXISTS app_forks;
-- +goose StatementEnd
