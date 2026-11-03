-- filename: 20261102193333680_trigger_when_unchanged_columns.sql
-- migration-compat: expand
-- Each table's triggers are swapped in their own DO block (one transaction,
-- one table lock), so no table is ever without its trigger. Re-runnable after
-- a partial run. Down restores the original triggers and function.
-- +goose NO TRANSACTION

-- +goose Up
-- Two trigger functions return without effect on an UPDATE that leaves their
-- columns unchanged, yet ran in full on every update:
--
--   capture_binding_promotion_revision (23 tables) builds jsonb of the whole
--   OLD and NEW rows, then returns when the watched columns it was given are
--   equal. On instances it ran for every runtime publish, readiness stamp and
--   capture barrier; on trigger_consumer_health for every poll.
--   guard_clone_checkpoint_instance_admission (instances) reads the clone
--   fence catalogue twice, then only acts when id, app, deployment, node,
--   wake, mode or state changed.
--
-- Each revision trigger now skips the call with a WHEN clause on exactly its
-- watched columns (SQL equality and the function's jsonb comparison agree for
-- every watched column type). WHEN cannot read OLD/NEW on INSERT or DELETE,
-- so a multi-event trigger splits into its INSERT/DELETE part (same name,
-- same arguments) and an UPDATE part named binding_promotion_revision_update,
-- which sorts directly after it, so firing order is unchanged. The revision
-- function is not modified. The clone guard returns before its reads when its
-- columns are unchanged; its trigger stays as it is.
SET lock_timeout = '5s';

