-- +goose Up
-- +goose StatementBegin
-- Capture ownership at the control-plane runtime publication boundary. No FK
-- to instances, apps, deployments, or nodes: the mapping survives teardown.
-- Account erasure still removes it. Existing active instances start at this
-- migration's time; earlier ownership is deliberately not fabricated.
CREATE TABLE outbound_flow_ip_leases (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    node_id uuid NOT NULL,
    host_ip inet NOT NULL,
    instance_id uuid NOT NULL,
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    org_id uuid,
    app_id uuid,
    deployment_id uuid,
    active_from timestamptz NOT NULL,
    active_until timestamptz,
    recorded_at timestamptz NOT NULL DEFAULT now(),
    CHECK (active_until IS NULL OR active_until >= active_from)
);
CREATE UNIQUE INDEX outbound_flow_ip_leases_open_instance_idx
    ON outbound_flow_ip_leases (instance_id) WHERE active_until IS NULL;
CREATE INDEX outbound_flow_ip_leases_node_ip_time_idx
    ON outbound_flow_ip_leases (node_id, host_ip, active_from DESC);
CREATE INDEX outbound_flow_ip_leases_retention_idx
    ON outbound_flow_ip_leases (active_until, id) WHERE active_until IS NOT NULL;

CREATE FUNCTION capture_outbound_flow_ip_lease()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    changed_at timestamptz := clock_timestamp();
    old_active boolean := false;
    new_active boolean := false;
    identity_changed boolean := false;
    owner_account uuid;
    lease_start timestamptz;
BEGIN
    IF TG_OP = 'DELETE' THEN
        UPDATE outbound_flow_ip_leases
           SET active_until = greatest(active_from, changed_at)
         WHERE instance_id = OLD.id AND active_until IS NULL;
        RETURN OLD;
    END IF;

    new_active := NEW.host_ip IS NOT NULL AND
        NEW.state IN ('cold_booting', 'running', 'draining', 'snapshotting', 'migrating', 'warm');
    IF TG_OP = 'UPDATE' THEN
        old_active := OLD.host_ip IS NOT NULL AND
            OLD.state IN ('cold_booting', 'running', 'draining', 'snapshotting', 'migrating', 'warm');
        identity_changed := OLD.node_id IS DISTINCT FROM NEW.node_id OR
            OLD.host_ip IS DISTINCT FROM NEW.host_ip OR
            OLD.started_at IS DISTINCT FROM NEW.started_at OR
            OLD.app_id IS DISTINCT FROM NEW.app_id OR
            OLD.deployment_id IS DISTINCT FROM NEW.deployment_id OR
            OLD.job_id IS DISTINCT FROM NEW.job_id;
        IF old_active AND (NOT new_active OR identity_changed) THEN
            UPDATE outbound_flow_ip_leases
               SET active_until = greatest(active_from, changed_at)
             WHERE instance_id = OLD.id AND active_until IS NULL;
        END IF;
    END IF;

    -- A state-only STOPPED -> COLD_BOOTING transition may still carry the
    -- previous boot's host_ip. Wait for runtime publication to open a lease.
    IF new_active AND (TG_OP = 'INSERT' OR identity_changed) THEN
        lease_start := changed_at;
        IF TG_OP = 'INSERT' THEN
            lease_start := coalesce(NEW.started_at, changed_at);
        ELSIF OLD.started_at IS DISTINCT FROM NEW.started_at THEN
            lease_start := coalesce(NEW.started_at, changed_at);
        END IF;
        IF NEW.app_id IS NOT NULL THEN
            SELECT account_id INTO owner_account FROM apps WHERE id = NEW.app_id;
        ELSIF NEW.job_id IS NOT NULL THEN
            SELECT account_id INTO owner_account FROM jobs WHERE id = NEW.job_id;
        END IF;
        IF owner_account IS NOT NULL THEN
            INSERT INTO outbound_flow_ip_leases
                (node_id, host_ip, instance_id, account_id, org_id, app_id,
                 deployment_id, active_from)
            VALUES
                (NEW.node_id, NEW.host_ip, NEW.id, owner_account, NEW.org_id,
                 NEW.app_id, NEW.deployment_id, lease_start)
            ON CONFLICT (instance_id) WHERE active_until IS NULL DO NOTHING;
        END IF;
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER instances_outbound_flow_ip_lease_trigger
AFTER INSERT OR UPDATE OF host_ip, node_id, started_at, state, app_id,
                          deployment_id, job_id OR DELETE ON instances
FOR EACH ROW EXECUTE FUNCTION capture_outbound_flow_ip_lease();

INSERT INTO outbound_flow_ip_leases
    (node_id, host_ip, instance_id, account_id, org_id, app_id,
     deployment_id, active_from)
SELECT i.node_id, i.host_ip, i.id, coalesce(a.account_id, j.account_id),
       i.org_id, i.app_id, i.deployment_id, now()
  FROM instances i
  LEFT JOIN apps a ON a.id = i.app_id
  LEFT JOIN jobs j ON j.id = i.job_id
 WHERE i.host_ip IS NOT NULL
   AND i.state IN ('cold_booting', 'running', 'draining', 'snapshotting', 'migrating', 'warm')
   AND coalesce(a.account_id, j.account_id) IS NOT NULL
ON CONFLICT (instance_id) WHERE active_until IS NULL DO NOTHING;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS instances_outbound_flow_ip_lease_trigger ON instances;
DROP FUNCTION IF EXISTS capture_outbound_flow_ip_lease();
DROP TABLE outbound_flow_ip_leases;
-- +goose StatementEnd
