-- filename: 20261112093000417_compute_node_db_logins.sql
-- migration-compat: expand
-- ADR-20261110210000 §4, slice 1c-1: per-node database identity.

-- +goose Up
--
-- Every compute node shares its database logins today, so the database
-- cannot tell which node wrote a placement input. compute_node_db_logins
-- maps a login to the one node it belongs to; only the schema owner writes
-- it (runtime-roles.sql revokes faas_runtime's DML). For a mapped login:
--
--   * compute_nodes: only its own row. id, name, region, zone, role, the
--     dial addresses and the certificate binding keep their values once set;
--     the capacity columns (vpcpus, mem_mb, max_concurrency,
--     admission_ceiling_mb, vcpu_budget) may go down but never up: a raise
--     is clamped to the current value and logged as
--     faas_node_capacity_clamped, so vmmd's self-registration keeps working
--     and a raise is made by the control plane.
--   * node_memory_reports, compute_node_heartbeats: only rows of its node.
--
-- An unmapped login behaves exactly as before; nothing changes until a node
-- logs in with a mapped login (slice 1c-2).
SET LOCAL lock_timeout = '5s';

CREATE TABLE IF NOT EXISTS compute_node_db_logins (
    login text PRIMARY KEY CHECK (login ~ '^faas_[a-z0-9_]{1,58}$'),
    node_id uuid NOT NULL REFERENCES compute_nodes(id) ON DELETE CASCADE,
    created_at timestamp with time zone NOT NULL DEFAULT now() CHECK (isfinite(created_at))
);
CREATE INDEX IF NOT EXISTS compute_node_db_logins_node_idx ON compute_node_db_logins (node_id);

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION compute_node_row_scope() RETURNS trigger
    LANGUAGE plpgsql SECURITY DEFINER
    SET search_path = pg_catalog, pg_temp
    AS $function$
DECLARE
    node uuid;
    own_name text;
BEGIN
    EXECUTE format('SELECT node_id FROM %I.compute_node_db_logins WHERE login = $1', TG_TABLE_SCHEMA)
        INTO node USING session_user::text;
    IF node IS NULL THEN
        IF TG_OP = 'DELETE' THEN RETURN OLD; END IF;
        RETURN NEW;
    END IF;
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'a compute node login cannot delete compute node rows'
            USING ERRCODE = '42501', DETAIL = format('login=%s node=%s', session_user, node);
    END IF;
    IF TG_OP = 'INSERT' THEN
        -- vmmd registers with INSERT ... ON CONFLICT (name) DO UPDATE: the
        -- insert is admitted only under the node's own name, where it
        -- becomes an update of its own row.
        EXECUTE format('SELECT name FROM %I.compute_nodes WHERE id = $1', TG_TABLE_SCHEMA)
            INTO own_name USING node;
        IF NEW.name IS DISTINCT FROM own_name THEN
            RAISE EXCEPTION 'a compute node login writes only its own compute node row'
                USING ERRCODE = '42501', DETAIL = format('login=%s node=%s name=%s', session_user, node, NEW.name);
        END IF;
        RETURN NEW;
    END IF;
    IF OLD.id IS DISTINCT FROM node OR NEW.id IS DISTINCT FROM node THEN
        RAISE EXCEPTION 'a compute node login writes only its own compute node row'
            USING ERRCODE = '42501', DETAIL = format('login=%s node=%s row=%s', session_user, node, OLD.id);
    END IF;
    IF NEW.name IS DISTINCT FROM OLD.name
        OR (OLD.region IS NOT NULL AND NEW.region IS DISTINCT FROM OLD.region)
        OR (OLD.zone IS NOT NULL AND NEW.zone IS DISTINCT FROM OLD.zone)
        OR (OLD.role IS NOT NULL AND NEW.role IS DISTINCT FROM OLD.role)
        OR NEW.target_url IS DISTINCT FROM OLD.target_url
        OR (OLD.schedd_target_url IS NOT NULL AND NEW.schedd_target_url IS DISTINCT FROM OLD.schedd_target_url)
        OR (OLD.gateway_target_url IS NOT NULL AND NEW.gateway_target_url IS DISTINCT FROM OLD.gateway_target_url)
        OR (OLD.cert_fingerprint IS NOT NULL AND NEW.cert_fingerprint IS DISTINCT FROM OLD.cert_fingerprint)
        OR (OLD.host_certificate IS NOT NULL AND NEW.host_certificate IS DISTINCT FROM OLD.host_certificate) THEN
        RAISE EXCEPTION 'a compute node''s identity, placement labels and addresses are changed by the control plane'
            USING ERRCODE = '42501', DETAIL = format('login=%s node=%s', session_user, node);
    END IF;
    IF NEW.vpcpus > OLD.vpcpus OR NEW.mem_mb > OLD.mem_mb OR NEW.max_concurrency > OLD.max_concurrency
        OR NEW.admission_ceiling_mb > OLD.admission_ceiling_mb OR NEW.vcpu_budget > OLD.vcpu_budget THEN
        RAISE LOG 'faas_node_capacity_clamped node=% login=% vpcpus=%->% mem_mb=%->% max_concurrency=%->% admission_ceiling_mb=%->% vcpu_budget=%->%',
            node, session_user, OLD.vpcpus, NEW.vpcpus, OLD.mem_mb, NEW.mem_mb, OLD.max_concurrency, NEW.max_concurrency,
            OLD.admission_ceiling_mb, NEW.admission_ceiling_mb, OLD.vcpu_budget, NEW.vcpu_budget;
        NEW.vpcpus := LEAST(NEW.vpcpus, OLD.vpcpus);
        NEW.mem_mb := LEAST(NEW.mem_mb, OLD.mem_mb);
        NEW.max_concurrency := LEAST(NEW.max_concurrency, OLD.max_concurrency);
        NEW.admission_ceiling_mb := LEAST(NEW.admission_ceiling_mb, OLD.admission_ceiling_mb);
        NEW.vcpu_budget := LEAST(NEW.vcpu_budget, OLD.vcpu_budget);
    END IF;
    RETURN NEW;
END;
$function$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION compute_node_report_scope() RETURNS trigger
    LANGUAGE plpgsql SECURITY DEFINER
    SET search_path = pg_catalog, pg_temp
    AS $function$
DECLARE
    node uuid;
BEGIN
    EXECUTE format('SELECT node_id FROM %I.compute_node_db_logins WHERE login = $1', TG_TABLE_SCHEMA)
        INTO node USING session_user::text;
    IF node IS NOT NULL AND (
        (TG_OP IN ('UPDATE', 'DELETE') AND OLD.node_id IS DISTINCT FROM node)
        OR (TG_OP IN ('INSERT', 'UPDATE') AND NEW.node_id IS DISTINCT FROM node)) THEN
        RAISE EXCEPTION 'a compute node login writes only its own node''s reports'
            USING ERRCODE = '42501', DETAIL = format('login=%s node=%s table=%s', session_user, node, TG_TABLE_NAME);
    END IF;
    IF TG_OP = 'DELETE' THEN RETURN OLD; END IF;
    RETURN NEW;
END;
$function$;
-- +goose StatementEnd

DROP TRIGGER IF EXISTS compute_node_row_scope ON compute_nodes;
CREATE TRIGGER compute_node_row_scope BEFORE INSERT OR UPDATE OR DELETE
    ON compute_nodes FOR EACH ROW EXECUTE FUNCTION compute_node_row_scope();
DROP TRIGGER IF EXISTS compute_node_report_scope ON node_memory_reports;
CREATE TRIGGER compute_node_report_scope BEFORE INSERT OR UPDATE OR DELETE
    ON node_memory_reports FOR EACH ROW EXECUTE FUNCTION compute_node_report_scope();
DROP TRIGGER IF EXISTS compute_node_report_scope ON compute_node_heartbeats;
CREATE TRIGGER compute_node_report_scope BEFORE INSERT OR UPDATE OR DELETE
    ON compute_node_heartbeats FOR EACH ROW EXECUTE FUNCTION compute_node_report_scope();

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TRIGGER IF EXISTS compute_node_report_scope ON compute_node_heartbeats;
DROP TRIGGER IF EXISTS compute_node_report_scope ON node_memory_reports;
DROP TRIGGER IF EXISTS compute_node_row_scope ON compute_nodes;
DROP FUNCTION IF EXISTS compute_node_report_scope();
DROP FUNCTION IF EXISTS compute_node_row_scope();
DROP TABLE IF EXISTS compute_node_db_logins;