-- +goose StatementBegin
DO $$
BEGIN
    DROP TRIGGER IF EXISTS binding_promotion_revision ON accounts;
    CREATE TRIGGER binding_promotion_revision AFTER UPDATE ON accounts
        FOR EACH ROW WHEN ((OLD.id, OLD.plan) IS DISTINCT FROM (NEW.id, NEW.plan))
        EXECUTE FUNCTION capture_binding_promotion_revision('id,plan');
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    DROP TRIGGER IF EXISTS binding_promotion_revision ON app_envs;
    DROP TRIGGER IF EXISTS binding_promotion_revision_update ON app_envs;
    CREATE TRIGGER binding_promotion_revision AFTER INSERT OR DELETE ON app_envs
        FOR EACH ROW EXECUTE FUNCTION capture_binding_promotion_revision('account_id,app_id,scope,key,value');
    CREATE TRIGGER binding_promotion_revision_update AFTER UPDATE ON app_envs
        FOR EACH ROW WHEN ((OLD.account_id, OLD.app_id, OLD.scope, OLD.key, OLD.value) IS DISTINCT FROM (NEW.account_id, NEW.app_id, NEW.scope, NEW.key, NEW.value))
        EXECUTE FUNCTION capture_binding_promotion_revision('account_id,app_id,scope,key,value');
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    DROP TRIGGER IF EXISTS binding_promotion_revision ON app_runtime_config_changes;
    DROP TRIGGER IF EXISTS binding_promotion_revision_update ON app_runtime_config_changes;
    CREATE TRIGGER binding_promotion_revision AFTER INSERT OR DELETE ON app_runtime_config_changes
        FOR EACH ROW EXECUTE FUNCTION capture_binding_promotion_revision('app_id,changed_at');
    CREATE TRIGGER binding_promotion_revision_update AFTER UPDATE ON app_runtime_config_changes
        FOR EACH ROW WHEN ((OLD.app_id, OLD.changed_at) IS DISTINCT FROM (NEW.app_id, NEW.changed_at))
        EXECUTE FUNCTION capture_binding_promotion_revision('app_id,changed_at');
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    DROP TRIGGER IF EXISTS binding_promotion_revision ON app_secret_runtime_processes;
    DROP TRIGGER IF EXISTS binding_promotion_revision_update ON app_secret_runtime_processes;
    CREATE TRIGGER binding_promotion_revision AFTER INSERT OR DELETE ON app_secret_runtime_processes
        FOR EACH ROW EXECUTE FUNCTION capture_binding_promotion_revision('app_id,instance_id,workload_name,generation,active,started_at');
    CREATE TRIGGER binding_promotion_revision_update AFTER UPDATE ON app_secret_runtime_processes
        FOR EACH ROW WHEN ((OLD.app_id, OLD.instance_id, OLD.workload_name, OLD.generation, OLD.active, OLD.started_at) IS DISTINCT FROM (NEW.app_id, NEW.instance_id, NEW.workload_name, NEW.generation, NEW.active, NEW.started_at))
        EXECUTE FUNCTION capture_binding_promotion_revision('app_id,instance_id,workload_name,generation,active,started_at');
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    DROP TRIGGER IF EXISTS binding_promotion_revision ON app_secret_runtime_reload_observations;
    DROP TRIGGER IF EXISTS binding_promotion_revision_update ON app_secret_runtime_reload_observations;
    CREATE TRIGGER binding_promotion_revision AFTER INSERT OR DELETE ON app_secret_runtime_reload_observations
        FOR EACH ROW EXECUTE FUNCTION capture_binding_promotion_revision('app_id,scope,key,instance_id,workload_name,secret_version,projection,signal,observed_at,error_code,application_ack_version,application_ack_status,application_ack_at,application_ack_error_code,application_ack_generation');
    CREATE TRIGGER binding_promotion_revision_update AFTER UPDATE ON app_secret_runtime_reload_observations
        FOR EACH ROW WHEN ((OLD.app_id, OLD.scope, OLD.key, OLD.instance_id, OLD.workload_name, OLD.secret_version, OLD.projection, OLD.signal, OLD.observed_at, OLD.error_code, OLD.application_ack_version, OLD.application_ack_status, OLD.application_ack_at, OLD.application_ack_error_code, OLD.application_ack_generation) IS DISTINCT FROM (NEW.app_id, NEW.scope, NEW.key, NEW.instance_id, NEW.workload_name, NEW.secret_version, NEW.projection, NEW.signal, NEW.observed_at, NEW.error_code, NEW.application_ack_version, NEW.application_ack_status, NEW.application_ack_at, NEW.application_ack_error_code, NEW.application_ack_generation))
        EXECUTE FUNCTION capture_binding_promotion_revision('app_id,scope,key,instance_id,workload_name,secret_version,projection,signal,observed_at,error_code,application_ack_version,application_ack_status,application_ack_at,application_ack_error_code,application_ack_generation');
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    DROP TRIGGER IF EXISTS binding_promotion_revision ON app_secrets;
    DROP TRIGGER IF EXISTS binding_promotion_revision_update ON app_secrets;
    CREATE TRIGGER binding_promotion_revision AFTER INSERT OR DELETE ON app_secrets
        FOR EACH ROW EXECUTE FUNCTION capture_binding_promotion_revision('account_id,app_id,scope,key,ciphertext,kid,value_hash,managed_postgres_binding_id,managed_credential_ref,managed_credential_generation,managed_object_storage_credential_id,delivery_version,secret_class');
    CREATE TRIGGER binding_promotion_revision_update AFTER UPDATE ON app_secrets
        FOR EACH ROW WHEN ((OLD.account_id, OLD.app_id, OLD.scope, OLD.key, OLD.ciphertext, OLD.kid, OLD.value_hash, OLD.managed_postgres_binding_id, OLD.managed_credential_ref, OLD.managed_credential_generation, OLD.managed_object_storage_credential_id, OLD.delivery_version, OLD.secret_class) IS DISTINCT FROM (NEW.account_id, NEW.app_id, NEW.scope, NEW.key, NEW.ciphertext, NEW.kid, NEW.value_hash, NEW.managed_postgres_binding_id, NEW.managed_credential_ref, NEW.managed_credential_generation, NEW.managed_object_storage_credential_id, NEW.delivery_version, NEW.secret_class))
        EXECUTE FUNCTION capture_binding_promotion_revision('account_id,app_id,scope,key,ciphertext,kid,value_hash,managed_postgres_binding_id,managed_credential_ref,managed_credential_generation,managed_object_storage_credential_id,delivery_version,secret_class');
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    DROP TRIGGER IF EXISTS binding_promotion_revision ON app_tasks;
    DROP TRIGGER IF EXISTS binding_promotion_revision_update ON app_tasks;
    CREATE TRIGGER binding_promotion_revision AFTER INSERT OR DELETE ON app_tasks
        FOR EACH ROW EXECUTE FUNCTION capture_binding_promotion_revision('id,app_id,account_id,deployment_id,deployment_scope,binding_verification,status,created_at,finished_at,stdout_tail,output_truncated,exit_code');
    CREATE TRIGGER binding_promotion_revision_update AFTER UPDATE ON app_tasks
        FOR EACH ROW WHEN ((OLD.id, OLD.app_id, OLD.account_id, OLD.deployment_id, OLD.deployment_scope, OLD.binding_verification, OLD.status, OLD.created_at, OLD.finished_at, OLD.stdout_tail, OLD.output_truncated, OLD.exit_code) IS DISTINCT FROM (NEW.id, NEW.app_id, NEW.account_id, NEW.deployment_id, NEW.deployment_scope, NEW.binding_verification, NEW.status, NEW.created_at, NEW.finished_at, NEW.stdout_tail, NEW.output_truncated, NEW.exit_code))
        EXECUTE FUNCTION capture_binding_promotion_revision('id,app_id,account_id,deployment_id,deployment_scope,binding_verification,status,created_at,finished_at,stdout_tail,output_truncated,exit_code');
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    DROP TRIGGER IF EXISTS binding_promotion_revision ON apps;
    DROP TRIGGER IF EXISTS binding_promotion_revision_update ON apps;
    CREATE TRIGGER binding_promotion_revision AFTER INSERT OR DELETE ON apps
        FOR EACH ROW EXECUTE FUNCTION capture_binding_promotion_revision('id,account_id,slug,status,manifest,type');
    CREATE TRIGGER binding_promotion_revision_update AFTER UPDATE ON apps
        FOR EACH ROW WHEN ((OLD.id, OLD.account_id, OLD.slug, OLD.status, OLD.manifest, OLD.type) IS DISTINCT FROM (NEW.id, NEW.account_id, NEW.slug, NEW.status, NEW.manifest, NEW.type))
        EXECUTE FUNCTION capture_binding_promotion_revision('id,account_id,slug,status,manifest,type');
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    DROP TRIGGER IF EXISTS binding_promotion_revision ON deployments;
    DROP TRIGGER IF EXISTS binding_promotion_revision_update ON deployments;
    CREATE TRIGGER binding_promotion_revision AFTER INSERT OR DELETE ON deployments
        FOR EACH ROW EXECUTE FUNCTION capture_binding_promotion_revision('id,app_id,status,scope,rootfs_key,image_digest,traffic_percent,canary_total_steps,rollout_state,override_env_secrets,sidecars,secret_reload_signal');
    CREATE TRIGGER binding_promotion_revision_update AFTER UPDATE ON deployments
        FOR EACH ROW WHEN ((OLD.id, OLD.app_id, OLD.status, OLD.scope, OLD.rootfs_key, OLD.image_digest, OLD.traffic_percent, OLD.canary_total_steps, OLD.rollout_state, OLD.override_env_secrets, OLD.sidecars, OLD.secret_reload_signal) IS DISTINCT FROM (NEW.id, NEW.app_id, NEW.status, NEW.scope, NEW.rootfs_key, NEW.image_digest, NEW.traffic_percent, NEW.canary_total_steps, NEW.rollout_state, NEW.override_env_secrets, NEW.sidecars, NEW.secret_reload_signal))
        EXECUTE FUNCTION capture_binding_promotion_revision('id,app_id,status,scope,rootfs_key,image_digest,traffic_percent,canary_total_steps,rollout_state,override_env_secrets,sidecars,secret_reload_signal');
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    DROP TRIGGER IF EXISTS binding_promotion_revision ON instances;
    DROP TRIGGER IF EXISTS binding_promotion_revision_update ON instances;
    CREATE TRIGGER binding_promotion_revision AFTER INSERT OR DELETE ON instances
        FOR EACH ROW EXECUTE FUNCTION capture_binding_promotion_revision('id,app_id,deployment_id,kind,mode,state,started_at');
    CREATE TRIGGER binding_promotion_revision_update AFTER UPDATE ON instances
        FOR EACH ROW WHEN ((OLD.id, OLD.app_id, OLD.deployment_id, OLD.kind, OLD.mode, OLD.state, OLD.started_at) IS DISTINCT FROM (NEW.id, NEW.app_id, NEW.deployment_id, NEW.kind, NEW.mode, NEW.state, NEW.started_at))
        EXECUTE FUNCTION capture_binding_promotion_revision('id,app_id,deployment_id,kind,mode,state,started_at');
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    DROP TRIGGER IF EXISTS binding_promotion_revision ON managed_postgres_bindings;
    DROP TRIGGER IF EXISTS binding_promotion_revision_update ON managed_postgres_bindings;
    CREATE TRIGGER binding_promotion_revision AFTER INSERT OR DELETE ON managed_postgres_bindings
        FOR EACH ROW EXECUTE FUNCTION capture_binding_promotion_revision('id,account_id,app_id,database_id,scope,environment_key,access,state,credential_generation,credential_ref,rotation_previous_generation,rotation_wake_id,rotation_cleanup_ready');
    CREATE TRIGGER binding_promotion_revision_update AFTER UPDATE ON managed_postgres_bindings
        FOR EACH ROW WHEN ((OLD.id, OLD.account_id, OLD.app_id, OLD.database_id, OLD.scope, OLD.environment_key, OLD.access, OLD.state, OLD.credential_generation, OLD.credential_ref, OLD.rotation_previous_generation, OLD.rotation_wake_id, OLD.rotation_cleanup_ready) IS DISTINCT FROM (NEW.id, NEW.account_id, NEW.app_id, NEW.database_id, NEW.scope, NEW.environment_key, NEW.access, NEW.state, NEW.credential_generation, NEW.credential_ref, NEW.rotation_previous_generation, NEW.rotation_wake_id, NEW.rotation_cleanup_ready))
        EXECUTE FUNCTION capture_binding_promotion_revision('id,account_id,app_id,database_id,scope,environment_key,access,state,credential_generation,credential_ref,rotation_previous_generation,rotation_wake_id,rotation_cleanup_ready');
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    DROP TRIGGER IF EXISTS binding_promotion_revision ON managed_postgres_databases;
    DROP TRIGGER IF EXISTS binding_promotion_revision_update ON managed_postgres_databases;
    CREATE TRIGGER binding_promotion_revision AFTER INSERT OR DELETE ON managed_postgres_databases
        FOR EACH ROW EXECUTE FUNCTION capture_binding_promotion_revision('id,account_id,name,state');
    CREATE TRIGGER binding_promotion_revision_update AFTER UPDATE ON managed_postgres_databases
        FOR EACH ROW WHEN ((OLD.id, OLD.account_id, OLD.name, OLD.state) IS DISTINCT FROM (NEW.id, NEW.account_id, NEW.name, NEW.state))
        EXECUTE FUNCTION capture_binding_promotion_revision('id,account_id,name,state');
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    DROP TRIGGER IF EXISTS binding_promotion_revision ON notification_outbox;
    DROP TRIGGER IF EXISTS binding_promotion_revision_update ON notification_outbox;
    CREATE TRIGGER binding_promotion_revision AFTER INSERT OR DELETE ON notification_outbox
        FOR EACH ROW EXECUTE FUNCTION capture_binding_promotion_revision('channel,payload,state,attempts,created_at,delivered_at,last_error');
    CREATE TRIGGER binding_promotion_revision_update AFTER UPDATE ON notification_outbox
        FOR EACH ROW WHEN ((OLD.channel, OLD.payload, OLD.state, OLD.attempts, OLD.created_at, OLD.delivered_at, OLD.last_error) IS DISTINCT FROM (NEW.channel, NEW.payload, NEW.state, NEW.attempts, NEW.created_at, NEW.delivered_at, NEW.last_error))
        EXECUTE FUNCTION capture_binding_promotion_revision('channel,payload,state,attempts,created_at,delivered_at,last_error');
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    DROP TRIGGER IF EXISTS binding_promotion_revision ON object_buckets;
    DROP TRIGGER IF EXISTS binding_promotion_revision_update ON object_buckets;
    CREATE TRIGGER binding_promotion_revision AFTER INSERT OR DELETE ON object_buckets
        FOR EACH ROW EXECUTE FUNCTION capture_binding_promotion_revision('id,account_id,app_id,name,state');
    CREATE TRIGGER binding_promotion_revision_update AFTER UPDATE ON object_buckets
        FOR EACH ROW WHEN ((OLD.id, OLD.account_id, OLD.app_id, OLD.name, OLD.state) IS DISTINCT FROM (NEW.id, NEW.account_id, NEW.app_id, NEW.name, NEW.state))
        EXECUTE FUNCTION capture_binding_promotion_revision('id,account_id,app_id,name,state');
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    DROP TRIGGER IF EXISTS binding_promotion_revision ON object_storage_s3_credentials;
    DROP TRIGGER IF EXISTS binding_promotion_revision_update ON object_storage_s3_credentials;
    CREATE TRIGGER binding_promotion_revision AFTER INSERT OR DELETE ON object_storage_s3_credentials
        FOR EACH ROW EXECUTE FUNCTION capture_binding_promotion_revision('id,account_id,bucket_id,permission,status,managed_app_id,managed_scope,managed_prefix,rotation_parent_id,rotation_wake_id,secret_sealed,kid,created_at');
    CREATE TRIGGER binding_promotion_revision_update AFTER UPDATE ON object_storage_s3_credentials
        FOR EACH ROW WHEN ((OLD.id, OLD.account_id, OLD.bucket_id, OLD.permission, OLD.status, OLD.managed_app_id, OLD.managed_scope, OLD.managed_prefix, OLD.rotation_parent_id, OLD.rotation_wake_id, OLD.secret_sealed, OLD.kid, OLD.created_at) IS DISTINCT FROM (NEW.id, NEW.account_id, NEW.bucket_id, NEW.permission, NEW.status, NEW.managed_app_id, NEW.managed_scope, NEW.managed_prefix, NEW.rotation_parent_id, NEW.rotation_wake_id, NEW.secret_sealed, NEW.kid, NEW.created_at))
        EXECUTE FUNCTION capture_binding_promotion_revision('id,account_id,bucket_id,permission,status,managed_app_id,managed_scope,managed_prefix,rotation_parent_id,rotation_wake_id,secret_sealed,kid,created_at');
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    DROP TRIGGER IF EXISTS binding_promotion_revision ON outbound_app_bindings;
    DROP TRIGGER IF EXISTS binding_promotion_revision_update ON outbound_app_bindings;
    CREATE TRIGGER binding_promotion_revision AFTER INSERT OR DELETE ON outbound_app_bindings
        FOR EACH ROW EXECUTE FUNCTION capture_binding_promotion_revision('app_id,account_id,integration_id,allowed_methods,allowed_path_prefixes,daily_request_limit');
    CREATE TRIGGER binding_promotion_revision_update AFTER UPDATE ON outbound_app_bindings
        FOR EACH ROW WHEN ((OLD.app_id, OLD.account_id, OLD.integration_id, OLD.allowed_methods, OLD.allowed_path_prefixes, OLD.daily_request_limit) IS DISTINCT FROM (NEW.app_id, NEW.account_id, NEW.integration_id, NEW.allowed_methods, NEW.allowed_path_prefixes, NEW.daily_request_limit))
        EXECUTE FUNCTION capture_binding_promotion_revision('app_id,account_id,integration_id,allowed_methods,allowed_path_prefixes,daily_request_limit');
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    DROP TRIGGER IF EXISTS binding_promotion_revision ON outbound_integration_credentials;
    DROP TRIGGER IF EXISTS binding_promotion_revision_update ON outbound_integration_credentials;
    CREATE TRIGGER binding_promotion_revision AFTER INSERT OR DELETE ON outbound_integration_credentials
        FOR EACH ROW EXECUTE FUNCTION capture_binding_promotion_revision('integration_id,account_id,authorization_sealed,updated_at');
    CREATE TRIGGER binding_promotion_revision_update AFTER UPDATE ON outbound_integration_credentials
        FOR EACH ROW WHEN ((OLD.integration_id, OLD.account_id, OLD.authorization_sealed, OLD.updated_at) IS DISTINCT FROM (NEW.integration_id, NEW.account_id, NEW.authorization_sealed, NEW.updated_at))
        EXECUTE FUNCTION capture_binding_promotion_revision('integration_id,account_id,authorization_sealed,updated_at');
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    DROP TRIGGER IF EXISTS binding_promotion_revision ON outbound_integration_probe_policies;
    DROP TRIGGER IF EXISTS binding_promotion_revision_update ON outbound_integration_probe_policies;
    CREATE TRIGGER binding_promotion_revision AFTER INSERT OR DELETE ON outbound_integration_probe_policies
        FOR EACH ROW EXECUTE FUNCTION capture_binding_promotion_revision('integration_id,account_id,method,path,expected_status');
    CREATE TRIGGER binding_promotion_revision_update AFTER UPDATE ON outbound_integration_probe_policies
        FOR EACH ROW WHEN ((OLD.integration_id, OLD.account_id, OLD.method, OLD.path, OLD.expected_status) IS DISTINCT FROM (NEW.integration_id, NEW.account_id, NEW.method, NEW.path, NEW.expected_status))
        EXECUTE FUNCTION capture_binding_promotion_revision('integration_id,account_id,method,path,expected_status');
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    DROP TRIGGER IF EXISTS binding_promotion_revision ON outbound_integrations;
    DROP TRIGGER IF EXISTS binding_promotion_revision_update ON outbound_integrations;
    CREATE TRIGGER binding_promotion_revision AFTER INSERT OR DELETE ON outbound_integrations
        FOR EACH ROW EXECUTE FUNCTION capture_binding_promotion_revision('id,account_id,name,enabled,allowed_methods,allowed_path_prefixes,credential_source,origin,provider_auth_mode,owner_kind,token_hash,daily_request_limit,rate_per_second,burst,max_in_flight,request_timeout_ms,max_retries,response_cache_ttl_seconds,circuit_breaker_failure_threshold,circuit_breaker_open_seconds,retry_budget_per_minute');
    CREATE TRIGGER binding_promotion_revision_update AFTER UPDATE ON outbound_integrations
        FOR EACH ROW WHEN ((OLD.id, OLD.account_id, OLD.name, OLD.enabled, OLD.allowed_methods, OLD.allowed_path_prefixes, OLD.credential_source, OLD.origin, OLD.provider_auth_mode, OLD.owner_kind, OLD.token_hash, OLD.daily_request_limit, OLD.rate_per_second, OLD.burst, OLD.max_in_flight, OLD.request_timeout_ms, OLD.max_retries, OLD.response_cache_ttl_seconds, OLD.circuit_breaker_failure_threshold, OLD.circuit_breaker_open_seconds, OLD.retry_budget_per_minute) IS DISTINCT FROM (NEW.id, NEW.account_id, NEW.name, NEW.enabled, NEW.allowed_methods, NEW.allowed_path_prefixes, NEW.credential_source, NEW.origin, NEW.provider_auth_mode, NEW.owner_kind, NEW.token_hash, NEW.daily_request_limit, NEW.rate_per_second, NEW.burst, NEW.max_in_flight, NEW.request_timeout_ms, NEW.max_retries, NEW.response_cache_ttl_seconds, NEW.circuit_breaker_failure_threshold, NEW.circuit_breaker_open_seconds, NEW.retry_budget_per_minute))
        EXECUTE FUNCTION capture_binding_promotion_revision('id,account_id,name,enabled,allowed_methods,allowed_path_prefixes,credential_source,origin,provider_auth_mode,owner_kind,token_hash,daily_request_limit,rate_per_second,burst,max_in_flight,request_timeout_ms,max_retries,response_cache_ttl_seconds,circuit_breaker_failure_threshold,circuit_breaker_open_seconds,retry_budget_per_minute');
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    DROP TRIGGER IF EXISTS binding_promotion_revision ON queue_binding_canaries;
    DROP TRIGGER IF EXISTS binding_promotion_revision_update ON queue_binding_canaries;
    CREATE TRIGGER binding_promotion_revision AFTER INSERT OR DELETE ON queue_binding_canaries
        FOR EACH ROW EXECUTE FUNCTION capture_binding_promotion_revision('id,app_id,binding_id,trigger_id,deployment_id,snapshot,published_at,expires_at,cancelled,completed_deployment_id,completed_attempt,completed_generation');
    CREATE TRIGGER binding_promotion_revision_update AFTER UPDATE ON queue_binding_canaries
        FOR EACH ROW WHEN ((OLD.id, OLD.app_id, OLD.binding_id, OLD.trigger_id, OLD.deployment_id, OLD.snapshot, OLD.published_at, OLD.expires_at, OLD.cancelled, OLD.completed_deployment_id, OLD.completed_attempt, OLD.completed_generation) IS DISTINCT FROM (NEW.id, NEW.app_id, NEW.binding_id, NEW.trigger_id, NEW.deployment_id, NEW.snapshot, NEW.published_at, NEW.expires_at, NEW.cancelled, NEW.completed_deployment_id, NEW.completed_attempt, NEW.completed_generation))
        EXECUTE FUNCTION capture_binding_promotion_revision('id,app_id,binding_id,trigger_id,deployment_id,snapshot,published_at,expires_at,cancelled,completed_deployment_id,completed_attempt,completed_generation');
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    DROP TRIGGER IF EXISTS binding_promotion_revision ON queue_bindings;
    DROP TRIGGER IF EXISTS binding_promotion_revision_update ON queue_bindings;
    CREATE TRIGGER binding_promotion_revision AFTER INSERT OR DELETE ON queue_bindings
        FOR EACH ROW EXECUTE FUNCTION capture_binding_promotion_revision('id,account_id,app_id,name,queue_name,mode,enabled,retired_at');
    CREATE TRIGGER binding_promotion_revision_update AFTER UPDATE ON queue_bindings
        FOR EACH ROW WHEN ((OLD.id, OLD.account_id, OLD.app_id, OLD.name, OLD.queue_name, OLD.mode, OLD.enabled, OLD.retired_at) IS DISTINCT FROM (NEW.id, NEW.account_id, NEW.app_id, NEW.name, NEW.queue_name, NEW.mode, NEW.enabled, NEW.retired_at))
        EXECUTE FUNCTION capture_binding_promotion_revision('id,account_id,app_id,name,queue_name,mode,enabled,retired_at');
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    DROP TRIGGER IF EXISTS binding_promotion_revision ON trigger_consumer_health;
    DROP TRIGGER IF EXISTS binding_promotion_revision_update ON trigger_consumer_health;
    CREATE TRIGGER binding_promotion_revision AFTER INSERT OR DELETE ON trigger_consumer_health
        FOR EACH ROW EXECUTE FUNCTION capture_binding_promotion_revision('trigger_id,last_error_at');
    CREATE TRIGGER binding_promotion_revision_update AFTER UPDATE ON trigger_consumer_health
        FOR EACH ROW WHEN ((OLD.trigger_id, OLD.last_error_at) IS DISTINCT FROM (NEW.trigger_id, NEW.last_error_at))
        EXECUTE FUNCTION capture_binding_promotion_revision('trigger_id,last_error_at');
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    DROP TRIGGER IF EXISTS binding_promotion_revision ON triggers;
    DROP TRIGGER IF EXISTS binding_promotion_revision_update ON triggers;
    CREATE TRIGGER binding_promotion_revision AFTER INSERT OR DELETE ON triggers
        FOR EACH ROW EXECUTE FUNCTION capture_binding_promotion_revision('id,app_id,kind,source,config,enabled,created_at');
    CREATE TRIGGER binding_promotion_revision_update AFTER UPDATE ON triggers
        FOR EACH ROW WHEN ((OLD.id, OLD.app_id, OLD.kind, OLD.source, OLD.config, OLD.enabled, OLD.created_at) IS DISTINCT FROM (NEW.id, NEW.app_id, NEW.kind, NEW.source, NEW.config, NEW.enabled, NEW.created_at))
        EXECUTE FUNCTION capture_binding_promotion_revision('id,app_id,kind,source,config,enabled,created_at');
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_clone_checkpoint_instance_admission() RETURNS trigger
    LANGUAGE plpgsql
    AS $fn$
DECLARE fenced boolean;
BEGIN
 -- An update that keeps these columns can be neither fenced nor in need of
 -- the snapshot guard: skip the two fence catalogue reads.
 IF TG_OP='UPDATE' AND (NEW.id,NEW.app_id,NEW.deployment_id,NEW.node_id,NEW.wake_id,NEW.mode,NEW.state)
    IS NOT DISTINCT FROM (OLD.id,OLD.app_id,OLD.deployment_id,OLD.node_id,OLD.wake_id,OLD.mode,OLD.state) THEN
  RETURN NEW;
 END IF;
 IF (TG_OP='INSERT' OR (NEW.id,NEW.app_id,NEW.deployment_id,NEW.node_id,NEW.wake_id,NEW.mode,NEW.state) IS DISTINCT FROM (OLD.id,OLD.app_id,OLD.deployment_id,OLD.node_id,OLD.wake_id,OLD.mode,OLD.state))
  AND current_setting('transaction_isolation')<>'read committed' THEN
  -- A snapshot older than the newest retirement request fails here (40001),
  -- so the fence check below never reads a stale retirement catalogue.
  PERFORM clone_checkpoint_admission_snapshot_guard();
 END IF;
 SELECT clone_checkpoint_deployment_fenced(NEW.deployment_id,d.reason) INTO fenced
 FROM (SELECT 1) x LEFT JOIN deployments d ON d.id=NEW.deployment_id;
 IF TG_OP='UPDATE' AND NOT fenced THEN
  SELECT clone_checkpoint_deployment_fenced(OLD.deployment_id,d.reason) INTO fenced
  FROM (SELECT 1) x LEFT JOIN deployments d ON d.id=OLD.deployment_id;
 END IF;
 IF fenced THEN
  IF TG_OP='INSERT' THEN
   RAISE EXCEPTION 'clone instance request permanently fenced' USING ERRCODE='23514',CONSTRAINT='clone_checkpoint_scheduler_request_fenced';
  ELSIF (NEW.id,NEW.app_id,NEW.deployment_id,NEW.node_id,NEW.wake_id,NEW.mode) IS DISTINCT FROM (OLD.id,OLD.app_id,OLD.deployment_id,OLD.node_id,OLD.wake_id,OLD.mode)
    OR (NEW.state IS DISTINCT FROM OLD.state AND NEW.state NOT IN ('draining','parked','stopped','failed','evicting_account_deleting')) THEN
   RAISE EXCEPTION 'clone instance restart permanently fenced' USING ERRCODE='23514',CONSTRAINT='clone_checkpoint_scheduler_request_fenced';
  END IF;
 END IF;
 RETURN NEW;
END $fn$;
-- +goose StatementEnd

-- +goose Down
SET lock_timeout = '5s';

-- +goose StatementBegin
DO $$
BEGIN
    DROP TRIGGER IF EXISTS binding_promotion_revision ON accounts;
    CREATE TRIGGER binding_promotion_revision AFTER UPDATE ON accounts
        FOR EACH ROW EXECUTE FUNCTION capture_binding_promotion_revision('id,plan');
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    DROP TRIGGER IF EXISTS binding_promotion_revision_update ON app_envs;
    DROP TRIGGER IF EXISTS binding_promotion_revision ON app_envs;
    CREATE TRIGGER binding_promotion_revision AFTER INSERT OR DELETE OR UPDATE ON app_envs
        FOR EACH ROW EXECUTE FUNCTION capture_binding_promotion_revision('account_id,app_id,scope,key,value');
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    DROP TRIGGER IF EXISTS binding_promotion_revision_update ON app_runtime_config_changes;
    DROP TRIGGER IF EXISTS binding_promotion_revision ON app_runtime_config_changes;
    CREATE TRIGGER binding_promotion_revision AFTER INSERT OR DELETE OR UPDATE ON app_runtime_config_changes
        FOR EACH ROW EXECUTE FUNCTION capture_binding_promotion_revision('app_id,changed_at');
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    DROP TRIGGER IF EXISTS binding_promotion_revision_update ON app_secret_runtime_processes;
    DROP TRIGGER IF EXISTS binding_promotion_revision ON app_secret_runtime_processes;
    CREATE TRIGGER binding_promotion_revision AFTER INSERT OR DELETE OR UPDATE ON app_secret_runtime_processes
        FOR EACH ROW EXECUTE FUNCTION capture_binding_promotion_revision('app_id,instance_id,workload_name,generation,active,started_at');
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    DROP TRIGGER IF EXISTS binding_promotion_revision_update ON app_secret_runtime_reload_observations;
    DROP TRIGGER IF EXISTS binding_promotion_revision ON app_secret_runtime_reload_observations;
    CREATE TRIGGER binding_promotion_revision AFTER INSERT OR DELETE OR UPDATE ON app_secret_runtime_reload_observations
        FOR EACH ROW EXECUTE FUNCTION capture_binding_promotion_revision('app_id,scope,key,instance_id,workload_name,secret_version,projection,signal,observed_at,error_code,application_ack_version,application_ack_status,application_ack_at,application_ack_error_code,application_ack_generation');
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    DROP TRIGGER IF EXISTS binding_promotion_revision_update ON app_secrets;
    DROP TRIGGER IF EXISTS binding_promotion_revision ON app_secrets;
    CREATE TRIGGER binding_promotion_revision AFTER INSERT OR DELETE OR UPDATE ON app_secrets
        FOR EACH ROW EXECUTE FUNCTION capture_binding_promotion_revision('account_id,app_id,scope,key,ciphertext,kid,value_hash,managed_postgres_binding_id,managed_credential_ref,managed_credential_generation,managed_object_storage_credential_id,delivery_version,secret_class');
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    DROP TRIGGER IF EXISTS binding_promotion_revision_update ON app_tasks;
    DROP TRIGGER IF EXISTS binding_promotion_revision ON app_tasks;
    CREATE TRIGGER binding_promotion_revision AFTER INSERT OR DELETE OR UPDATE ON app_tasks
        FOR EACH ROW EXECUTE FUNCTION capture_binding_promotion_revision('id,app_id,account_id,deployment_id,deployment_scope,binding_verification,status,created_at,finished_at,stdout_tail,output_truncated,exit_code');
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    DROP TRIGGER IF EXISTS binding_promotion_revision_update ON apps;
    DROP TRIGGER IF EXISTS binding_promotion_revision ON apps;
    CREATE TRIGGER binding_promotion_revision AFTER INSERT OR DELETE OR UPDATE ON apps
        FOR EACH ROW EXECUTE FUNCTION capture_binding_promotion_revision('id,account_id,slug,status,manifest,type');
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    DROP TRIGGER IF EXISTS binding_promotion_revision_update ON deployments;
    DROP TRIGGER IF EXISTS binding_promotion_revision ON deployments;
    CREATE TRIGGER binding_promotion_revision AFTER INSERT OR DELETE OR UPDATE ON deployments
        FOR EACH ROW EXECUTE FUNCTION capture_binding_promotion_revision('id,app_id,status,scope,rootfs_key,image_digest,traffic_percent,canary_total_steps,rollout_state,override_env_secrets,sidecars,secret_reload_signal');
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    DROP TRIGGER IF EXISTS binding_promotion_revision_update ON instances;
    DROP TRIGGER IF EXISTS binding_promotion_revision ON instances;
    CREATE TRIGGER binding_promotion_revision AFTER INSERT OR DELETE OR UPDATE ON instances
        FOR EACH ROW EXECUTE FUNCTION capture_binding_promotion_revision('id,app_id,deployment_id,kind,mode,state,started_at');
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    DROP TRIGGER IF EXISTS binding_promotion_revision_update ON managed_postgres_bindings;
    DROP TRIGGER IF EXISTS binding_promotion_revision ON managed_postgres_bindings;
    CREATE TRIGGER binding_promotion_revision AFTER INSERT OR DELETE OR UPDATE ON managed_postgres_bindings
        FOR EACH ROW EXECUTE FUNCTION capture_binding_promotion_revision('id,account_id,app_id,database_id,scope,environment_key,access,state,credential_generation,credential_ref,rotation_previous_generation,rotation_wake_id,rotation_cleanup_ready');
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    DROP TRIGGER IF EXISTS binding_promotion_revision_update ON managed_postgres_databases;
    DROP TRIGGER IF EXISTS binding_promotion_revision ON managed_postgres_databases;
    CREATE TRIGGER binding_promotion_revision AFTER INSERT OR DELETE OR UPDATE ON managed_postgres_databases
        FOR EACH ROW EXECUTE FUNCTION capture_binding_promotion_revision('id,account_id,name,state');
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    DROP TRIGGER IF EXISTS binding_promotion_revision_update ON notification_outbox;
    DROP TRIGGER IF EXISTS binding_promotion_revision ON notification_outbox;
    CREATE TRIGGER binding_promotion_revision AFTER INSERT OR DELETE OR UPDATE ON notification_outbox
        FOR EACH ROW EXECUTE FUNCTION capture_binding_promotion_revision('channel,payload,state,attempts,created_at,delivered_at,last_error');
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    DROP TRIGGER IF EXISTS binding_promotion_revision_update ON object_buckets;
    DROP TRIGGER IF EXISTS binding_promotion_revision ON object_buckets;
    CREATE TRIGGER binding_promotion_revision AFTER INSERT OR DELETE OR UPDATE ON object_buckets
        FOR EACH ROW EXECUTE FUNCTION capture_binding_promotion_revision('id,account_id,app_id,name,state');
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    DROP TRIGGER IF EXISTS binding_promotion_revision_update ON object_storage_s3_credentials;
    DROP TRIGGER IF EXISTS binding_promotion_revision ON object_storage_s3_credentials;
    CREATE TRIGGER binding_promotion_revision AFTER INSERT OR DELETE OR UPDATE ON object_storage_s3_credentials
        FOR EACH ROW EXECUTE FUNCTION capture_binding_promotion_revision('id,account_id,bucket_id,permission,status,managed_app_id,managed_scope,managed_prefix,rotation_parent_id,rotation_wake_id,secret_sealed,kid,created_at');
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    DROP TRIGGER IF EXISTS binding_promotion_revision_update ON outbound_app_bindings;
    DROP TRIGGER IF EXISTS binding_promotion_revision ON outbound_app_bindings;
    CREATE TRIGGER binding_promotion_revision AFTER INSERT OR DELETE OR UPDATE ON outbound_app_bindings
        FOR EACH ROW EXECUTE FUNCTION capture_binding_promotion_revision('app_id,account_id,integration_id,allowed_methods,allowed_path_prefixes,daily_request_limit');
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    DROP TRIGGER IF EXISTS binding_promotion_revision_update ON outbound_integration_credentials;
    DROP TRIGGER IF EXISTS binding_promotion_revision ON outbound_integration_credentials;
    CREATE TRIGGER binding_promotion_revision AFTER INSERT OR DELETE OR UPDATE ON outbound_integration_credentials
        FOR EACH ROW EXECUTE FUNCTION capture_binding_promotion_revision('integration_id,account_id,authorization_sealed,updated_at');
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    DROP TRIGGER IF EXISTS binding_promotion_revision_update ON outbound_integration_probe_policies;
    DROP TRIGGER IF EXISTS binding_promotion_revision ON outbound_integration_probe_policies;
    CREATE TRIGGER binding_promotion_revision AFTER INSERT OR DELETE OR UPDATE ON outbound_integration_probe_policies
        FOR EACH ROW EXECUTE FUNCTION capture_binding_promotion_revision('integration_id,account_id,method,path,expected_status');
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    DROP TRIGGER IF EXISTS binding_promotion_revision_update ON outbound_integrations;
    DROP TRIGGER IF EXISTS binding_promotion_revision ON outbound_integrations;
    CREATE TRIGGER binding_promotion_revision AFTER INSERT OR DELETE OR UPDATE ON outbound_integrations
        FOR EACH ROW EXECUTE FUNCTION capture_binding_promotion_revision('id,account_id,name,enabled,allowed_methods,allowed_path_prefixes,credential_source,origin,provider_auth_mode,owner_kind,token_hash,daily_request_limit,rate_per_second,burst,max_in_flight,request_timeout_ms,max_retries,response_cache_ttl_seconds,circuit_breaker_failure_threshold,circuit_breaker_open_seconds,retry_budget_per_minute');
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    DROP TRIGGER IF EXISTS binding_promotion_revision_update ON queue_binding_canaries;
    DROP TRIGGER IF EXISTS binding_promotion_revision ON queue_binding_canaries;
    CREATE TRIGGER binding_promotion_revision AFTER INSERT OR DELETE OR UPDATE ON queue_binding_canaries
        FOR EACH ROW EXECUTE FUNCTION capture_binding_promotion_revision('id,app_id,binding_id,trigger_id,deployment_id,snapshot,published_at,expires_at,cancelled,completed_deployment_id,completed_attempt,completed_generation');
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    DROP TRIGGER IF EXISTS binding_promotion_revision_update ON queue_bindings;
    DROP TRIGGER IF EXISTS binding_promotion_revision ON queue_bindings;
    CREATE TRIGGER binding_promotion_revision AFTER INSERT OR DELETE OR UPDATE ON queue_bindings
        FOR EACH ROW EXECUTE FUNCTION capture_binding_promotion_revision('id,account_id,app_id,name,queue_name,mode,enabled,retired_at');
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    DROP TRIGGER IF EXISTS binding_promotion_revision_update ON trigger_consumer_health;
    DROP TRIGGER IF EXISTS binding_promotion_revision ON trigger_consumer_health;
    CREATE TRIGGER binding_promotion_revision AFTER INSERT OR DELETE OR UPDATE ON trigger_consumer_health
        FOR EACH ROW EXECUTE FUNCTION capture_binding_promotion_revision('trigger_id,last_error_at');
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    DROP TRIGGER IF EXISTS binding_promotion_revision_update ON triggers;
    DROP TRIGGER IF EXISTS binding_promotion_revision ON triggers;
    CREATE TRIGGER binding_promotion_revision AFTER INSERT OR DELETE OR UPDATE ON triggers
        FOR EACH ROW EXECUTE FUNCTION capture_binding_promotion_revision('id,app_id,kind,source,config,enabled,created_at');
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_clone_checkpoint_instance_admission() RETURNS trigger
    LANGUAGE plpgsql
    AS $fn$
DECLARE fenced boolean;
BEGIN
 IF (TG_OP='INSERT' OR (NEW.id,NEW.app_id,NEW.deployment_id,NEW.node_id,NEW.wake_id,NEW.mode,NEW.state) IS DISTINCT FROM (OLD.id,OLD.app_id,OLD.deployment_id,OLD.node_id,OLD.wake_id,OLD.mode,OLD.state))
  AND current_setting('transaction_isolation')<>'read committed' THEN
  -- A snapshot older than the newest retirement request fails here (40001),
  -- so the fence check below never reads a stale retirement catalogue.
  PERFORM clone_checkpoint_admission_snapshot_guard();
 END IF;
 SELECT clone_checkpoint_deployment_fenced(NEW.deployment_id,d.reason) INTO fenced
 FROM (SELECT 1) x LEFT JOIN deployments d ON d.id=NEW.deployment_id;
 IF TG_OP='UPDATE' AND NOT fenced THEN
  SELECT clone_checkpoint_deployment_fenced(OLD.deployment_id,d.reason) INTO fenced
  FROM (SELECT 1) x LEFT JOIN deployments d ON d.id=OLD.deployment_id;
 END IF;
 IF fenced THEN
  IF TG_OP='INSERT' THEN
   RAISE EXCEPTION 'clone instance request permanently fenced' USING ERRCODE='23514',CONSTRAINT='clone_checkpoint_scheduler_request_fenced';
  ELSIF (NEW.id,NEW.app_id,NEW.deployment_id,NEW.node_id,NEW.wake_id,NEW.mode) IS DISTINCT FROM (OLD.id,OLD.app_id,OLD.deployment_id,OLD.node_id,OLD.wake_id,OLD.mode)
    OR (NEW.state IS DISTINCT FROM OLD.state AND NEW.state NOT IN ('draining','parked','stopped','failed','evicting_account_deleting')) THEN
   RAISE EXCEPTION 'clone instance restart permanently fenced' USING ERRCODE='23514',CONSTRAINT='clone_checkpoint_scheduler_request_fenced';
  END IF;
 END IF;
 RETURN NEW;
END $fn$;
-- +goose StatementEnd
