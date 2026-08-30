--
-- PostgreSQL database dump
--



SET statement_timeout = 0;
SET lock_timeout = 0;
SET idle_in_transaction_session_timeout = 0;
SET client_encoding = 'UTF8';
SET standard_conforming_strings = on;
SELECT pg_catalog.set_config('search_path', '', false);
SET check_function_bodies = false;
SET xmloption = content;
SET client_min_messages = warning;
SET row_security = off;

--
-- Name: citext; Type: EXTENSION; Schema: -; Owner: -
--

CREATE EXTENSION IF NOT EXISTS citext WITH SCHEMA public;


--
-- Name: EXTENSION citext; Type: COMMENT; Schema: -; Owner: -
--

COMMENT ON EXTENSION citext IS 'data type for case-insensitive character strings';


--
-- Name: apps_egress_allowlist_cidr_check(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.apps_egress_allowlist_cidr_check() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
declare
  bad cidr;
begin
  if new.egress_allowlist is null or cardinality(new.egress_allowlist) = 0 then
    return new;
  end if;
  -- Per-entry guards: family must be v4 or v6, mask must be non-zero.
  -- The /0 reject closes the same hole as the v4-only trigger's
  -- `prefix.Bits() == 0` reject at the wire + apid layers: an
  -- operator cannot pin "the entire address space" — that is the
  -- chain-policy accept's job, not the allowlist's. Two narrow
  -- selects (one per guard) keep the error messages specific; a
  -- combined select with bool_or would conflate family and masklen
  -- failures and force a parser to guess.
  for bad in
    select c
      from unnest(new.egress_allowlist) c
     where family(c) not in (4, 6)
     limit 1
  loop
    raise exception 'apps_egress_allowlist: only v4 or v6 CIDRs (got family % for %)', family(bad), bad
      using errcode = '23514',
            constraint = 'apps_egress_allowlist_cidr';
  end loop;
  for bad in
    select c
      from unnest(new.egress_allowlist) c
     where masklen(c) = 0
     limit 1
  loop
    raise exception 'apps_egress_allowlist: rejected % (masklen /0; ADR-032 non-/0 contract)', bad
      using errcode = '23514',
            constraint = 'apps_egress_allowlist_cidr';
  end loop;
  return new;
end;
$$;


--
-- Name: apps_maintenance_mode_notify(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.apps_maintenance_mode_notify() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF (TG_OP = 'UPDATE' AND NEW.maintenance_mode IS DISTINCT FROM OLD.maintenance_mode) THEN
        PERFORM pg_notify('app_changed', NEW.id::text);
    END IF;
    RETURN NEW;
END;
$$;


--
-- Name: compute_node_keys_notify(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.compute_node_keys_notify() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
begin
    perform pg_notify('compute_node_changed', TG_TABLE_NAME);
    return null;
end;
$$;


--
-- Name: compute_node_notify(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.compute_node_notify() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
declare
    payload jsonb;
begin
    payload := jsonb_build_object(
        'node_id', new.id::text,
        'active', new.active
    );
    perform pg_notify('compute_node_changed', payload::text);
    return new;
end;
$$;


--
-- Name: data_upstreams_notify(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.data_upstreams_notify() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    PERFORM pg_notify(
        'data_upstreams_changed',
        format('%s|%s|%s|%s|%s|%s|%s',
            COALESCE(NEW.app_id, OLD.app_id)::text,
            COALESCE(NEW.scope, OLD.scope),
            COALESCE(NEW.deployment_scope, OLD.deployment_scope),
            COALESCE(NEW.kind, OLD.kind),
            COALESCE(NEW.host, OLD.host),
            COALESCE(NEW.port, OLD.port)::text,
            TG_OP)
    );
    IF TG_OP = 'DELETE' THEN
        RETURN OLD;
    END IF;
    RETURN NEW;
END;
$$;


--
-- Name: deployment_sidecar_layers_cap_check(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.deployment_sidecar_layers_cap_check() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    current_count integer;
BEGIN
    -- NEW-row predicate on UPDATE; existing-row count on INSERT.
    -- Same query works for both because NEW carries the row's
    -- deployment_id whether we're inserting a fresh row or
    -- rewriting an existing one.
    -- `current_count` is the number of rows for this deployment_id
    -- ALREADY in the table at the time of this trigger call. We
    -- reject the operation when the row count would exceed
    -- SidecarCapMax (=2). Because the trigger fires BEFORE the
    -- row is written, the post-insert count is current_count + 1
    -- (INSERT) or unchanged (UPDATE that doesn't move the row to
    -- a different deployment). We compare against the post-write
    -- ceiling: if the existing count is already at or above
    -- SidecarCapMax, refuse — that is, current_count >= 2 is a
    -- hard reject, since adding another row would push us to 3.
    SELECT count(*) INTO current_count
        FROM deployment_sidecar_layers
        WHERE deployment_id = NEW.deployment_id;

    IF current_count >= 2 THEN
        RAISE EXCEPTION 'deployment_sidecar_layers: deployment % exceeds the 2-row cap (existing=%, new would make 3)',
            NEW.deployment_id, current_count
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;


--
-- Name: cors_presets_set_updated_at(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.cors_presets_set_updated_at() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    NEW.updated_at = now();
    RETURN NEW;
END;
$$;


--
-- Name: edge_rules_set_updated_at(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.edge_rules_set_updated_at() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    NEW.updated_at = now();
    RETURN NEW;
END;
$$;


--
-- Name: egress_policy_notify(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.egress_policy_notify() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
begin
    perform pg_notify(
        'egress_policy_changed',
        json_build_object(
            'policy_id', new.id,
            'public_iface', new.public_iface,
            'masquerade_cidr', new.masquerade_cidr,
            'overlay_exceptions', new.overlay_exceptions,
            'danger_accept_rfc1918_lateral_movement', new.danger_accept_rfc1918_lateral_movement,
            'changed_at', new.changed_at
        )::text
    );
    return null;
end;
$$;


--
-- Name: github_webhook_secrets_notify(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.github_webhook_secrets_notify() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    PERFORM pg_notify(
        'github_webhook_secret_changed',
        jsonb_build_object('installation_id', NEW.installation_id)::text
    );
    RETURN NEW;
END;
$$;


--
-- Name: instances_started_at_set(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.instances_started_at_set() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
begin
  if new.started_at is null then
    new.started_at = now();
  end if;
  return new;
end
$$;


--
-- Name: invocation_done_notify(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.invocation_done_notify() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
declare
    payload jsonb;
begin
    if (tg_op = 'UPDATE' and old.state in ('pending','dispatching')
        and new.state in ('completed','failed','cancelled')) then
        payload := jsonb_build_object(
            'invocation_id', new.id::text,
            'app_id', new.app_id::text,
            'source', new.source,
            'state', new.state
        );
        perform pg_notify('invocation_done', payload::text);
    end if;
    return new;
end;
$$;


--
-- Name: invocation_due_notify(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.invocation_due_notify() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
declare
    payload jsonb;
begin
    payload := jsonb_build_object(
        'invocation_id', new.id::text,
        'app_id', new.app_id::text,
        'source', new.source
    );
    perform pg_notify('invocation_due', payload::text);
    return new;
end;
$$;


--
-- Name: job_tasks_notify(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.job_tasks_notify() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    -- Only notify on transitions INTO queued (initial
    -- INSERT) or from queued (claim by the dispatch
    -- tick). Other transitions don't need to wake the
    -- LISTEN worker because they're responses to the
    -- worker's own batch.
    IF (TG_OP = 'INSERT' AND NEW.status = 'queued')
       OR (TG_OP = 'UPDATE' AND OLD.status = 'queued'
           AND NEW.status <> 'queued') THEN
        PERFORM pg_notify(
            'job_tasks_queued',
            format('%s|%s|%s', NEW.run_id, NEW.task_index, TG_OP)
        );
    END IF;
    IF TG_OP = 'DELETE' THEN
        RETURN OLD;
    END IF;
    RETURN NEW;
END;
$$;


--
-- Name: notify_tenant_surface_changed(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.notify_tenant_surface_changed() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        PERFORM pg_notify('tenant_surface_changed', OLD.surface_id::text);
        RETURN OLD;
    ELSIF TG_OP = 'UPDATE' THEN
        -- surface row updates (status, cert_state, cert_not_after, …)
        -- bubble up the surface id directly; this trigger is also
        -- responsible for the surface's row-level changes.
        PERFORM pg_notify('tenant_surface_changed', NEW.id::text);
        RETURN NEW;
    ELSE
        -- INSERT on tenant_surfaces
        PERFORM pg_notify('tenant_surface_changed', NEW.id::text);
        RETURN NEW;
    END IF;
END
$$;


--
-- Name: pg_tier_rank(text); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.pg_tier_rank(tier text) RETURNS integer
    LANGUAGE sql IMMUTABLE
    AS $$
    SELECT CASE tier
        WHEN 'compose'    THEN 8
        WHEN 'k8s'        THEN 8
        WHEN 'render'     THEN 8
        WHEN 'fly'        THEN 8
        WHEN 'serverless' THEN 8
        WHEN 'procfile'   THEN 6
        WHEN 'workspace'  THEN 4
        WHEN 'convention' THEN 2
        WHEN 'single'     THEN 1
        ELSE 0
    END
$$;


--
-- Name: trg_notify_trigger_ready(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.trg_notify_trigger_ready() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    PERFORM pg_notify('trigger_ready',
        json_build_object('trigger_id', NEW.trigger_id,
                          'record_id',  NEW.id,
                          'item_id',    NEW.item_identifier)::text);
    RETURN NEW;
END $$;


SET default_table_access_method = heap;

--
-- Name: account_credits; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.account_credits (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    account_id uuid NOT NULL,
    cents_remaining bigint NOT NULL,
    reason text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    expires_at timestamp with time zone,
    CONSTRAINT account_credits_cents_remaining_check CHECK ((cents_remaining >= 0)),
    CONSTRAINT account_credits_reason_check CHECK (((char_length(reason) >= 3) AND (char_length(reason) <= 500)))
);


--
-- Name: account_passwords; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.account_passwords (
    account_id uuid NOT NULL,
    hash text NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: accounts; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.accounts (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    email public.citext NOT NULL,
    plan text DEFAULT 'free'::text NOT NULL,
    status text DEFAULT 'active'::text NOT NULL,
    provider_customer_id text,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    deletion_requested_at timestamp with time zone,
    stripe_subscription_item text,
    last_quota_warning_at timestamp with time zone,
    past_due_at timestamp with time zone,
    mfa_enrolled_at timestamp with time zone,
    mfa_secret_encrypted bytea,
    mfa_recovery_codes_hash bytea[],
    mfa_required boolean DEFAULT false NOT NULL,
    overage_cap_cents bigint,
    key_grace_window_days integer,
    egress_allowlist_extra integer DEFAULT 0 NOT NULL,
    CONSTRAINT accounts_egress_allowlist_extra_check CHECK ((egress_allowlist_extra >= 0)),
    CONSTRAINT accounts_key_grace_window_days_check CHECK (((key_grace_window_days IS NULL) OR (key_grace_window_days >= 0))),
    CONSTRAINT accounts_mfa_enrolled_shape_chk CHECK (((mfa_enrolled_at IS NULL) OR ((mfa_secret_encrypted IS NOT NULL) AND ((mfa_recovery_codes_hash IS NULL) OR (array_length(mfa_recovery_codes_hash, 1) >= 0))))),
    CONSTRAINT accounts_overage_cap_cents_chk CHECK (((overage_cap_cents IS NULL) OR (overage_cap_cents >= 0))),
    CONSTRAINT accounts_plan_check CHECK ((plan = ANY (ARRAY['free'::text, 'hobby'::text, 'pro'::text, 'scale'::text]))),
    CONSTRAINT accounts_status_check CHECK ((status = ANY (ARRAY['active'::text, 'past_due'::text, 'suspended'::text, 'deleted_pending'::text])))
);


--
-- Name: alert_deliveries; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.alert_deliveries (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    rule_id uuid NOT NULL,
    account_id uuid NOT NULL,
    app_id uuid,
    idempotency_key text NOT NULL,
    payload jsonb NOT NULL,
    status text DEFAULT 'pending'::text NOT NULL,
    attempt_count integer DEFAULT 0 NOT NULL,
    last_status_code integer,
    last_error text,
    observed_value double precision NOT NULL,
    fired_at timestamp with time zone DEFAULT now() NOT NULL,
    delivered_at timestamp with time zone,
    CONSTRAINT alert_deliveries_status_chk CHECK ((status = ANY (ARRAY['pending'::text, 'delivered'::text, 'failed'::text])))
);


--
-- Name: alert_rules; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.alert_rules (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    account_id uuid NOT NULL,
    app_id uuid,
    name text NOT NULL,
    enabled boolean DEFAULT true NOT NULL,
    metric text NOT NULL,
    comparison text NOT NULL,
    threshold double precision NOT NULL,
    window_spec text NOT NULL,
    failure_source text,
    webhook_url text NOT NULL,
    webhook_secret_sealed bytea NOT NULL,
    cooldown_minutes integer DEFAULT 30 NOT NULL,
    state text DEFAULT 'ok'::text NOT NULL,
    last_fired_at timestamp with time zone,
    last_evaluated_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    org_id uuid,
    CONSTRAINT alert_rules_comparison_chk CHECK ((comparison = ANY (ARRAY['gt'::text, 'gte'::text, 'lt'::text, 'lte'::text]))),
    CONSTRAINT alert_rules_cooldown_chk CHECK (((cooldown_minutes >= 5) AND (cooldown_minutes <= 1440))),
    CONSTRAINT alert_rules_failure_source_chk CHECK (((failure_source IS NULL) OR (failure_source = ANY (ARRAY['any'::text, 'cron'::text, 'queue'::text, 'delayed_task'::text, 'async_invoke'::text])))),
    CONSTRAINT alert_rules_failure_source_xor_chk CHECK ((((metric = 'failed_invocations'::text) AND (failure_source IS NOT NULL)) OR ((metric <> 'failed_invocations'::text) AND (failure_source IS NULL)))),
    CONSTRAINT alert_rules_metric_chk CHECK ((metric = ANY (ARRAY['error_rate_pct'::text, 'latency_p50_ms'::text, 'latency_p95_ms'::text, 'latency_p99_ms'::text, 'cold_start_pct'::text, 'request_count'::text, 'failed_invocations'::text]))),
    CONSTRAINT alert_rules_name_len_chk CHECK (((char_length(name) >= 1) AND (char_length(name) <= 64))),
    CONSTRAINT alert_rules_state_chk CHECK ((state = ANY (ARRAY['ok'::text, 'firing'::text]))),
    CONSTRAINT alert_rules_window_chk CHECK ((window_spec = ANY (ARRAY['5m'::text, '15m'::text, '1h'::text, '6h'::text, '24h'::text, '7d'::text, '15d'::text])))
);


--
-- Name: api_keys; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.api_keys (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    account_id uuid NOT NULL,
    key_sha256 bytea NOT NULL,
    label text,
    last_used_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    scopes text[] DEFAULT '{admin}'::text[] NOT NULL,
    org_id uuid NOT NULL,
    expires_at timestamp with time zone,
    status text DEFAULT 'active'::text NOT NULL,
    revoked_at timestamp with time zone,
    rotated_from_id uuid,
    created_ip inet,
    created_ua text,
    parent_key_id uuid,
    CONSTRAINT api_keys_scopes_vocab_chk CHECK (((scopes <@ ARRAY['admin'::text, 'deploy:write'::text, 'secrets:read'::text, 'secrets:write'::text, 'usage:read'::text, 'apps:read'::text, 'env:read'::text, 'env:write'::text]) AND (cardinality(scopes) > 0))),
    CONSTRAINT api_keys_status_check CHECK ((status = ANY (ARRAY['active'::text, 'grace'::text, 'revoked'::text])))
);


--
-- Name: app_envs; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.app_envs (
    account_id uuid NOT NULL,
    app_id uuid NOT NULL,
    key text NOT NULL,
    value text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    org_id uuid,
    scope text DEFAULT 'default'::text NOT NULL,
    CONSTRAINT app_envs_key_shape CHECK (((key ~ '^[A-Z][A-Z0-9_]*$'::text) AND (length(key) <= 128))),
    CONSTRAINT app_envs_scope_shape CHECK ((scope ~ '^[a-z0-9]([a-z0-9-]{1,38})[a-z0-9]$'::text))
);


--
-- Name: app_error_requests; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.app_error_requests (
    id uuid NOT NULL,
    account_id uuid NOT NULL,
    app_id uuid NOT NULL,
    fingerprint character(64) NOT NULL,
    request_id uuid NOT NULL,
    received_at timestamp with time zone NOT NULL,
    route text NOT NULL,
    http_status integer NOT NULL,
    error_class text NOT NULL,
    sample_message text NOT NULL,
    deployment_id uuid,
    headers_sample jsonb,
    redactions text[] DEFAULT '{}'::text[] NOT NULL,
    CONSTRAINT app_error_requests_error_class_check CHECK ((error_class = ANY (ARRAY['db_timeout'::text, 'stripe_timeout'::text, 'null_pointer'::text, 'invalid_json'::text, 'wake_failed'::text, 'upstream_5xx'::text, 'unhandled'::text, 'client_error'::text]))),
    CONSTRAINT app_error_requests_fingerprint_check CHECK ((fingerprint ~ '^[0-9a-f]{64}$'::text)),
    CONSTRAINT app_error_requests_headers_sample_check CHECK (((headers_sample IS NULL) OR (pg_column_size((headers_sample)::text) <= 8192))),
    CONSTRAINT app_error_requests_http_status_check CHECK (((http_status >= 400) AND (http_status <= 599))),
    CONSTRAINT app_error_requests_sample_message_check CHECK ((pg_column_size(sample_message) <= 512))
);


--
-- Name: app_errors; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.app_errors (
    id uuid NOT NULL,
    account_id uuid NOT NULL,
    app_id uuid NOT NULL,
    deployment_id uuid,
    fingerprint character(64) NOT NULL,
    route text NOT NULL,
    http_status integer NOT NULL,
    error_class text NOT NULL,
    sample_message text NOT NULL,
    count bigint DEFAULT 1 NOT NULL,
    request_count bigint DEFAULT 1 NOT NULL,
    first_seen_at timestamp with time zone DEFAULT now() NOT NULL,
    last_seen_at timestamp with time zone DEFAULT now() NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT app_errors_error_class_check CHECK ((error_class = ANY (ARRAY['db_timeout'::text, 'stripe_timeout'::text, 'null_pointer'::text, 'invalid_json'::text, 'wake_failed'::text, 'upstream_5xx'::text, 'unhandled'::text, 'client_error'::text]))),
    CONSTRAINT app_errors_fingerprint_check CHECK ((fingerprint ~ '^[0-9a-f]{64}$'::text)),
    CONSTRAINT app_errors_http_status_check CHECK (((http_status >= 400) AND (http_status <= 599))),
    CONSTRAINT app_errors_sample_message_check CHECK ((pg_column_size(sample_message) <= 512))
);


--
-- Name: app_registry_credentials; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.app_registry_credentials (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    account_id uuid NOT NULL,
    app_id uuid NOT NULL,
    registry text NOT NULL,
    username text NOT NULL,
    password_encrypted bytea NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    last_used_at timestamp with time zone,
    CONSTRAINT app_registry_credentials_password_chk CHECK ((length(password_encrypted) > 0)),
    CONSTRAINT app_registry_credentials_registry_chk CHECK (((length(registry) > 0) AND (length(registry) <= 253))),
    CONSTRAINT app_registry_credentials_username_chk CHECK (((length(username) > 0) AND (length(username) <= 256)))
);


--
-- Name: app_secrets; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.app_secrets (
    account_id uuid NOT NULL,
    app_id uuid NOT NULL,
    key text NOT NULL,
    ciphertext bytea NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    org_id uuid,
    kid text,
    CONSTRAINT app_secrets_key_shape CHECK (((key ~ '^[A-Z][A-Z0-9_]*$'::text) AND (length(key) <= 128)))
);


--
-- Name: app_trusted_signers; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.app_trusted_signers (
    account_id uuid NOT NULL,
    app_id uuid NOT NULL,
    signer_name text NOT NULL,
    cosign_public_key bytea NOT NULL,
    added_at timestamp with time zone DEFAULT now() NOT NULL,
    added_by_account_id uuid NOT NULL,
    CONSTRAINT app_trusted_signers_name_shape CHECK ((signer_name ~ '^[a-z0-9][a-z0-9_-]{0,63}$'::text)),
    CONSTRAINT app_trusted_signers_pem_shape CHECK (((octet_length(cosign_public_key) >= 64) AND (octet_length(cosign_public_key) <= 1024)))
);


--
-- Name: app_webhook_deliveries; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.app_webhook_deliveries (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    webhook_id uuid NOT NULL,
    app_id uuid NOT NULL,
    account_id uuid NOT NULL,
    event text NOT NULL,
    payload jsonb NOT NULL,
    attempt integer DEFAULT 0 NOT NULL,
    status text DEFAULT 'pending'::text NOT NULL,
    last_error text,
    last_response_code integer,
    next_attempt_at timestamp with time zone DEFAULT now() NOT NULL,
    delivered_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT app_webhook_deliveries_attempt_chk CHECK (((attempt >= 0) AND (attempt <= 8))),
    CONSTRAINT app_webhook_deliveries_event_chk CHECK ((event = ANY (ARRAY['cron.fired'::text, 'cron.fired.manually'::text, 'app.deployed'::text, 'app.scaled'::text, 'app.parked'::text, 'app.woken'::text]))),
    CONSTRAINT app_webhook_deliveries_status_chk CHECK ((status = ANY (ARRAY['pending'::text, 'in_flight'::text, 'succeeded'::text, 'failed'::text, 'dead'::text])))
);


--
-- Name: app_webhooks; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.app_webhooks (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    app_id uuid NOT NULL,
    account_id uuid NOT NULL,
    target_url text NOT NULL,
    secret_sealed bytea NOT NULL,
    event_filter text[] DEFAULT '{}'::text[] NOT NULL,
    retry_policy text DEFAULT 'default'::text NOT NULL,
    enabled boolean DEFAULT true NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT app_webhooks_retry_policy_chk CHECK ((retry_policy = ANY (ARRAY['default'::text, 'aggressive'::text, 'none'::text]))),
    CONSTRAINT app_webhooks_target_url_len_chk CHECK (((char_length(target_url) >= 8) AND (char_length(target_url) <= 2048)))
);


--
-- Name: apps; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.apps (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    account_id uuid NOT NULL,
    slug text NOT NULL,
    type text DEFAULT 'app'::text NOT NULL,
    runtime text,
    ram_mb integer NOT NULL,
    idle_timeout_s integer,
    max_concurrency integer DEFAULT 1 NOT NULL,
    status text DEFAULT 'active'::text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    manifest jsonb DEFAULT '{}'::jsonb NOT NULL,
    github_install_id bigint,
    github_repo_full_name text,
    github_production_branch text,
    min_instances integer DEFAULT 0 NOT NULL,
    egress_allowlist cidr[] DEFAULT '{}'::cidr[] NOT NULL,
    public_auth_ip_allowlist cidr[] DEFAULT '{}'::cidr[] NOT NULL,
    autoscale_target_rps integer,
    autoscale_target_cpu_pct integer,
    github_install_binding_id text,
    github_install_account_id uuid,
    github_install_linked_at timestamp with time zone,
    project_id uuid,
    root_dir text DEFAULT ''::text NOT NULL,
    workload_name text DEFAULT ''::text NOT NULL,
    workload_class text DEFAULT 'http'::text NOT NULL,
    start_command text,
    streaming_enabled boolean DEFAULT false NOT NULL,
    scaling_policy jsonb DEFAULT '{}'::jsonb NOT NULL,
    last_scale_out_at timestamp with time zone,
    last_scale_in_at timestamp with time zone,
    require_signed boolean DEFAULT false NOT NULL,
    node_id uuid,
    reassigned_at timestamp with time zone,
    org_id uuid,
    migrated_at timestamp with time zone,
    warm_snapshot_enabled boolean DEFAULT false NOT NULL,
    warm_snapshot_min_requests integer DEFAULT 5 NOT NULL,
    warm_snapshot_min_ms integer DEFAULT 2000 NOT NULL,
    eviction_priority text DEFAULT 'best_effort'::text NOT NULL,
    require_authn boolean DEFAULT false NOT NULL,
    public_auth_mode text DEFAULT 'open'::text NOT NULL,
    public_auth_basic bytea,
    websocket_enabled boolean DEFAULT false NOT NULL,
    auth_default_flipped_at timestamp with time zone,
    overflow_node uuid,
    route_metrics_enabled boolean DEFAULT false NOT NULL,
    app_protocol text DEFAULT 'http1'::text NOT NULL,
    preview_of_slug text,
    preview_pr_number integer,
    preview_pr_state text,
    preview_expires_at timestamp with time zone,
    cors_default_enabled boolean DEFAULT false NOT NULL,
    cors_default_origins text[],
    maintenance_mode boolean DEFAULT false NOT NULL,
    CONSTRAINT apps_app_protocol_chk CHECK ((app_protocol = ANY (ARRAY['http1'::text, 'http2'::text, 'grpc'::text]))),
    CONSTRAINT apps_autoscale_target_cpu_pct_range CHECK (((autoscale_target_cpu_pct IS NULL) OR ((autoscale_target_cpu_pct >= 0) AND (autoscale_target_cpu_pct <= 100)))),
    CONSTRAINT apps_autoscale_target_rps_nonneg CHECK (((autoscale_target_rps IS NULL) OR (autoscale_target_rps >= 0))),
    CONSTRAINT apps_eviction_priority_chk CHECK ((eviction_priority = ANY (ARRAY['best_effort'::text, 'reserved'::text]))),
    CONSTRAINT apps_idle_timeout_s_check CHECK (((idle_timeout_s IS NULL) OR (idle_timeout_s >= 10))),
    CONSTRAINT apps_last_scale_in_at_le_now_chk CHECK (((last_scale_in_at IS NULL) OR (last_scale_in_at <= now()))),
    CONSTRAINT apps_last_scale_out_at_le_now_chk CHECK (((last_scale_out_at IS NULL) OR (last_scale_out_at <= now()))),
    CONSTRAINT apps_max_concurrency_check CHECK ((max_concurrency >= 1)),
    CONSTRAINT apps_migrated_at_chk CHECK (((migrated_at IS NULL) OR (migrated_at <= (now() + '00:01:00'::interval)))),
    CONSTRAINT apps_min_instances_check CHECK ((min_instances >= 0)),
    CONSTRAINT apps_node_id_nonempty_chk CHECK ((node_id <> '00000000-0000-0000-0000-000000000000'::uuid)),
    CONSTRAINT apps_overflow_node_chk CHECK (((overflow_node IS NULL) OR (overflow_node <> '00000000-0000-0000-0000-000000000000'::uuid))),
    CONSTRAINT apps_preview_pr_state_chk CHECK (((preview_pr_state = ANY (ARRAY['open'::text, 'closed'::text, 'stale'::text, 'torn_down'::text])) OR (preview_pr_state IS NULL))),
    CONSTRAINT apps_public_auth_mode_chk CHECK ((public_auth_mode = ANY (ARRAY['open'::text, 'bearer'::text, 'basic'::text, 'ip_allowlist'::text, 'internal_only'::text]))),
    CONSTRAINT apps_ram_mb_check CHECK ((ram_mb > 0)),
    CONSTRAINT apps_reassigned_at_chk CHECK (((reassigned_at IS NULL) OR (reassigned_at <= (now() + '00:01:00'::interval)))),
    CONSTRAINT apps_runtime_check CHECK (((runtime IS NULL) OR (runtime = ANY (ARRAY['node22'::text, 'python312'::text, 'go124'::text, 'go124-alpine'::text, 'node24'::text, 'python313'::text])))),
    CONSTRAINT apps_status_check CHECK ((status = ANY (ARRAY['active'::text, 'evicted_cold'::text, 'deleted'::text]))),
    CONSTRAINT apps_type_check CHECK ((type = ANY (ARRAY['app'::text, 'function'::text]))),
    CONSTRAINT apps_warm_snapshot_min_ms_check CHECK (((warm_snapshot_min_ms >= 100) AND (warm_snapshot_min_ms <= 60000))),
    CONSTRAINT apps_warm_snapshot_min_requests_check CHECK (((warm_snapshot_min_requests >= 1) AND (warm_snapshot_min_requests <= 100))),
    CONSTRAINT apps_workload_class_chk CHECK ((workload_class = ANY (ARRAY['http'::text, 'graphql'::text, 'grpc'::text, 'job'::text, 'worker'::text])))
);


--
-- Name: COLUMN apps.autoscale_target_rps; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON COLUMN public.apps.autoscale_target_rps IS 'Per-instance RPS target. When live_request_count / live_instance_count exceeds this, schedd admits another instance (up to plan max_concurrency). Hobby/Pro/Scale only (plan gate). 0 / NULL = disabled (the trigger skips the app).';


--
-- Name: COLUMN apps.autoscale_target_cpu_pct; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON COLUMN public.apps.autoscale_target_cpu_pct IS 'Per-instance CPU% target (1..100). Pro/Scale only (plan gate). 0 / NULL = disabled (the trigger skips the app). CPU target is unbounded above 100 inside the DB; the apid handler enforces [1, 100] via 422.';


--
-- Name: audit_log; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.audit_log (
    id uuid NOT NULL,
    kind text NOT NULL,
    account_id uuid,
    account_email text,
    actor text,
    received_at timestamp with time zone DEFAULT now() NOT NULL,
    data jsonb
);


--
-- Name: build_provenance; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.build_provenance (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    build_id uuid NOT NULL,
    buildkit_version text,
    railpack_version text,
    base_digest text,
    source_sha256 text NOT NULL,
    source_url text,
    commit_sha text,
    plan text,
    runner_digest text,
    builder_node_id text,
    started_at timestamp with time zone NOT NULL,
    finished_at timestamp with time zone NOT NULL,
    sbom_storage_key text,
    framework_version text
);


--
-- Name: builder_usage; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.builder_usage (
    build_id uuid NOT NULL,
    account_id uuid NOT NULL,
    app_id uuid NOT NULL,
    finished_at timestamp with time zone NOT NULL,
    kind text DEFAULT 'none'::text NOT NULL,
    seconds bigint DEFAULT 0 NOT NULL,
    org_id uuid
);


--
-- Name: TABLE builder_usage; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON TABLE public.builder_usage IS 'Per-build wall-clock seconds, one row per terminal build. Source: cmd/builderd reaper + markSucceeded/markFailed adapters. ADR-048. Informational only — not billed.';


--
-- Name: COLUMN builder_usage.kind; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON COLUMN public.builder_usage.kind IS 'build kind (railpack|dockerfile|tarball). Mirrors builds.kind. ADR-048.';


--
-- Name: builds; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.builds (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    deployment_id uuid NOT NULL,
    kind text NOT NULL,
    source_bytes bigint NOT NULL,
    status text NOT NULL,
    failure_class text,
    log_path text,
    started_at timestamp with time zone,
    finished_at timestamp with time zone,
    enqueued_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT builds_failure_class_check CHECK (((failure_class IS NULL) OR (failure_class = ANY (ARRAY['oom'::text, 'timeout'::text, 'user_error'::text, 'infra'::text])))),
    CONSTRAINT builds_kind_check CHECK ((kind = ANY (ARRAY['railpack'::text, 'dockerfile'::text, 'tarball'::text, 'github'::text, 'preview'::text]))),
    CONSTRAINT builds_status_check CHECK ((status = ANY (ARRAY['queued'::text, 'running'::text, 'succeeded'::text, 'failed'::text])))
);


--
-- Name: cli_auth_codes; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.cli_auth_codes (
    token_hash bytea NOT NULL,
    account_id uuid,
    status text DEFAULT 'pending'::text NOT NULL,
    expires_at timestamp with time zone NOT NULL,
    consumed_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT cli_auth_codes_status_check CHECK ((status = ANY (ARRAY['pending'::text, 'consumed'::text, 'expired'::text])))
);


--
-- Name: compute_node_heartbeats; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.compute_node_heartbeats (
    id bigint NOT NULL,
    node_id uuid NOT NULL,
    received_at timestamp with time zone DEFAULT now() NOT NULL,
    last_heartbeat_at timestamp with time zone NOT NULL,
    source text NOT NULL,
    cpu_pct_60s numeric(5,2),
    disk_used_bytes bigint,
    CONSTRAINT compute_node_heartbeats_source_check CHECK ((source = ANY (ARRAY['heartbeat_tick'::text, 'deactivation'::text, 'reactivation'::text])))
);


--
-- Name: compute_node_heartbeats_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.compute_node_heartbeats_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: compute_node_heartbeats_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.compute_node_heartbeats_id_seq OWNED BY public.compute_node_heartbeats.id;


--
-- Name: compute_node_keys; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.compute_node_keys (
    compute_node_id uuid NOT NULL,
    key_id text NOT NULL,
    public_key_pem text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT compute_node_keys_key_id_shape CHECK ((key_id ~ '^[a-f0-9]{64}$'::text)),
    CONSTRAINT compute_node_keys_pem_shape CHECK ((public_key_pem ~~ '-----BEGIN PUBLIC KEY-----%'::text))
);


--
-- Name: consumer_keys; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.consumer_keys (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    account_id uuid NOT NULL,
    app_id uuid NOT NULL,
    name text NOT NULL,
    prefix text NOT NULL,
    hashed_secret bytea NOT NULL,
    scopes text[] DEFAULT '{}'::text[] NOT NULL,
    expires_at timestamp with time zone,
    last_used_at timestamp with time zone,
    revoked_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT consumer_keys_expires_after_created_chk CHECK (((expires_at IS NULL) OR (expires_at > created_at))),
    CONSTRAINT consumer_keys_hashed_secret_len_chk CHECK ((octet_length(hashed_secret) = 32)),
    CONSTRAINT consumer_keys_name_len_chk CHECK (((length(name) >= 1) AND (length(name) <= 64))),
    CONSTRAINT consumer_keys_prefix_len_chk CHECK (((length(prefix) >= 1) AND (length(prefix) <= 16))),
    CONSTRAINT consumer_keys_revoked_state_chk CHECK (((revoked_at IS NULL) OR (revoked_at >= created_at))),
    CONSTRAINT consumer_keys_scopes_vocab_chk CHECK ((scopes <@ ARRAY['read'::text, 'write'::text, 'admin'::text]::text[]) AND (cardinality(scopes) > 0))
);


--
-- Name: compute_nodes; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.compute_nodes (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    name text NOT NULL,
    target_url text NOT NULL,
    vpcpus integer NOT NULL,
    mem_mb integer NOT NULL,
    max_concurrency integer NOT NULL,
    admission_ceiling_mb integer NOT NULL,
    active boolean DEFAULT true NOT NULL,
    last_heartbeat_at timestamp with time zone DEFAULT now() NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    region text,
    zone text,
    schedd_target_url text,
    vcpu_budget integer DEFAULT 160 NOT NULL,
    public_ip inet,
    public_ip_set_at timestamp with time zone,
    release_id text,
    manifest_hash text,
    host_certificate text,
    cert_fingerprint text,
    role text,
    generation integer,
    gateway_target_url text,
    CONSTRAINT compute_nodes_admission_ceiling_mb_check CHECK ((admission_ceiling_mb > 0)),
    CONSTRAINT compute_nodes_gateway_target_url_scheme_chk CHECK (((gateway_target_url IS NULL) OR (gateway_target_url ~ '^tcp://[^/:][^/]*:[0-9]+$'::text))),
    CONSTRAINT compute_nodes_max_concurrency_check CHECK ((max_concurrency > 0)),
    CONSTRAINT compute_nodes_mem_mb_check CHECK ((mem_mb > 0)),
    CONSTRAINT compute_nodes_public_ip_format_chk CHECK (((public_ip IS NULL) OR (family(public_ip) = ANY (ARRAY[4, 6])))),
    CONSTRAINT compute_nodes_schedd_target_url_scheme_chk CHECK (((schedd_target_url IS NULL) OR (schedd_target_url ~ '^(unix|tcp)://'::text))),
    CONSTRAINT compute_nodes_target_url_check CHECK ((target_url ~ '^(unix|tcp|dns)://'::text)),
    CONSTRAINT compute_nodes_vcpu_budget_check CHECK ((vcpu_budget > 0)),
    CONSTRAINT compute_nodes_vpcpus_check CHECK ((vpcpus > 0))
);


--
-- Name: COLUMN compute_nodes.region; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON COLUMN public.compute_nodes.region IS 'Locality label for the chooser tie-break (pkg/sched/placement.go). Free-form text; nullable so pre-00072 rows accept the schema. The seeded default-local row is backfilled to ''local''. ADR-025.';


--
-- Name: COLUMN compute_nodes.zone; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON COLUMN public.compute_nodes.zone IS 'Finer locality inside region. Currently informational; nullable. ADR-025.';


--
-- Name: COLUMN compute_nodes.release_id; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON COLUMN public.compute_nodes.release_id IS 'release_bundles.git_sha this node claims membership in (PR-3a). NULL = pre-bundle row. Populated by PR-3 release install + PR-2 renderer.';


--
-- Name: COLUMN compute_nodes.manifest_hash; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON COLUMN public.compute_nodes.manifest_hash IS 'sha256:<64hex> hash of the manifest the PR-2 renderer materialized on this node (PR-3a). NULL = pre-manifest row. Compared against release_bundles.manifest_hash by PR-4 doctor.';


--
-- Name: COLUMN compute_nodes.host_certificate; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON COLUMN public.compute_nodes.host_certificate IS 'PEM-encoded leaf certificate for this node (PR-3a). NULL = pre-PR-X or pre-cmd/hostage-gen row. Doctor reads to verify cert on disk matches cert_fingerprint.';


--
-- Name: COLUMN compute_nodes.cert_fingerprint; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON COLUMN public.compute_nodes.cert_fingerprint IS 'sha256:<64hex> fingerprint of host_certificate (PR-3a). NULL until secrets init (PR-X) or cmd/hostage-gen stamps it. PR-3 bundle install + PR-4 doctor compare against pkg/pki.LoadCertificateFingerprint at mTLS handshake time.';


--
-- Name: COLUMN compute_nodes.role; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON COLUMN public.compute_nodes.role IS 'per-node role label: control-plane | compute-node (PR-3a). Populated from manifest.fleet.hosts[].role by PR-2 renderer. NULL = pre-manifest row.';


--
-- Name: COLUMN compute_nodes.generation; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON COLUMN public.compute_nodes.generation IS 'monotonic counter bumped by PR-4 doctor on per-node inconsistency detection (PR-3a). Default 0; never decreases.';


--
-- Name: cors_presets; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.cors_presets (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    account_id uuid NOT NULL,
    app_id uuid,
    name text NOT NULL,
    description text,
    allow_origins text[] NOT NULL,
    allow_methods text[] NOT NULL,
    allow_headers text[] DEFAULT '{}'::text[] NOT NULL,
    expose_headers text[] DEFAULT '{}'::text[] NOT NULL,
    allow_credentials boolean DEFAULT false NOT NULL,
    max_age_seconds integer DEFAULT 600 NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT cors_presets_name_check CHECK (((length(name) >= 1) AND (length(name) <= 64))),
    CONSTRAINT cors_presets_max_age_check CHECK (((max_age_seconds >= 0) AND (max_age_seconds <= 86400)))
);


--
-- Name: credit_ledger; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.credit_ledger (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    account_id uuid NOT NULL,
    credit_id uuid NOT NULL,
    delta_cents bigint NOT NULL,
    reason text NOT NULL,
    actor text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    provider_invoice_id text,
    CONSTRAINT credit_ledger_delta_cents_check CHECK ((delta_cents <> 0))
);


--
-- Name: cron_fire_now_requests; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.cron_fire_now_requests (
    id uuid NOT NULL,
    cron_id uuid NOT NULL,
    account_id uuid NOT NULL,
    requested_at timestamp with time zone DEFAULT now() NOT NULL,
    status text NOT NULL,
    invocation_id uuid,
    error text,
    finished_at timestamp with time zone,
    CONSTRAINT cron_fire_now_requests_status_check CHECK ((status = ANY (ARRAY['pending'::text, 'running'::text, 'succeeded'::text, 'failed'::text, 'cancelled'::text])))
);


--
-- Name: crons; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.crons (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    app_id uuid NOT NULL,
    schedule text NOT NULL,
    path text DEFAULT '/'::text NOT NULL,
    enabled boolean DEFAULT true NOT NULL,
    last_fired_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    org_id uuid
);


--
-- Name: custom_domains; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.custom_domains (
    domain public.citext NOT NULL,
    app_id uuid NOT NULL,
    verified_at timestamp with time zone,
    challenge_token text DEFAULT ''::text NOT NULL,
    app_id_redirect uuid,
    org_id uuid
);


--
-- Name: data_upstream_probes; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.data_upstream_probes (
    id uuid NOT NULL,
    host_redacted_hash text NOT NULL,
    region text NOT NULL,
    kind text NOT NULL,
    sampled_at timestamp with time zone NOT NULL,
    rtt_ms integer,
    ok boolean NOT NULL,
    error_class text,
    probe_node text,
    CONSTRAINT data_upstream_probes_error_class_check CHECK (((error_class IS NULL) OR (error_class = ANY (ARRAY['timeout'::text, 'refused'::text, 'tls_handshake'::text, 'dns'::text, 'unreachable'::text])))),
    CONSTRAINT data_upstream_probes_kind_check CHECK ((kind = ANY (ARRAY['postgres'::text, 'redis'::text, 'mongo'::text, 'cassandra'::text, 'clickhouse'::text, 'elasticsearch'::text, 'opensearch'::text, 'rabbitmq'::text, 'kafka'::text, 'nats'::text, 'minio'::text, 'memcached'::text, 'etcd'::text, 's3'::text, 'https_api'::text]))),
    CONSTRAINT data_upstream_probes_ok_pair_chk CHECK ((((ok = true) AND (rtt_ms IS NOT NULL) AND (error_class IS NULL)) OR ((ok = false) AND (error_class IS NOT NULL)))),
    CONSTRAINT data_upstream_probes_region_check CHECK ((region ~ '^[a-z0-9_-]{1,32}$'::text)),
    CONSTRAINT data_upstream_probes_rtt_ms_check CHECK (((rtt_ms IS NULL) OR ((rtt_ms >= 0) AND (rtt_ms <= 600000))))
)
PARTITION BY RANGE (sampled_at);


--
-- Name: data_upstream_probes_default; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.data_upstream_probes_default (
    id uuid NOT NULL,
    host_redacted_hash text NOT NULL,
    region text NOT NULL,
    kind text NOT NULL,
    sampled_at timestamp with time zone NOT NULL,
    rtt_ms integer,
    ok boolean NOT NULL,
    error_class text,
    probe_node text,
    CONSTRAINT data_upstream_probes_error_class_check CHECK (((error_class IS NULL) OR (error_class = ANY (ARRAY['timeout'::text, 'refused'::text, 'tls_handshake'::text, 'dns'::text, 'unreachable'::text])))),
    CONSTRAINT data_upstream_probes_kind_check CHECK ((kind = ANY (ARRAY['postgres'::text, 'redis'::text, 'mongo'::text, 'cassandra'::text, 'clickhouse'::text, 'elasticsearch'::text, 'opensearch'::text, 'rabbitmq'::text, 'kafka'::text, 'nats'::text, 'minio'::text, 'memcached'::text, 'etcd'::text, 's3'::text, 'https_api'::text]))),
    CONSTRAINT data_upstream_probes_ok_pair_chk CHECK ((((ok = true) AND (rtt_ms IS NOT NULL) AND (error_class IS NULL)) OR ((ok = false) AND (error_class IS NOT NULL)))),
    CONSTRAINT data_upstream_probes_region_check CHECK ((region ~ '^[a-z0-9_-]{1,32}$'::text)),
    CONSTRAINT data_upstream_probes_rtt_ms_check CHECK (((rtt_ms IS NULL) OR ((rtt_ms >= 0) AND (rtt_ms <= 600000))))
);


--
-- Name: data_upstreams; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.data_upstreams (
    id uuid NOT NULL,
    account_id uuid NOT NULL,
    app_id uuid NOT NULL,
    source text NOT NULL,
    scope text NOT NULL,
    kind text NOT NULL,
    host text NOT NULL,
    port integer NOT NULL,
    host_redacted_hash text NOT NULL,
    declared_region text,
    last_rtt_ms integer,
    last_probed_at timestamp with time zone,
    last_seen_at timestamp with time zone DEFAULT now() NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    deployment_scope text DEFAULT 'default'::text NOT NULL,
    CONSTRAINT data_upstreams_declared_region_check CHECK (((declared_region IS NULL) OR (declared_region ~ '^[a-z0-9_-]{1,32}$'::text))),
    CONSTRAINT data_upstreams_deployment_scope_shape CHECK ((deployment_scope ~ '^[a-z0-9]([a-z0-9-]{1,38})[a-z0-9]$'::text)),
    CONSTRAINT data_upstreams_host_check CHECK (((host ~ '^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)*$'::text) AND (host !~ '^[0-9]+(\.[0-9]+)+$'::text) AND ((length(host) >= 1) AND (length(host) <= 253)))),
    CONSTRAINT data_upstreams_host_redacted_hash_check CHECK (((host_redacted_hash ~ '^[a-f0-9]{64}$'::text) OR (host_redacted_hash = '__unsalted__'::text))),
    CONSTRAINT data_upstreams_kind_check CHECK ((kind = ANY (ARRAY['postgres'::text, 'redis'::text, 'mongo'::text, 'cassandra'::text, 'clickhouse'::text, 'elasticsearch'::text, 'opensearch'::text, 'rabbitmq'::text, 'kafka'::text, 'nats'::text, 'minio'::text, 'memcached'::text, 'etcd'::text, 's3'::text, 'https_api'::text]))),
    CONSTRAINT data_upstreams_last_probed_pair_chk CHECK ((((last_rtt_ms IS NULL) AND (last_probed_at IS NULL)) OR ((last_rtt_ms IS NOT NULL) AND (last_probed_at IS NOT NULL)))),
    CONSTRAINT data_upstreams_last_rtt_ms_check CHECK (((last_rtt_ms IS NULL) OR ((last_rtt_ms >= 0) AND (last_rtt_ms <= 600000)))),
    CONSTRAINT data_upstreams_port_check CHECK (((port >= 1) AND (port <= 65535))),
    CONSTRAINT data_upstreams_scope_check CHECK ((scope ~ '^[a-z0-9]([a-z0-9-]{1,38})[a-z0-9]$'::text)),
    CONSTRAINT data_upstreams_source_check CHECK ((source = ANY (ARRAY['inferred'::text, 'explicit'::text])))
);


--
-- Name: deployment_logs; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.deployment_logs (
    deployment_id uuid NOT NULL,
    seq bigint NOT NULL,
    stream text DEFAULT 'stdout'::text NOT NULL,
    line text NOT NULL,
    written_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: deployment_logs_seq_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.deployment_logs_seq_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: deployment_logs_seq_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.deployment_logs_seq_seq OWNED BY public.deployment_logs.seq;


--
-- Name: deployment_sidecar_layers; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.deployment_sidecar_layers (
    deployment_id uuid NOT NULL,
    sidecar_name text NOT NULL,
    storage_key text NOT NULL,
    bytes bigint NOT NULL,
    content_digest text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT deployment_sidecar_layers_bytes_check CHECK ((bytes >= 0))
);


--
-- Name: deployments; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.deployments (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    app_id uuid NOT NULL,
    build_id uuid,
    image_digest text NOT NULL,
    rootfs_path text,
    rootfs_bytes bigint,
    status text NOT NULL,
    error text,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    kind text DEFAULT 'image'::text NOT NULL,
    source_path text,
    source_bytes bigint,
    handler text,
    log_path text,
    error_code text,
    rootfs_key text DEFAULT ''::text NOT NULL,
    source_url text,
    commit_sha text,
    override_entrypoint text[],
    override_cmd text[],
    override_env jsonb,
    override_env_secrets jsonb,
    override_port integer,
    override_healthcheck jsonb,
    sidecars jsonb DEFAULT '[]'::jsonb NOT NULL,
    min_instances integer DEFAULT 0 NOT NULL,
    scan_result jsonb,
    scan_status text,
    scanned_at timestamp with time zone,
    override_liveness_probe jsonb,
    parked_reason text,
    parked_at timestamp with time zone,
    traffic_percent integer DEFAULT 100 NOT NULL,
    scope text DEFAULT 'default'::text NOT NULL,
    secret_findings jsonb DEFAULT '[]'::jsonb NOT NULL,
    secret_scanned_at timestamp with time zone,
    CONSTRAINT deployments_commit_sha_shape_chk CHECK (((commit_sha IS NULL) OR (((char_length(commit_sha) >= 7) AND (char_length(commit_sha) <= 64)) AND (commit_sha ~ '^[0-9a-f]+$'::text)))),
    CONSTRAINT deployments_kind_check CHECK ((kind = ANY (ARRAY['image'::text, 'tarball'::text, 'dockerfile'::text, 'github'::text, 'preview'::text]))),
    CONSTRAINT deployments_min_instances_chk CHECK (((min_instances >= 0) AND (min_instances <= 100))),
    CONSTRAINT deployments_parked_reason_check CHECK (((parked_reason IS NULL) OR (parked_reason = ANY (ARRAY['liveness_exhausted'::text, 'lifecycle_park'::text, 'admin_park'::text])))),
    CONSTRAINT deployments_scan_status_chk CHECK (((scan_status IS NULL) OR (scan_status = ANY (ARRAY['pending'::text, 'complete'::text, 'failed'::text, 'skipped'::text, 'complete_with_redactions'::text])))),
    CONSTRAINT deployments_scope_shape CHECK ((scope ~ '^[a-z0-9]([a-z0-9-]{1,38})[a-z0-9]$'::text)),
    CONSTRAINT deployments_sidecars_cap_chk CHECK ((jsonb_array_length(sidecars) <= 2)),
    CONSTRAINT deployments_status_check CHECK ((status = ANY (ARRAY['pending'::text, 'building'::text, 'imaging'::text, 'snapshotting'::text, 'live'::text, 'failed'::text, 'superseded'::text]))),
    CONSTRAINT deployments_traffic_percent_chk CHECK (((traffic_percent >= 0) AND (traffic_percent <= 100)))
);


--
-- Name: edge_rules; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.edge_rules (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    account_id uuid NOT NULL,
    app_id uuid NOT NULL,
    match_host text NOT NULL,
    match_path text DEFAULT '/'::text NOT NULL,
    match_methods text[] DEFAULT '{}'::text[] NOT NULL,
    priority smallint DEFAULT 100 NOT NULL,
    enabled boolean DEFAULT true NOT NULL,
    kind text NOT NULL,
    action jsonb NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
CONSTRAINT edge_rules_kind_check CHECK ((kind = ANY (ARRAY['route'::text, 'rewrite'::text, 'redirect'::text, 'headers'::text, 'cors'::text, 'jwt'::text, 'ip'::text, 'validate'::text, 'limit'::text, 'geo'::text, 'maintenance'::text, 'throttle'::text]))),
    CONSTRAINT edge_rules_priority_check CHECK (((priority >= 0) AND (priority <= 10000)))
);


--
-- Name: egress_policy; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.egress_policy (
    id text NOT NULL,
    public_iface text NOT NULL,
    masquerade_cidr text NOT NULL,
    changed_at timestamp with time zone DEFAULT now() NOT NULL,
    overlay_exceptions text[] DEFAULT '{}'::text[] NOT NULL,
    danger_accept_rfc1918_lateral_movement boolean DEFAULT false NOT NULL,
    CONSTRAINT egress_policy_pair_check CHECK (((NOT danger_accept_rfc1918_lateral_movement) OR (COALESCE(array_length(overlay_exceptions, 1), 0) > 0))),
    CONSTRAINT egress_policy_singleton CHECK ((id = 'singleton'::text))
);


--
-- Name: events; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.events (
    id bigint NOT NULL,
    at timestamp with time zone DEFAULT now() NOT NULL,
    actor text NOT NULL,
    kind text NOT NULL,
    subject uuid,
    data jsonb,
    actor_account_id uuid
);


--
-- Name: events_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

ALTER TABLE public.events ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME public.events_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);


--
-- Name: gdpr_requests; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.gdpr_requests (
    id uuid NOT NULL,
    account_id uuid NOT NULL,
    account_email text NOT NULL,
    action text NOT NULL,
    requested_at timestamp with time zone DEFAULT now() NOT NULL,
    completed_at timestamp with time zone,
    org_id uuid,
    request_id text,
    CONSTRAINT gdpr_requests_action_check CHECK ((action = ANY (ARRAY['export'::text, 'delete'::text, 'restore'::text])))
);


--
-- Name: github_installations; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.github_installations (
    account_id uuid NOT NULL,
    installation_id bigint NOT NULL,
    default_branch text NOT NULL,
    sealed_install_token bytea NOT NULL,
    token_expires_at timestamp with time zone NOT NULL,
    sealed_at timestamp with time zone DEFAULT now() NOT NULL,
    audit_github_login text NOT NULL,
    org_id uuid
);


--
-- Name: github_webhook_secrets; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.github_webhook_secrets (
    installation_id bigint NOT NULL,
    secret_value bytea NOT NULL,
    upgraded_at timestamp with time zone DEFAULT now() NOT NULL,
    upgraded_by text DEFAULT 'platform'::text NOT NULL
);


--
-- Name: goose_db_version; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.goose_db_version (
    id integer NOT NULL,
    version_id bigint NOT NULL,
    is_applied boolean NOT NULL,
    tstamp timestamp without time zone DEFAULT now() NOT NULL
);


--
-- Name: goose_db_version_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

ALTER TABLE public.goose_db_version ALTER COLUMN id ADD GENERATED BY DEFAULT AS IDENTITY (
    SEQUENCE NAME public.goose_db_version_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);


--
-- Name: idempotency_keys; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.idempotency_keys (
    key text NOT NULL,
    account_id uuid NOT NULL,
    response_status integer NOT NULL,
    response_body bytea NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: instances; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.instances (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    app_id uuid,
    deployment_id uuid NOT NULL,
    state text NOT NULL,
    netns text,
    guest_uid integer,
    host_ip inet,
    ram_mb integer NOT NULL,
    started_at timestamp with time zone,
    last_request_at timestamp with time zone,
    parked_at timestamp with time zone,
    terminal_at timestamp with time zone,
    node_id uuid NOT NULL,
    wake_id uuid DEFAULT gen_random_uuid() NOT NULL,
    org_id uuid,
    migrated_from_node_id uuid,
    migrated_at timestamp with time zone,
    lease_token text,
    framework_ready_at timestamp with time zone,
    tail_count integer DEFAULT 0 NOT NULL,
    request_count bigint DEFAULT 0 NOT NULL,
    kind text DEFAULT 'wake'::text NOT NULL,
    job_id uuid,
    CONSTRAINT instances_app_or_job_chk CHECK ((((kind = ANY (ARRAY['wake'::text, 'build'::text])) AND (app_id IS NOT NULL) AND (job_id IS NULL)) OR ((kind = 'job_task'::text) AND (app_id IS NULL) AND (job_id IS NOT NULL)))),
    CONSTRAINT instances_kind_check CHECK ((kind = ANY (ARRAY['wake'::text, 'build'::text, 'job_task'::text]))),
    CONSTRAINT instances_migrated_at_chk CHECK (((migrated_at IS NULL) OR (migrated_at <= (now() + '00:01:00'::interval)))),
    CONSTRAINT instances_state_check CHECK ((state = ANY (ARRAY['pending'::text, 'parked'::text, 'waking'::text, 'cold_booting'::text, 'running'::text, 'snapshotting'::text, 'migrating'::text, 'stopped'::text, 'failed'::text, 'evicting_account_deleting'::text])))
);


--
-- Name: invocations; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.invocations (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    app_id uuid NOT NULL,
    account_id uuid NOT NULL,
    source text NOT NULL,
    state text DEFAULT 'pending'::text NOT NULL,
    payload jsonb DEFAULT '{}'::jsonb NOT NULL,
    headers jsonb DEFAULT '{}'::jsonb NOT NULL,
    due_at timestamp with time zone DEFAULT now() NOT NULL,
    method text DEFAULT 'POST'::text NOT NULL,
    path text DEFAULT '/'::text NOT NULL,
    cron_id uuid,
    scheduled_at timestamp with time zone,
    ack_url text,
    result jsonb,
    lease_expires_at timestamp with time zone,
    received_at timestamp with time zone,
    completed_at timestamp with time zone,
    instance_id text,
    attempts integer DEFAULT 0 NOT NULL,
    last_error text,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    org_id uuid,
    outcome text,
    CONSTRAINT invocations_outcome_check CHECK (((outcome IS NULL) OR (outcome = ANY (ARRAY['success'::text, 'failed'::text, 'timeout'::text, 'dead_letter'::text])))),
    CONSTRAINT invocations_source_check CHECK ((source = ANY (ARRAY['async_invoke'::text, 'queue'::text, 'delayed_task'::text, 'cron'::text, 'replay'::text, 'esm'::text]))),
    CONSTRAINT invocations_state_check CHECK ((state = ANY (ARRAY['pending'::text, 'dispatching'::text, 'completed'::text, 'failed'::text, 'cancelled'::text, 'dead_letter'::text])))
);


--
-- Name: invocations_pending_per_app; Type: VIEW; Schema: public; Owner: -
--

CREATE VIEW public.invocations_pending_per_app AS
 SELECT app_id,
    source,
    count(*) AS pending
   FROM public.invocations
  WHERE (state = ANY (ARRAY['pending'::text, 'dispatching'::text]))
  GROUP BY app_id, source;


--
-- Name: invoices; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.invoices (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    account_id uuid NOT NULL,
    provider text NOT NULL,
    provider_invoice_id text NOT NULL,
    number text DEFAULT ''::text NOT NULL,
    status text NOT NULL,
    period_start timestamp with time zone NOT NULL,
    period_end timestamp with time zone NOT NULL,
    subtotal_cents bigint DEFAULT 0 NOT NULL,
    tax_cents bigint DEFAULT 0 NOT NULL,
    total_cents bigint DEFAULT 0 NOT NULL,
    amount_paid_cents bigint DEFAULT 0 NOT NULL,
    currency text DEFAULT 'eur'::text NOT NULL,
    pdf_available boolean DEFAULT false NOT NULL,
    hosted_url text DEFAULT ''::text NOT NULL,
    raw jsonb DEFAULT '{}'::jsonb NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    org_id uuid,
    CONSTRAINT invoices_amount_paid_cents_check CHECK ((amount_paid_cents >= 0)),
    CONSTRAINT invoices_currency_check CHECK ((currency = 'eur'::text)),
    CONSTRAINT invoices_provider_check CHECK ((provider = ANY (ARRAY['stripe'::text, 'paddle'::text]))),
    CONSTRAINT invoices_status_check CHECK ((status = ANY (ARRAY['draft'::text, 'open'::text, 'paid'::text, 'uncollectible'::text, 'void'::text]))),
    CONSTRAINT invoices_subtotal_cents_check CHECK ((subtotal_cents >= 0)),
    CONSTRAINT invoices_tax_cents_check CHECK ((tax_cents >= 0)),
    CONSTRAINT invoices_total_cents_check CHECK ((total_cents >= 0))
);


--
-- Name: job_runs; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.job_runs (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    job_id uuid NOT NULL,
    account_id uuid NOT NULL,
    trigger_kind text NOT NULL,
    env_overrides jsonb DEFAULT '{}'::jsonb NOT NULL,
    tasks integer NOT NULL,
    parallelism integer NOT NULL,
    retry_max integer,
    task_timeout_s integer,
    aggregate_status text DEFAULT 'queued'::text NOT NULL,
    tasks_succeeded integer DEFAULT 0 NOT NULL,
    tasks_failed integer DEFAULT 0 NOT NULL,
    tasks_cancelled integer DEFAULT 0 NOT NULL,
    tasks_running integer DEFAULT 0 NOT NULL,
    started_at timestamp with time zone,
    finished_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT job_runs_aggregate_status_check CHECK ((aggregate_status = ANY (ARRAY['queued'::text, 'running'::text, 'succeeded'::text, 'failed'::text, 'cancelled'::text, 'dead_letter'::text]))),
    CONSTRAINT job_runs_counters_check CHECK (((tasks_succeeded >= 0) AND (tasks_failed >= 0) AND (tasks_cancelled >= 0) AND (tasks_running >= 0) AND ((((tasks_succeeded + tasks_failed) + tasks_cancelled) + tasks_running) <= tasks))),
    CONSTRAINT job_runs_parallelism_check CHECK (((parallelism >= 1) AND (parallelism <= 1000))),
    CONSTRAINT job_runs_retry_max_check CHECK (((retry_max IS NULL) OR ((retry_max >= 0) AND (retry_max <= 10)))),
    CONSTRAINT job_runs_task_timeout_s_check CHECK (((task_timeout_s IS NULL) OR ((task_timeout_s >= 1) AND (task_timeout_s <= 86400)))),
    CONSTRAINT job_runs_tasks_check CHECK (((tasks >= 1) AND (tasks <= 100000))),
    CONSTRAINT job_runs_terminal_pair_chk CHECK ((((finished_at IS NULL) AND (aggregate_status = ANY (ARRAY['queued'::text, 'running'::text]))) OR ((finished_at IS NOT NULL) AND (aggregate_status = ANY (ARRAY['succeeded'::text, 'failed'::text, 'cancelled'::text, 'dead_letter'::text]))))),
    CONSTRAINT job_runs_trigger_kind_check CHECK ((trigger_kind = ANY (ARRAY['manual'::text, 'scheduled'::text, 'triggered'::text])))
);


--
-- Name: job_tasks; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.job_tasks (
    run_id uuid NOT NULL,
    task_index integer NOT NULL,
    status text DEFAULT 'queued'::text NOT NULL,
    attempt integer DEFAULT 1 NOT NULL,
    instance_id uuid,
    error_class text,
    error_message text,
    started_at timestamp with time zone,
    finished_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT job_tasks_attempt_check CHECK (((attempt >= 1) AND (attempt <= 11))),
    CONSTRAINT job_tasks_error_class_check CHECK (((error_class IS NULL) OR (error_class = ANY (ARRAY['timeout'::text, 'refused'::text, 'tls_handshake'::text, 'dns'::text, 'unreachable'::text, 'oom'::text, 'user_error'::text, 'infra'::text])))),
    CONSTRAINT job_tasks_instance_pair_chk CHECK ((((instance_id IS NULL) AND (status = 'queued'::text)) OR (instance_id IS NOT NULL))),
    CONSTRAINT job_tasks_status_check CHECK ((status = ANY (ARRAY['queued'::text, 'claimed'::text, 'succeeded'::text, 'failed'::text, 'timeout'::text, 'cancelled'::text, 'oom'::text]))),
    CONSTRAINT job_tasks_task_index_check CHECK ((task_index >= 0)),
    CONSTRAINT job_tasks_terminal_pair_chk CHECK ((((finished_at IS NULL) AND (status = ANY (ARRAY['queued'::text, 'claimed'::text]))) OR ((finished_at IS NOT NULL) AND (status = ANY (ARRAY['succeeded'::text, 'failed'::text, 'timeout'::text, 'cancelled'::text, 'oom'::text])))))
);


--
-- Name: jobs; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.jobs (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    account_id uuid NOT NULL,
    kind text NOT NULL,
    name text NOT NULL,
    image_ref text NOT NULL,
    ram_mb integer NOT NULL,
    task_timeout_s integer NOT NULL,
    max_parallelism integer NOT NULL,
    retry_max integer NOT NULL,
    env_overrides jsonb DEFAULT '{}'::jsonb NOT NULL,
    status text DEFAULT 'active'::text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT jobs_kind_check CHECK ((kind = ANY (ARRAY['app'::text, 'function'::text]))),
    CONSTRAINT jobs_max_parallelism_check CHECK (((max_parallelism >= 1) AND (max_parallelism <= 1000))),
    CONSTRAINT jobs_name_check CHECK ((name ~ '^[a-z0-9]([a-z0-9-]{1,38})[a-z0-9]$'::text)),
    CONSTRAINT jobs_ram_mb_check CHECK ((ram_mb > 0)),
    CONSTRAINT jobs_retry_max_check CHECK (((retry_max >= 0) AND (retry_max <= 10))),
    CONSTRAINT jobs_status_check CHECK ((status = ANY (ARRAY['active'::text, 'paused'::text, 'deleted'::text]))),
    CONSTRAINT jobs_task_timeout_s_check CHECK (((task_timeout_s >= 1) AND (task_timeout_s <= 86400)))
);


--
-- Name: login_tokens; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.login_tokens (
    token_hash bytea NOT NULL,
    account_id uuid NOT NULL,
    expires_at timestamp with time zone NOT NULL,
    consumed_at timestamp with time zone
);


--
-- Name: oauth_links; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.oauth_links (
    provider text NOT NULL,
    provider_subject text NOT NULL,
    account_id uuid NOT NULL,
    email text NOT NULL,
    email_verified boolean NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: oidc_exchanged_tokens; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.oidc_exchanged_tokens (
    id uuid NOT NULL,
    account_id uuid NOT NULL,
    token_hash bytea NOT NULL,
    expires_at timestamp with time zone NOT NULL,
    issuer_url text NOT NULL,
    subject text NOT NULL,
    audience text[] NOT NULL,
    jti text,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: oidc_trust_policies; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.oidc_trust_policies (
    account_id uuid NOT NULL,
    issuer_url text NOT NULL,
    jwks_url text NOT NULL,
    audience text[] NOT NULL,
    subject_pattern text,
    algorithms text[] NOT NULL,
    required_claims jsonb DEFAULT '{}'::jsonb NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    audit_login text NOT NULL
);


--
-- Name: org_invitations; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.org_invitations (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    org_id uuid NOT NULL,
    email public.citext NOT NULL,
    role text NOT NULL,
    token_hash bytea NOT NULL,
    invited_by_account_id uuid,
    expires_at timestamp with time zone NOT NULL,
    consumed_at timestamp with time zone,
    revoked_at timestamp with time zone,
    accepting_account_id uuid,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT org_invitations_role_chk CHECK ((role = ANY (ARRAY['admin'::text, 'developer'::text, 'viewer'::text, 'billing'::text]))),
    CONSTRAINT org_invitations_state_chk CHECK ((((consumed_at IS NULL) AND (revoked_at IS NULL)) OR ((consumed_at IS NOT NULL) AND (revoked_at IS NULL)) OR ((consumed_at IS NULL) AND (revoked_at IS NOT NULL))))
);


--
-- Name: org_memberships; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.org_memberships (
    org_id uuid NOT NULL,
    account_id uuid NOT NULL,
    role text NOT NULL,
    invited_by_account_id uuid,
    joined_at timestamp with time zone DEFAULT now() NOT NULL,
    removed_at timestamp with time zone,
    CONSTRAINT org_memberships_removed_role_chk CHECK (((removed_at IS NULL) OR (role = ANY (ARRAY['admin'::text, 'developer'::text, 'viewer'::text, 'billing'::text])))),
    CONSTRAINT org_memberships_role_chk CHECK ((role = ANY (ARRAY['owner'::text, 'admin'::text, 'developer'::text, 'viewer'::text, 'billing'::text])))
);


--
-- Name: orgs; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.orgs (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    slug text NOT NULL,
    name text NOT NULL,
    personal_org boolean DEFAULT false NOT NULL,
    personal_owner_account_id uuid,
    plan text DEFAULT 'free'::text NOT NULL,
    status text DEFAULT 'active'::text NOT NULL,
    provider_customer_id text,
    stripe_subscription_item text,
    deleted_pending boolean DEFAULT false NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT orgs_personal_owner_link CHECK ((((personal_org = true) AND (personal_owner_account_id IS NOT NULL)) OR ((personal_org = false) AND (personal_owner_account_id IS NULL)))),
    CONSTRAINT orgs_plan_chk CHECK ((plan = ANY (ARRAY['free'::text, 'hobby'::text, 'pro'::text, 'scale'::text]))),
    CONSTRAINT orgs_slug_shape CHECK ((slug ~ '^[a-z0-9][a-z0-9-]{1,30}[a-z0-9]$'::text)),
    CONSTRAINT orgs_status_chk CHECK ((status = ANY (ARRAY['active'::text, 'past_due'::text, 'suspended'::text, 'deleted_pending'::text])))
);


--
-- Name: paddle_overage_dedupe; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.paddle_overage_dedupe (
    account_id uuid NOT NULL,
    month timestamp with time zone,
    pushed_at timestamp with time zone DEFAULT now() NOT NULL,
    window_start timestamp with time zone NOT NULL,
    state text DEFAULT 'completed'::text NOT NULL,
    claimed_at timestamp with time zone,
    claimed_by text,
    org_id uuid,
    pushed_mb_seconds bigint,
    CONSTRAINT paddle_overage_dedupe_state_check CHECK ((state = ANY (ARRAY['pending'::text, 'completed'::text])))
);


--
-- Name: pg_ratelimit_counters; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.pg_ratelimit_counters (
    scope text NOT NULL,
    subject_id uuid NOT NULL,
    plan text NOT NULL,
    tokens bigint NOT NULL,
    last_refill timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT pg_ratelimit_counters_plan_check CHECK ((plan = ANY (ARRAY['free'::text, 'hobby'::text, 'pro'::text, 'scale'::text]))),
    CONSTRAINT pg_ratelimit_counters_scope_check CHECK ((scope = ANY (ARRAY['app'::text, 'account'::text]))),
    CONSTRAINT pg_ratelimit_counters_tokens_check CHECK ((tokens >= 0))
);


--
-- Name: projects; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.projects (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    account_id uuid NOT NULL,
    slug text NOT NULL,
    repo_full_name text,
    production_branch text,
    install_id bigint,
    scan_source text DEFAULT 'unknown'::text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    org_id uuid,
    CONSTRAINT projects_scan_source_chk CHECK ((scan_source = ANY (ARRAY['compose'::text, 'procfile'::text, 'k8s'::text, 'render'::text, 'fly'::text, 'serverless'::text, 'workspace'::text, 'convention'::text, 'single'::text, 'unknown'::text]))),
    CONSTRAINT projects_slug_shape CHECK ((slug ~ '^[a-z0-9][a-z0-9-]{0,62}$'::text))
);


--
-- Name: recent_build_claims; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.recent_build_claims (
    account_id uuid NOT NULL,
    claimed_at timestamp with time zone DEFAULT now() NOT NULL,
    build_id uuid NOT NULL,
    org_id uuid
);


--
-- Name: release_bundles; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.release_bundles (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    git_sha text NOT NULL,
    manifest_hash text NOT NULL,
    daemon_hashes jsonb DEFAULT '{}'::jsonb NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    applied_at timestamp with time zone,
    CONSTRAINT release_bundles_git_sha_shape CHECK ((git_sha ~ '^[a-f0-9]{40}$'::text)),
    CONSTRAINT release_bundles_manifest_shape CHECK ((manifest_hash ~ '^sha256:[a-f0-9]{64}$'::text))
);


--
-- Name: sessions; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.sessions (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    account_id uuid NOT NULL,
    issued_ip inet,
    issued_ua text,
    issued_at timestamp with time zone DEFAULT now() NOT NULL,
    last_seen_at timestamp with time zone,
    revoked_at timestamp with time zone,
    binding_hash text
);


--
-- Name: snapshot_storage_daily; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.snapshot_storage_daily (
    account_id uuid NOT NULL,
    app_id uuid NOT NULL,
    day date NOT NULL,
    snapshot_bytes bigint DEFAULT 0 NOT NULL,
    layer_bytes bigint DEFAULT 0 NOT NULL,
    computed_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: TABLE snapshot_storage_daily; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON TABLE public.snapshot_storage_daily IS 'Per-(account, app, day) byte totals from snapshots.mem_bytes + disk_bytes + overlay staging. Source: pkg/meter/storage.go cron tick. ADR-049 §B.3. Informational only — not billed today; the future "Pro plan 1 GB included" PR consumes this surface.';


--
-- Name: COLUMN snapshot_storage_daily.snapshot_bytes; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON COLUMN public.snapshot_storage_daily.snapshot_bytes IS 'Σ snapshots.mem_bytes + snapshots.disk_bytes (latest non-stale row per app per day). ADR-049 §B.3. Informational.';


--
-- Name: COLUMN snapshot_storage_daily.layer_bytes; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON COLUMN public.snapshot_storage_daily.layer_bytes IS 'Σ overlay staging bytes per app per day. ADR-049 §B.3. Informational.';


--
-- Name: snapshots; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.snapshots (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    deployment_id uuid NOT NULL,
    fc_version text NOT NULL,
    mem_bytes bigint NOT NULL,
    disk_bytes bigint NOT NULL,
    stale boolean DEFAULT false NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    storage_key text DEFAULT ''::text NOT NULL,
    tier text DEFAULT 'init'::text NOT NULL,
    CONSTRAINT snapshots_tier_check CHECK ((tier = ANY (ARRAY['init'::text, 'warm'::text])))
);


--
-- Name: stripe_push_dedupe; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.stripe_push_dedupe (
    account_id uuid NOT NULL,
    hour timestamp with time zone NOT NULL,
    pushed_at timestamp with time zone DEFAULT now() NOT NULL,
    org_id uuid
);


--
-- Name: tenant_hostnames; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.tenant_hostnames (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    surface_id uuid NOT NULL,
    hostname public.citext NOT NULL,
    challenge_token text DEFAULT ''::text NOT NULL,
    verified_at timestamp with time zone,
    last_check_at timestamp with time zone,
    last_error text,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT tenant_hostnames_hostname_len_chk CHECK (((hostname OPERATOR(public.<>) ''::public.citext) AND (length((hostname)::text) <= 253)))
);


--
-- Name: tenant_surfaces; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.tenant_surfaces (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    account_id uuid NOT NULL,
    app_id uuid,
    name public.citext NOT NULL,
    cert_kind text DEFAULT 'per_host_san'::text NOT NULL,
    status text DEFAULT 'pending'::text NOT NULL,
    cert_state text DEFAULT 'none'::text NOT NULL,
    cert_not_after timestamp with time zone,
    cert_last_error text,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT tenant_surfaces_app_or_not_chk CHECK ((app_id IS NOT NULL)),
CONSTRAINT tenant_surfaces_cert_kind_check CHECK ((cert_kind = ANY (ARRAY['per_host_san'::text, 'shared_wildcard'::text, 'per_host'::text]))),
    CONSTRAINT tenant_surfaces_cert_state_check CHECK ((cert_state = ANY (ARRAY['none'::text, 'pending'::text, 'issued'::text, 'failed'::text]))),
    CONSTRAINT tenant_surfaces_status_check CHECK ((status = ANY (ARRAY['pending'::text, 'active'::text, 'suspended'::text, 'deleted'::text])))
);


--
-- Name: trigger_dead_letter; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.trigger_dead_letter (
    record_id uuid NOT NULL,
    trigger_id uuid NOT NULL,
    reason text NOT NULL,
    routed_to text NOT NULL,
    detail jsonb DEFAULT '{}'::jsonb NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT trigger_dead_letter_reason_check CHECK ((reason = ANY (ARRAY['rate_limited'::text, 'poison_record'::text, 'max_attempts'::text, 'broker_error'::text, 'plan_quota'::text, 'payload_too_large'::text, 'customer_disabled'::text]))),
    CONSTRAINT trigger_dead_letter_routed_to_check CHECK ((routed_to = ANY (ARRAY['drop'::text, 'manual_retry'::text, 'customer_dlq'::text])))
);


--
-- Name: trigger_records; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.trigger_records (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    trigger_id uuid NOT NULL,
    item_identifier text NOT NULL,
    payload jsonb DEFAULT '{}'::jsonb NOT NULL,
    headers jsonb DEFAULT '{}'::jsonb NOT NULL,
    metadata jsonb DEFAULT '{}'::jsonb NOT NULL,
    state text DEFAULT 'pending'::text NOT NULL,
    attempts integer DEFAULT 0 NOT NULL,
    next_fire_at timestamp with time zone DEFAULT now() NOT NULL,
    received_at timestamp with time zone DEFAULT now() NOT NULL,
    last_error text,
    last_dispatched_at timestamp with time zone,
    CONSTRAINT trigger_records_attempts_check CHECK (((attempts >= 0) AND (attempts <= 25))),
    CONSTRAINT trigger_records_state_check CHECK ((state = ANY (ARRAY['pending'::text, 'claimed'::text, 'succeeded'::text, 'retry'::text, 'dead_letter'::text])))
);


--
-- Name: triggers; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.triggers (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    account_id uuid NOT NULL,
    app_id uuid NOT NULL,
    kind text NOT NULL,
    slug text NOT NULL,
    enabled boolean DEFAULT true NOT NULL,
    config jsonb DEFAULT '{}'::jsonb NOT NULL,
    batch_size_max integer DEFAULT 64 NOT NULL,
    batch_window_ms integer DEFAULT 1000 NOT NULL,
    max_attempts integer DEFAULT 5 NOT NULL,
    cron_id uuid,
    source text,
    payload_max_bytes integer DEFAULT 6291456 NOT NULL,
    broker_poison_strategy text DEFAULT 'commit'::text NOT NULL,
    filter_criteria jsonb,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT triggers_batch_size_max_check CHECK (((batch_size_max >= 1) AND (batch_size_max <= 5000))),
    CONSTRAINT triggers_batch_window_ms_check CHECK (((batch_window_ms >= 10) AND (batch_window_ms <= 600000))),
    CONSTRAINT triggers_broker_poison_strategy_check CHECK ((broker_poison_strategy = ANY (ARRAY['commit'::text, 'seek-to-offset'::text]))),
    CONSTRAINT triggers_check CHECK ((((kind = 'cron'::text) AND (cron_id IS NOT NULL) AND (source IS NULL)) OR ((kind <> 'cron'::text) AND (cron_id IS NULL)))),
    CONSTRAINT triggers_kind_check CHECK ((kind = ANY (ARRAY['cron'::text, 'kafka'::text, 'nats'::text, 'redis_streams'::text, 'sqs_compat'::text, 'queue'::text]))),
    CONSTRAINT triggers_max_attempts_check CHECK (((max_attempts >= 1) AND (max_attempts <= 25))),
    CONSTRAINT triggers_payload_max_bytes_check CHECK (((payload_max_bytes >= 1024) AND (payload_max_bytes <= 67108864))),
    CONSTRAINT triggers_source_check CHECK (((source IS NULL) OR (source = ANY (ARRAY['queue'::text, 'delayed_task'::text]))))
);


--
-- Name: usage_daily; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.usage_daily (
    account_id uuid NOT NULL,
    app_id uuid NOT NULL,
    day date NOT NULL,
    mb_seconds bigint DEFAULT 0 NOT NULL,
    requests bigint DEFAULT 0 NOT NULL,
    cpu_usec bigint DEFAULT 0 NOT NULL,
    tx_bytes bigint DEFAULT 0 NOT NULL,
    net_tx_bytes bigint DEFAULT 0 NOT NULL,
    net_rx_bytes bigint DEFAULT 0 NOT NULL,
    cold_boot_count bigint DEFAULT 0 NOT NULL,
    builder_seconds bigint DEFAULT 0 NOT NULL,
    rolled_up_at timestamp with time zone DEFAULT now() NOT NULL,
    org_id uuid,
    tail_seconds bigint DEFAULT 0 NOT NULL
);


--
-- Name: TABLE usage_daily; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON TABLE public.usage_daily IS 'Per-(account, app, day) materialised rollup of usage_minutes. Populated by the meterd cron tick FAAS_ROLLUP_INTERVAL (default 5 min) via INSERT ... SELECT ... GROUP BY with ON CONFLICT additive merge. Read by GET /v1/usage/daily. ADR-048. Informational — not billed.';


--
-- Name: COLUMN usage_daily.cold_boot_count; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON COLUMN public.usage_daily.cold_boot_count IS 'Per-day sum of usage_minutes.cold_boot_count for this (account, app, day). ADR-048. Informational — not billed.';


--
-- Name: COLUMN usage_daily.rolled_up_at; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON COLUMN public.usage_daily.rolled_up_at IS 'Timestamp the meterd cron last wrote this row. Stamped on every ON CONFLICT update so a stuck cron is visible in /v1/usage/daily metadata.';


--
-- Name: usage_minutes; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.usage_minutes (
    account_id uuid NOT NULL,
    app_id uuid,
    instance_id uuid NOT NULL,
    minute timestamp with time zone NOT NULL,
    mb_seconds bigint NOT NULL,
    requests integer DEFAULT 0 NOT NULL,
    cpu_usec bigint DEFAULT 0 NOT NULL,
    tx_bytes bigint DEFAULT 0 NOT NULL,
    net_tx_bytes bigint DEFAULT 0 NOT NULL,
    net_rx_bytes bigint DEFAULT 0 NOT NULL,
    cold_boot_count integer DEFAULT 0 NOT NULL,
    builder_seconds bigint DEFAULT 0 NOT NULL,
    builder_kind text DEFAULT 'none'::text NOT NULL,
    org_id uuid,
    tail_seconds bigint DEFAULT 0 NOT NULL,
    meter_kind text DEFAULT 'app'::text NOT NULL,
    job_id uuid,
    CONSTRAINT usage_minutes_app_or_job_chk CHECK ((((meter_kind = 'app'::text) AND (app_id IS NOT NULL) AND (job_id IS NULL)) OR ((meter_kind = 'job'::text) AND (app_id IS NULL) AND (job_id IS NOT NULL)))),
    CONSTRAINT usage_minutes_meter_kind_check CHECK ((meter_kind = ANY (ARRAY['app'::text, 'job'::text])))
);


--
-- Name: COLUMN usage_minutes.cpu_usec; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON COLUMN public.usage_minutes.cpu_usec IS 'Cumulative host cgroup CPU-µs consumed by the instance during this minute. Source: vmmd cpustats.Cache (cpu.stat usage_usec delta) → schedd instancestats.Poller → meterd Sampler. Measurement only — billing is on plan RAM. issue #279 / PR-B.';


--
-- Name: COLUMN usage_minutes.tx_bytes; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON COLUMN public.usage_minutes.tx_bytes IS 'Cumulative HTTP response body bytes the gateway forwarded for this instance in this minute. Source: pkg/gateway/handler.go statusRecorder.Bytes → per-(instance, minute) ring buffer → meterd Sampler.SampleAndRoll → AppendUsage. ADR-046. Informational — not billed.';


--
-- Name: COLUMN usage_minutes.net_tx_bytes; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON COLUMN public.usage_minutes.net_tx_bytes IS 'Cumulative byte delta on root-side vethHost.rx_bytes for this instance in this minute. Source: vmmd pkg/fcvm/netstats.Cache reading /sys/class/net/<vethHost>/statistics/rx_bytes → vmmd.Stats → schedd instancestats.Poller → meterd Sampler.SampleAndRoll → AppendUsage. ADR-046. Informational — not billed. Unit = interface bytes (includes Ethernet/IP framing).';


--
-- Name: COLUMN usage_minutes.net_rx_bytes; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON COLUMN public.usage_minutes.net_rx_bytes IS 'Cumulative byte delta on root-side vethHost.tx_bytes (root→guest = ingress) for this instance in this minute. Source: vmmd pkg/fcvm/netstats.Cache TX path reading /sys/class/net/<vethHost>/statistics/tx_bytes → vmmd.Stats → schedd instancestats.Poller → meterd Sampler.SampleAndRoll → AppendUsage. ADR-048. Informational — not billed. Unit = interface bytes (includes Ethernet/IP framing).';


--
-- Name: COLUMN usage_minutes.cold_boot_count; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON COLUMN public.usage_minutes.cold_boot_count IS 'Per-minute count of WAKE_RESTORE→WAKE_COLD_BOOT transitions observed for this instance. Source: scheddgrpc.InstanceStatsRow.LastWakeMethod, sampled by meterd Sampler.SampleAndRoll. ADR-048. Informational — not billed. Idempotent on a redelivered tick within the same minute (only the transition counts).';


--
-- Name: COLUMN usage_minutes.builder_seconds; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON COLUMN public.usage_minutes.builder_seconds IS 'Billable builder VM seconds (2-vCPU / 2048-MB per spec §4.5), written once per build at build completion via state.Store.AppendBuilderUsage keyed by build_id. ADR-048. Informational — not billed. NOT counted in CountsForRAM() — runtime GB-RAM-hour billing is unchanged.';


--
-- Name: COLUMN usage_minutes.builder_kind; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON COLUMN public.usage_minutes.builder_kind IS 'Build kind parallel to builds.kind (railpack / dockerfile / tarball); ''none'' for non-build rows. ADR-048. Informational — not billed.';


--
-- Name: usage_monthly; Type: VIEW; Schema: public; Owner: -
--

CREATE VIEW public.usage_monthly AS
 SELECT account_id,
    app_id,
    date_trunc('month'::text, minute) AS month,
    sum(mb_seconds) AS mb_seconds,
    sum(cpu_usec) AS cpu_usec,
    sum(requests) AS requests,
    sum(tx_bytes) AS tx_bytes,
    sum(net_tx_bytes) AS net_tx_bytes,
    sum(net_rx_bytes) AS net_rx_bytes,
    sum(cold_boot_count) AS cold_boot_count,
    sum(
        CASE
            WHEN (builder_kind <> 'none'::text) THEN builder_seconds
            ELSE (0)::bigint
        END) AS builder_seconds
   FROM public.usage_minutes
  GROUP BY account_id, app_id, (date_trunc('month'::text, minute));


--
-- Name: warm_hint; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.warm_hint (
    app_id uuid NOT NULL,
    node_id uuid NOT NULL,
    written_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT warm_hint_written_at_chk CHECK ((written_at <= (now() + '00:01:00'::interval)))
);


--
-- Name: webhook_deliveries; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.webhook_deliveries (
    provider text NOT NULL,
    delivery_id text NOT NULL,
    received_at timestamp with time zone DEFAULT now() NOT NULL,
    expires_at timestamp with time zone NOT NULL,
    CONSTRAINT webhook_deliveries_provider_check CHECK ((provider = ANY (ARRAY['github'::text, 'stripe'::text, 'paddle'::text])))
);


--
-- Name: request_telemetry; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.request_telemetry (
    id uuid DEFAULT gen_random_uuid(),
    account_id uuid NOT NULL,
    app_id uuid NOT NULL,
    deployment_id uuid NOT NULL,
    route text NOT NULL,
    method text NOT NULL,
    status integer NOT NULL,
    latency_ms integer NOT NULL,
    cold_boot boolean DEFAULT false NOT NULL,
    trace_id text,
    spans_summary jsonb,
    received_at timestamp with time zone DEFAULT now() NOT NULL,
    count integer DEFAULT 1 NOT NULL,
    CONSTRAINT request_telemetry_status_check CHECK (((status >= 100) AND (status <= 599))),
    CONSTRAINT request_telemetry_latency_ms_check CHECK ((latency_ms >= 0)),
    CONSTRAINT request_telemetry_trace_id_check CHECK (((trace_id IS NULL) OR (trace_id ~ '^[0-9a-f]{32}$'::text))),
    CONSTRAINT request_telemetry_count_check CHECK ((count >= 1))
)
PARTITION BY RANGE (received_at);


--
-- Name: request_telemetry_default; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.request_telemetry_default
    PARTITION OF public.request_telemetry DEFAULT;


--
-- Name: debug_regression_observations; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.debug_regression_observations (
    app_id uuid NOT NULL,
    deployment_id uuid NOT NULL,
    route text NOT NULL,
    p95_ms integer NOT NULL,
    p95_base_ms integer NOT NULL,
    affected_count integer NOT NULL,
    regression_factor numeric(5,2) NOT NULL,
    first_detected_at timestamp with time zone DEFAULT now() NOT NULL,
    last_detected_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT debug_regression_observations_route_check CHECK (((length(route) >= 1) AND (length(route) <= 256))),
    CONSTRAINT debug_regression_observations_p95_ms_check CHECK ((p95_ms >= 0)),
    CONSTRAINT debug_regression_observations_p95_base_ms_check CHECK ((p95_base_ms >= 0)),
    CONSTRAINT debug_regression_observations_affected_count_check CHECK ((affected_count >= 0)),
    CONSTRAINT debug_regression_observations_regression_factor_check CHECK ((regression_factor >= 1.0))
);


--
-- Name: data_upstream_probes_default; Type: TABLE ATTACH; Schema: public; Owner: -
--

ALTER TABLE ONLY public.data_upstream_probes ATTACH PARTITION public.data_upstream_probes_default DEFAULT;


--
-- Name: compute_node_heartbeats id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.compute_node_heartbeats ALTER COLUMN id SET DEFAULT nextval('public.compute_node_heartbeats_id_seq'::regclass);


--
-- Name: deployment_logs seq; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.deployment_logs ALTER COLUMN seq SET DEFAULT nextval('public.deployment_logs_seq_seq'::regclass);


--
-- Name: account_credits account_credits_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.account_credits
    ADD CONSTRAINT account_credits_pkey PRIMARY KEY (id);


--
-- Name: account_passwords account_passwords_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.account_passwords
    ADD CONSTRAINT account_passwords_pkey PRIMARY KEY (account_id);


--
-- Name: accounts accounts_email_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.accounts
    ADD CONSTRAINT accounts_email_key UNIQUE (email);


--
-- Name: accounts accounts_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.accounts
    ADD CONSTRAINT accounts_pkey PRIMARY KEY (id);


--
-- Name: accounts accounts_stripe_customer_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.accounts
    ADD CONSTRAINT accounts_stripe_customer_id_key UNIQUE (provider_customer_id);


--
-- Name: alert_deliveries alert_deliveries_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.alert_deliveries
    ADD CONSTRAINT alert_deliveries_pkey PRIMARY KEY (id);


--
-- Name: alert_rules alert_rules_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.alert_rules
    ADD CONSTRAINT alert_rules_pkey PRIMARY KEY (id);


--
-- Name: api_keys api_keys_key_sha256_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.api_keys
    ADD CONSTRAINT api_keys_key_sha256_key UNIQUE (key_sha256);


--
-- Name: api_keys api_keys_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.api_keys
    ADD CONSTRAINT api_keys_pkey PRIMARY KEY (id);


--
-- Name: app_envs app_envs_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.app_envs
    ADD CONSTRAINT app_envs_pkey PRIMARY KEY (app_id, scope, key);


--
-- Name: app_error_requests app_error_requests_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.app_error_requests
    ADD CONSTRAINT app_error_requests_pkey PRIMARY KEY (id);


--
-- Name: app_errors app_errors_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.app_errors
    ADD CONSTRAINT app_errors_pkey PRIMARY KEY (id);


--
-- Name: app_registry_credentials app_registry_credentials_app_registry_uq; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.app_registry_credentials
    ADD CONSTRAINT app_registry_credentials_app_registry_uq UNIQUE (app_id, registry);


--
-- Name: app_registry_credentials app_registry_credentials_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.app_registry_credentials
    ADD CONSTRAINT app_registry_credentials_pkey PRIMARY KEY (id);


--
-- Name: app_secrets app_secrets_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.app_secrets
    ADD CONSTRAINT app_secrets_pkey PRIMARY KEY (app_id, key);


--
-- Name: app_trusted_signers app_trusted_signers_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.app_trusted_signers
    ADD CONSTRAINT app_trusted_signers_pkey PRIMARY KEY (app_id, signer_name);


--
-- Name: app_webhook_deliveries app_webhook_deliveries_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.app_webhook_deliveries
    ADD CONSTRAINT app_webhook_deliveries_pkey PRIMARY KEY (id);


--
-- Name: app_webhooks app_webhooks_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.app_webhooks
    ADD CONSTRAINT app_webhooks_pkey PRIMARY KEY (id);


--
-- Name: apps apps_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.apps
    ADD CONSTRAINT apps_pkey PRIMARY KEY (id);


--
-- Name: apps apps_slug_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.apps
    ADD CONSTRAINT apps_slug_key UNIQUE (slug);


--
-- Name: audit_log audit_log_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.audit_log
    ADD CONSTRAINT audit_log_pkey PRIMARY KEY (id);


--
-- Name: build_provenance build_provenance_build_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.build_provenance
    ADD CONSTRAINT build_provenance_build_id_key UNIQUE (build_id);


--
-- Name: build_provenance build_provenance_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.build_provenance
    ADD CONSTRAINT build_provenance_pkey PRIMARY KEY (id);


--
-- Name: builder_usage builder_usage_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.builder_usage
    ADD CONSTRAINT builder_usage_pkey PRIMARY KEY (build_id);


--
-- Name: builds builds_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.builds
    ADD CONSTRAINT builds_pkey PRIMARY KEY (id);


--
-- Name: cli_auth_codes cli_auth_codes_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.cli_auth_codes
    ADD CONSTRAINT cli_auth_codes_pkey PRIMARY KEY (token_hash);


--
-- Name: compute_node_heartbeats compute_node_heartbeats_node_at_uniq; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.compute_node_heartbeats
    ADD CONSTRAINT compute_node_heartbeats_node_at_uniq UNIQUE (node_id, received_at);


--
-- Name: compute_node_heartbeats compute_node_heartbeats_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.compute_node_heartbeats
    ADD CONSTRAINT compute_node_heartbeats_pkey PRIMARY KEY (id);


--
-- Name: compute_node_keys compute_node_keys_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.compute_node_keys
    ADD CONSTRAINT compute_node_keys_pkey PRIMARY KEY (compute_node_id, key_id);


--
-- Name: compute_nodes compute_nodes_name_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.compute_nodes
    ADD CONSTRAINT compute_nodes_name_key UNIQUE (name);


--
-- Name: compute_nodes compute_nodes_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.compute_nodes
    ADD CONSTRAINT compute_nodes_pkey PRIMARY KEY (id);


--
-- Name: cors_presets cors_presets_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.cors_presets
    ADD CONSTRAINT cors_presets_pkey PRIMARY KEY (id);


--
-- Name: cors_presets_unique_name; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX cors_presets_unique_name ON public.cors_presets USING btree (account_id, COALESCE(app_id, '00000000-0000-0000-0000-000000000000'::uuid), name);


--
-- Name: credit_ledger credit_ledger_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.credit_ledger
    ADD CONSTRAINT credit_ledger_pkey PRIMARY KEY (id);


--
-- Name: cron_fire_now_requests cron_fire_now_requests_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.cron_fire_now_requests
    ADD CONSTRAINT cron_fire_now_requests_pkey PRIMARY KEY (id);


--
-- Name: crons crons_app_schedule_path_unique; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.crons
    ADD CONSTRAINT crons_app_schedule_path_unique UNIQUE (app_id, schedule, path);


--
-- Name: crons crons_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.crons
    ADD CONSTRAINT crons_pkey PRIMARY KEY (id);


--
-- Name: custom_domains custom_domains_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.custom_domains
    ADD CONSTRAINT custom_domains_pkey PRIMARY KEY (domain);


--
-- Name: data_upstream_probes data_upstream_probes_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.data_upstream_probes
    ADD CONSTRAINT data_upstream_probes_pkey PRIMARY KEY (id, sampled_at);


--
-- Name: data_upstream_probes_default data_upstream_probes_default_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.data_upstream_probes_default
    ADD CONSTRAINT data_upstream_probes_default_pkey PRIMARY KEY (id, sampled_at);


--
-- Name: data_upstreams data_upstreams_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.data_upstreams
    ADD CONSTRAINT data_upstreams_pkey PRIMARY KEY (id);


--
-- Name: deployment_logs deployment_logs_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.deployment_logs
    ADD CONSTRAINT deployment_logs_pkey PRIMARY KEY (deployment_id, seq);


--
-- Name: deployment_sidecar_layers deployment_sidecar_layers_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.deployment_sidecar_layers
    ADD CONSTRAINT deployment_sidecar_layers_pkey PRIMARY KEY (deployment_id, sidecar_name);


--
-- Name: deployments deployments_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.deployments
    ADD CONSTRAINT deployments_pkey PRIMARY KEY (id);


--
-- Name: edge_rules edge_rules_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.edge_rules
    ADD CONSTRAINT edge_rules_pkey PRIMARY KEY (id);


--
-- Name: egress_policy egress_policy_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.egress_policy
    ADD CONSTRAINT egress_policy_pkey PRIMARY KEY (id);


--
-- Name: events events_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.events
    ADD CONSTRAINT events_pkey PRIMARY KEY (id);


--
-- Name: gdpr_requests gdpr_requests_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.gdpr_requests
    ADD CONSTRAINT gdpr_requests_pkey PRIMARY KEY (id);


--
-- Name: github_installations github_installations_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.github_installations
    ADD CONSTRAINT github_installations_pkey PRIMARY KEY (account_id);


--
-- Name: github_webhook_secrets github_webhook_secrets_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.github_webhook_secrets
    ADD CONSTRAINT github_webhook_secrets_pkey PRIMARY KEY (installation_id);


--
-- Name: goose_db_version goose_db_version_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.goose_db_version
    ADD CONSTRAINT goose_db_version_pkey PRIMARY KEY (id);


--
-- Name: idempotency_keys idempotency_keys_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.idempotency_keys
    ADD CONSTRAINT idempotency_keys_pkey PRIMARY KEY (account_id, key);


--
-- Name: instances instances_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.instances
    ADD CONSTRAINT instances_pkey PRIMARY KEY (id);


--
-- Name: invocations invocations_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.invocations
    ADD CONSTRAINT invocations_pkey PRIMARY KEY (id);


--
-- Name: invoices invoices_account_id_provider_provider_invoice_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.invoices
    ADD CONSTRAINT invoices_account_id_provider_provider_invoice_id_key UNIQUE (account_id, provider, provider_invoice_id);


--
-- Name: invoices invoices_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.invoices
    ADD CONSTRAINT invoices_pkey PRIMARY KEY (id);


--
-- Name: job_runs job_runs_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.job_runs
    ADD CONSTRAINT job_runs_pkey PRIMARY KEY (id);


--
-- Name: job_tasks job_tasks_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.job_tasks
    ADD CONSTRAINT job_tasks_pkey PRIMARY KEY (run_id, task_index);


--
-- Name: jobs jobs_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.jobs
    ADD CONSTRAINT jobs_pkey PRIMARY KEY (id);


--
-- Name: login_tokens login_tokens_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.login_tokens
    ADD CONSTRAINT login_tokens_pkey PRIMARY KEY (token_hash);


--
-- Name: oauth_links oauth_links_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.oauth_links
    ADD CONSTRAINT oauth_links_pkey PRIMARY KEY (provider, provider_subject);


--
-- Name: oidc_exchanged_tokens oidc_exchanged_tokens_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.oidc_exchanged_tokens
    ADD CONSTRAINT oidc_exchanged_tokens_pkey PRIMARY KEY (id);


--
-- Name: oidc_exchanged_tokens oidc_exchanged_tokens_token_hash_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.oidc_exchanged_tokens
    ADD CONSTRAINT oidc_exchanged_tokens_token_hash_key UNIQUE (token_hash);


--
-- Name: oidc_trust_policies oidc_trust_policies_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.oidc_trust_policies
    ADD CONSTRAINT oidc_trust_policies_pkey PRIMARY KEY (account_id, issuer_url);


--
-- Name: org_invitations org_invitations_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.org_invitations
    ADD CONSTRAINT org_invitations_pkey PRIMARY KEY (id);


--
-- Name: org_invitations org_invitations_token_uniq; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.org_invitations
    ADD CONSTRAINT org_invitations_token_uniq UNIQUE (token_hash);


--
-- Name: org_memberships org_memberships_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.org_memberships
    ADD CONSTRAINT org_memberships_pkey PRIMARY KEY (org_id, account_id);


--
-- Name: orgs orgs_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.orgs
    ADD CONSTRAINT orgs_pkey PRIMARY KEY (id);


--
-- Name: paddle_overage_dedupe paddle_overage_dedupe_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.paddle_overage_dedupe
    ADD CONSTRAINT paddle_overage_dedupe_pkey PRIMARY KEY (account_id, window_start);


--
-- Name: pg_ratelimit_counters pg_ratelimit_counters_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.pg_ratelimit_counters
    ADD CONSTRAINT pg_ratelimit_counters_pkey PRIMARY KEY (scope, subject_id, plan);


--
-- Name: projects projects_account_slug_uniq; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.projects
    ADD CONSTRAINT projects_account_slug_uniq UNIQUE (account_id, slug);


--
-- Name: projects projects_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.projects
    ADD CONSTRAINT projects_pkey PRIMARY KEY (id);


--
-- Name: release_bundles release_bundles_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.release_bundles
    ADD CONSTRAINT release_bundles_pkey PRIMARY KEY (id);


--
-- Name: sessions sessions_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.sessions
    ADD CONSTRAINT sessions_pkey PRIMARY KEY (id);


--
-- Name: snapshot_storage_daily snapshot_storage_daily_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.snapshot_storage_daily
    ADD CONSTRAINT snapshot_storage_daily_pkey PRIMARY KEY (account_id, app_id, day);


--
-- Name: snapshots snapshots_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.snapshots
    ADD CONSTRAINT snapshots_pkey PRIMARY KEY (id);


--
-- Name: stripe_push_dedupe stripe_push_dedupe_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.stripe_push_dedupe
    ADD CONSTRAINT stripe_push_dedupe_pkey PRIMARY KEY (account_id, hour);


--
-- Name: tenant_hostnames tenant_hostnames_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.tenant_hostnames
    ADD CONSTRAINT tenant_hostnames_pkey PRIMARY KEY (id);


--
-- Name: tenant_surfaces tenant_surfaces_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.tenant_surfaces
    ADD CONSTRAINT tenant_surfaces_pkey PRIMARY KEY (id);


--
-- Name: trigger_dead_letter trigger_dead_letter_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.trigger_dead_letter
    ADD CONSTRAINT trigger_dead_letter_pkey PRIMARY KEY (record_id);


--
-- Name: trigger_records trigger_records_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.trigger_records
    ADD CONSTRAINT trigger_records_pkey PRIMARY KEY (id);


--
-- Name: trigger_records trigger_records_trigger_id_item_identifier_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.trigger_records
    ADD CONSTRAINT trigger_records_trigger_id_item_identifier_key UNIQUE (trigger_id, item_identifier);


--
-- Name: triggers triggers_app_id_slug_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.triggers
    ADD CONSTRAINT triggers_app_id_slug_key UNIQUE (app_id, slug);


--
-- Name: triggers triggers_cron_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.triggers
    ADD CONSTRAINT triggers_cron_id_key UNIQUE (cron_id);


--
-- Name: triggers triggers_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.triggers
    ADD CONSTRAINT triggers_pkey PRIMARY KEY (id);


--
-- Name: usage_daily usage_daily_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.usage_daily
    ADD CONSTRAINT usage_daily_pkey PRIMARY KEY (account_id, app_id, day);


--
-- Name: usage_minutes usage_minutes_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.usage_minutes
    ADD CONSTRAINT usage_minutes_pkey PRIMARY KEY (instance_id, minute);


--
-- Name: warm_hint warm_hint_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.warm_hint
    ADD CONSTRAINT warm_hint_pkey PRIMARY KEY (app_id);


--
-- Name: webhook_deliveries webhook_deliveries_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.webhook_deliveries
    ADD CONSTRAINT webhook_deliveries_pkey PRIMARY KEY (provider, delivery_id);


--
-- Name: account_credits_account_active_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX account_credits_account_active_idx ON public.account_credits USING btree (account_id, expires_at, cents_remaining) WHERE (cents_remaining > 0);


--
-- Name: accounts_deletion_pending_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX accounts_deletion_pending_idx ON public.accounts USING btree (deletion_requested_at) WHERE (status = 'deleted_pending'::text);


--
-- Name: accounts_mfa_required_pending_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX accounts_mfa_required_pending_idx ON public.accounts USING btree (id) WHERE ((mfa_required = true) AND (mfa_enrolled_at IS NULL));


--
-- Name: accounts_past_due_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX accounts_past_due_idx ON public.accounts USING btree (past_due_at) WHERE (status = 'past_due'::text);


--
-- Name: alert_deliveries_idempotency_uniq; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX alert_deliveries_idempotency_uniq ON public.alert_deliveries USING btree (idempotency_key);


--
-- Name: alert_deliveries_rule_fired_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX alert_deliveries_rule_fired_idx ON public.alert_deliveries USING btree (rule_id, fired_at DESC);


--
-- Name: alert_rules_account_name_uniq; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX alert_rules_account_name_uniq ON public.alert_rules USING btree (account_id, name);


--
-- Name: alert_rules_enabled_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX alert_rules_enabled_idx ON public.alert_rules USING btree (account_id) WHERE (enabled = true);


--
-- Name: alert_rules_org_id_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX alert_rules_org_id_idx ON public.alert_rules USING btree (org_id) WHERE (org_id IS NOT NULL);


--
-- Name: api_keys_account_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX api_keys_account_idx ON public.api_keys USING btree (account_id);


--
-- Name: api_keys_account_status_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX api_keys_account_status_idx ON public.api_keys USING btree (account_id, status);


--
-- Name: api_keys_active_grace_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX api_keys_active_grace_idx ON public.api_keys USING btree (account_id) WHERE (status = ANY (ARRAY['active'::text, 'grace'::text]));


--
-- Name: api_keys_org_id_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX api_keys_org_id_idx ON public.api_keys USING btree (org_id) WHERE (org_id IS NOT NULL);


--
-- Name: api_keys_rotated_from_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX api_keys_rotated_from_idx ON public.api_keys USING btree (rotated_from_id) WHERE (rotated_from_id IS NOT NULL);


--
-- Name: app_envs_account_app_scope_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX app_envs_account_app_scope_idx ON public.app_envs USING btree (account_id, app_id, scope);


--
-- Name: app_envs_account_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX app_envs_account_idx ON public.app_envs USING btree (account_id);


--
-- Name: app_envs_app_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX app_envs_app_idx ON public.app_envs USING btree (app_id);


--
-- Name: app_envs_org_id_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX app_envs_org_id_idx ON public.app_envs USING btree (org_id) WHERE (org_id IS NOT NULL);


--
-- Name: app_error_requests_drill_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX app_error_requests_drill_idx ON public.app_error_requests USING btree (account_id, app_id, fingerprint, received_at DESC, request_id DESC);


--
-- Name: app_error_requests_retention_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX app_error_requests_retention_idx ON public.app_error_requests USING btree (account_id, received_at);


--
-- Name: app_errors_account_app_fp_last_seen_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX app_errors_account_app_fp_last_seen_idx ON public.app_errors USING btree (account_id, app_id, fingerprint, last_seen_at DESC);


--
-- Name: app_errors_account_app_last_seen_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX app_errors_account_app_last_seen_idx ON public.app_errors USING btree (account_id, app_id, last_seen_at DESC);


--
-- Name: app_errors_dedupe_uniq; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX app_errors_dedupe_uniq ON public.app_errors USING btree (account_id, app_id, fingerprint);


--
-- Name: app_registry_credentials_account_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX app_registry_credentials_account_idx ON public.app_registry_credentials USING btree (account_id);


--
-- Name: app_secrets_account_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX app_secrets_account_idx ON public.app_secrets USING btree (account_id);


--
-- Name: app_secrets_app_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX app_secrets_app_idx ON public.app_secrets USING btree (app_id);


--
-- Name: app_secrets_kid_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX app_secrets_kid_idx ON public.app_secrets USING btree (kid) WHERE (kid IS NOT NULL);


--
-- Name: app_secrets_org_id_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX app_secrets_org_id_idx ON public.app_secrets USING btree (org_id) WHERE (org_id IS NOT NULL);


--
-- Name: app_trusted_signers_app_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX app_trusted_signers_app_idx ON public.app_trusted_signers USING btree (app_id);


--
-- Name: app_webhook_deliveries_account_created_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX app_webhook_deliveries_account_created_idx ON public.app_webhook_deliveries USING btree (account_id, created_at DESC);


--
-- Name: app_webhook_deliveries_pending_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX app_webhook_deliveries_pending_idx ON public.app_webhook_deliveries USING btree (account_id, next_attempt_at) WHERE (status = ANY (ARRAY['pending'::text, 'in_flight'::text]));


--
-- Name: app_webhooks_account_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX app_webhooks_account_idx ON public.app_webhooks USING btree (account_id);


--
-- Name: app_webhooks_app_target_uniq; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX app_webhooks_app_target_uniq ON public.app_webhooks USING btree (app_id, target_url);


--
-- Name: apps_account_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX apps_account_idx ON public.apps USING btree (account_id, status);


--
-- Name: apps_github_install_account_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX apps_github_install_account_idx ON public.apps USING btree (github_install_account_id) WHERE (github_install_account_id IS NOT NULL);


--
-- Name: apps_github_install_account_uniq; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX apps_github_install_account_uniq ON public.apps USING btree (github_install_account_id, github_install_binding_id) WHERE ((github_install_account_id IS NOT NULL) AND (github_install_binding_id IS NOT NULL));


--
-- Name: apps_github_install_id_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX apps_github_install_id_idx ON public.apps USING btree (github_install_id) WHERE (github_install_id IS NOT NULL);


--
-- Name: apps_github_install_repo_branch_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX apps_github_install_repo_branch_idx ON public.apps USING btree (github_repo_full_name, github_production_branch) WHERE ((github_repo_full_name IS NOT NULL) AND (github_production_branch IS NOT NULL));


--
-- Name: apps_maintenance_mode_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX apps_maintenance_mode_idx ON public.apps USING btree (maintenance_mode) WHERE (maintenance_mode = true);


--
-- Name: apps_node_id_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX apps_node_id_idx ON public.apps USING btree (node_id);


--
-- Name: apps_node_id_status_partial_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX apps_node_id_status_partial_idx ON public.apps USING btree (node_id, status) WHERE ((node_id IS NOT NULL) AND (status = ANY (ARRAY['active'::text, 'evicted_cold'::text])));


--
-- Name: apps_org_id_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX apps_org_id_idx ON public.apps USING btree (org_id) WHERE (org_id IS NOT NULL);


--
-- Name: apps_overflow_node_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX apps_overflow_node_idx ON public.apps USING btree (overflow_node) WHERE (overflow_node IS NOT NULL);


--
-- Name: apps_preview_expires_at_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX apps_preview_expires_at_idx ON public.apps USING btree (preview_expires_at) WHERE (preview_expires_at IS NOT NULL);


--
-- Name: apps_preview_of_slug_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX apps_preview_of_slug_idx ON public.apps USING btree (preview_of_slug) WHERE (preview_of_slug IS NOT NULL);


--
-- Name: apps_project_workload_uniq; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX apps_project_workload_uniq ON public.apps USING btree (project_id, workload_name) WHERE (project_id IS NOT NULL);


--
-- Name: apps_reassigned_at_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX apps_reassigned_at_idx ON public.apps USING btree (reassigned_at) WHERE (reassigned_at IS NOT NULL);


--
-- Name: apps_route_metrics_enabled_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX apps_route_metrics_enabled_idx ON public.apps USING btree (route_metrics_enabled) WHERE (route_metrics_enabled = true);


--
-- Name: apps_streaming_enabled_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX apps_streaming_enabled_idx ON public.apps USING btree (streaming_enabled) WHERE (streaming_enabled = true);


--
-- Name: apps_websocket_enabled_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX apps_websocket_enabled_idx ON public.apps USING btree (websocket_enabled) WHERE (websocket_enabled = true);


--
-- Name: audit_log_account_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX audit_log_account_idx ON public.audit_log USING btree (account_id, received_at DESC) WHERE (account_id IS NOT NULL);


--
-- Name: audit_log_received_at_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX audit_log_received_at_idx ON public.audit_log USING btree (received_at DESC);


--
-- Name: build_provenance_build_id_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX build_provenance_build_id_idx ON public.build_provenance USING btree (build_id);


--
-- Name: build_provenance_framework_version_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX build_provenance_framework_version_idx ON public.build_provenance USING btree (framework_version) WHERE (framework_version IS NOT NULL);


--
-- Name: builder_usage_account_finished_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX builder_usage_account_finished_idx ON public.builder_usage USING btree (account_id, finished_at DESC);


--
-- Name: builder_usage_org_id_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX builder_usage_org_id_idx ON public.builder_usage USING btree (org_id) WHERE (org_id IS NOT NULL);


--
-- Name: builds_deployment_started_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX builds_deployment_started_idx ON public.builds USING btree (deployment_id, started_at DESC NULLS LAST);


--
-- Name: builds_running_started_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX builds_running_started_idx ON public.builds USING btree (started_at) WHERE (status = 'running'::text);


--
-- Name: cli_auth_codes_pending_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX cli_auth_codes_pending_idx ON public.cli_auth_codes USING btree (status, expires_at) WHERE (status = 'pending'::text);


--
-- Name: compute_node_heartbeats_node_at_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX compute_node_heartbeats_node_at_idx ON public.compute_node_heartbeats USING btree (node_id, received_at DESC);


--
-- Name: compute_node_keys_node_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX compute_node_keys_node_idx ON public.compute_node_keys USING btree (compute_node_id);


--
-- Name: compute_nodes_active_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX compute_nodes_active_idx ON public.compute_nodes USING btree (name) WHERE (active = true);


--
-- Name: compute_nodes_region_zone_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX compute_nodes_region_zone_idx ON public.compute_nodes USING btree (region, zone) WHERE (active = true);


--
-- Name: cors_presets_account_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX cors_presets_account_idx ON public.cors_presets USING btree (account_id);


--
-- Name: credit_ledger_account_created_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX credit_ledger_account_created_idx ON public.credit_ledger USING btree (account_id, created_at DESC);


--
-- Name: credit_ledger_invoice_credit_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX credit_ledger_invoice_credit_idx ON public.credit_ledger USING btree (provider_invoice_id, credit_id) WHERE (provider_invoice_id IS NOT NULL);


--
-- Name: cron_fire_now_requests_cron_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX cron_fire_now_requests_cron_idx ON public.cron_fire_now_requests USING btree (cron_id, requested_at DESC);


--
-- Name: cron_fire_now_requests_pending_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX cron_fire_now_requests_pending_idx ON public.cron_fire_now_requests USING btree (status, requested_at) WHERE (status = 'pending'::text);


--
-- Name: crons_app_full_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX crons_app_full_idx ON public.crons USING btree (app_id);


--
-- Name: crons_app_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX crons_app_idx ON public.crons USING btree (app_id) WHERE enabled;


--
-- Name: crons_org_id_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX crons_org_id_idx ON public.crons USING btree (org_id) WHERE (org_id IS NOT NULL);


--
-- Name: custom_domains_org_id_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX custom_domains_org_id_idx ON public.custom_domains USING btree (org_id) WHERE (org_id IS NOT NULL);


--
-- Name: custom_domains_unverified_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX custom_domains_unverified_idx ON public.custom_domains USING btree (domain) WHERE (verified_at IS NULL);


--
-- Name: data_upstreams_app_created_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX data_upstreams_app_created_idx ON public.data_upstreams USING btree (app_id, created_at DESC, id DESC);


--
-- Name: data_upstreams_dedupe_uniq; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX data_upstreams_dedupe_uniq ON public.data_upstreams USING btree (app_id, scope, deployment_scope, kind, host, port);


--
-- Name: data_upstreams_host_redacted_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX data_upstreams_host_redacted_idx ON public.data_upstreams USING btree (host_redacted_hash);


--
-- Name: deployment_logs_seq_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX deployment_logs_seq_idx ON public.deployment_logs USING btree (deployment_id, seq DESC);


--
-- Name: deployment_sidecar_layers_storage_key_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX deployment_sidecar_layers_storage_key_idx ON public.deployment_sidecar_layers USING btree (storage_key);


--
-- Name: deployments_app_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX deployments_app_idx ON public.deployments USING btree (app_id, created_at DESC);


--
-- Name: deployments_app_scan_complete_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX deployments_app_scan_complete_idx ON public.deployments USING btree (app_id, scanned_at DESC) WHERE (scan_status = 'complete'::text);


--
-- Name: deployments_app_scope_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX deployments_app_scope_idx ON public.deployments USING btree (app_id, scope, created_at DESC);


--
-- Name: deployments_app_scope_live_uniq; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX deployments_app_scope_live_uniq ON public.deployments USING btree (app_id, scope) WHERE (status = 'live'::text);


--
-- Name: deployments_failed_error_code_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX deployments_failed_error_code_idx ON public.deployments USING btree (error_code) WHERE (status = 'failed'::text);


--
-- Name: deployments_live_traffic_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX deployments_live_traffic_idx ON public.deployments USING btree (app_id) INCLUDE (traffic_percent, id) WHERE (status = 'live'::text);


--
-- Name: edge_rules_app_id_enabled_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX edge_rules_app_id_enabled_idx ON public.edge_rules USING btree (app_id) WHERE enabled;


--
-- Name: edge_rules_enabled_match_host_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX edge_rules_enabled_match_host_idx ON public.edge_rules USING btree (match_host) WHERE enabled;


--
-- Name: edge_rules_match_host_pattern_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX edge_rules_match_host_pattern_idx ON public.edge_rules USING btree (match_host text_pattern_ops) WHERE enabled;


--
-- Name: events_actor_account_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX events_actor_account_idx ON public.events USING btree (actor_account_id) WHERE (actor_account_id IS NOT NULL);


--
-- Name: events_kind_at_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX events_kind_at_idx ON public.events USING btree (kind, at DESC);


--
-- Name: events_sidecar_name_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX events_sidecar_name_idx ON public.events USING btree (((data ->> 'sidecar_name'::text))) WHERE (kind = ANY (ARRAY['wake.sidecar_init_exit'::text, 'wake.sidecar_restart'::text]));


--
-- Name: events_subject_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX events_subject_idx ON public.events USING btree (subject, at DESC);


--
-- Name: events_wake_id_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX events_wake_id_idx ON public.events USING btree (((data ->> 'wake_id'::text))) WHERE ((data ->> 'wake_id'::text) IS NOT NULL);


--
-- Name: gdpr_requests_account_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX gdpr_requests_account_idx ON public.gdpr_requests USING btree (account_id, requested_at DESC);


--
-- Name: gdpr_requests_org_id_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX gdpr_requests_org_id_idx ON public.gdpr_requests USING btree (org_id) WHERE (org_id IS NOT NULL);


--
-- Name: gdpr_requests_request_id_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX gdpr_requests_request_id_idx ON public.gdpr_requests USING btree (account_id, request_id) WHERE (request_id IS NOT NULL);


--
-- Name: github_installations_login_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX github_installations_login_idx ON public.github_installations USING btree (audit_github_login);


--
-- Name: github_installations_org_id_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX github_installations_org_id_idx ON public.github_installations USING btree (org_id) WHERE (org_id IS NOT NULL);


--
-- Name: instances_app_deployment_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX instances_app_deployment_idx ON public.instances USING btree (app_id, deployment_id) WHERE (state = ANY (ARRAY['RUNNING'::text, 'WAKING'::text, 'COLD_BOOTING'::text]));


--
-- Name: instances_app_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX instances_app_idx ON public.instances USING btree (app_id, state);


--
-- Name: instances_job_id_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX instances_job_id_idx ON public.instances USING btree (job_id) WHERE (job_id IS NOT NULL);


--
-- Name: instances_kind_job_task_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX instances_kind_job_task_idx ON public.instances USING btree (job_id) WHERE (kind = 'job_task'::text);


--
-- Name: instances_migrated_from_node_id_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX instances_migrated_from_node_id_idx ON public.instances USING btree (migrated_from_node_id) WHERE (migrated_from_node_id IS NOT NULL);


--
-- Name: instances_org_id_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX instances_org_id_idx ON public.instances USING btree (org_id) WHERE (org_id IS NOT NULL);


--
-- Name: instances_reaper_state_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX instances_reaper_state_idx ON public.instances USING btree (started_at DESC) WHERE (state = ANY (ARRAY['running'::text, 'waking'::text, 'cold_booting'::text, 'snapshotting'::text]));


--
-- Name: instances_terminal_at_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX instances_terminal_at_idx ON public.instances USING btree (terminal_at) WHERE (state = ANY (ARRAY['stopped'::text, 'failed'::text]));


--
-- Name: instances_wake_id_app_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX instances_wake_id_app_idx ON public.instances USING btree (app_id, wake_id) WHERE (state = ANY (ARRAY['waking'::text, 'cold_booting'::text, 'running'::text, 'snapshotting'::text, 'parked'::text]));


--
-- Name: instances_watchdog_state_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX instances_watchdog_state_idx ON public.instances USING btree (state, started_at) WHERE (state = ANY (ARRAY['waking'::text, 'cold_booting'::text, 'snapshotting'::text]));


--
-- Name: invocations_app_dead_letter_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX invocations_app_dead_letter_idx ON public.invocations USING btree (app_id, created_at DESC) WHERE (state = 'dead_letter'::text);


--
-- Name: invocations_app_pending_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX invocations_app_pending_idx ON public.invocations USING btree (app_id, source, state) WHERE (state = ANY (ARRAY['pending'::text, 'dispatching'::text]));


--
-- Name: invocations_cron_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX invocations_cron_idx ON public.invocations USING btree (cron_id, created_at DESC) WHERE (cron_id IS NOT NULL);


--
-- Name: invocations_delayed_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX invocations_delayed_idx ON public.invocations USING btree (app_id, scheduled_at) WHERE (source = 'delayed_task'::text);


--
-- Name: invocations_due_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX invocations_due_idx ON public.invocations USING btree (due_at) WHERE (state = 'pending'::text);


--
-- Name: invocations_instance_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX invocations_instance_idx ON public.invocations USING btree (instance_id, due_at) WHERE (state = 'dispatching'::text);


--
-- Name: invocations_org_id_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX invocations_org_id_idx ON public.invocations USING btree (org_id) WHERE (org_id IS NOT NULL);


--
-- Name: invoices_account_period_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX invoices_account_period_idx ON public.invoices USING btree (account_id, period_end DESC, id DESC);


--
-- Name: invoices_org_id_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX invoices_org_id_idx ON public.invoices USING btree (org_id) WHERE (org_id IS NOT NULL);


--
-- Name: job_runs_account_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX job_runs_account_idx ON public.job_runs USING btree (account_id, created_at DESC);


--
-- Name: job_runs_active_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX job_runs_active_idx ON public.job_runs USING btree (account_id, id) WHERE (aggregate_status = ANY (ARRAY['queued'::text, 'running'::text]));


--
-- Name: job_runs_job_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX job_runs_job_idx ON public.job_runs USING btree (job_id, created_at DESC);


--
-- Name: job_tasks_ready_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX job_tasks_ready_idx ON public.job_tasks USING btree (created_at, run_id, task_index) WHERE (status = 'queued'::text);


--
-- Name: job_tasks_run_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX job_tasks_run_idx ON public.job_tasks USING btree (run_id, task_index);


--
-- Name: jobs_account_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX jobs_account_idx ON public.jobs USING btree (account_id, created_at DESC);


--
-- Name: jobs_account_name_uniq; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX jobs_account_name_uniq ON public.jobs USING btree (account_id, name) WHERE (status <> 'deleted'::text);


--
-- Name: login_tokens_account_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX login_tokens_account_idx ON public.login_tokens USING btree (account_id, expires_at);


--
-- Name: oauth_links_account_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX oauth_links_account_idx ON public.oauth_links USING btree (account_id);


--
-- Name: oidc_exchanged_tokens_expires_at_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX oidc_exchanged_tokens_expires_at_idx ON public.oidc_exchanged_tokens USING btree (expires_at);


--
-- Name: org_invitations_email_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX org_invitations_email_idx ON public.org_invitations USING btree (org_id, email) WHERE (consumed_at IS NULL);


--
-- Name: org_invitations_pending_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX org_invitations_pending_idx ON public.org_invitations USING btree (org_id) WHERE ((consumed_at IS NULL) AND (revoked_at IS NULL));


--
-- Name: org_memberships_account_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX org_memberships_account_idx ON public.org_memberships USING btree (account_id) WHERE (removed_at IS NULL);


--
-- Name: org_memberships_one_owner_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX org_memberships_one_owner_idx ON public.org_memberships USING btree (org_id) WHERE ((role = 'owner'::text) AND (removed_at IS NULL));


--
-- Name: orgs_created_at_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX orgs_created_at_idx ON public.orgs USING btree (created_at DESC);


--
-- Name: orgs_one_personal_per_account_uniq; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX orgs_one_personal_per_account_uniq ON public.orgs USING btree (personal_owner_account_id) WHERE (personal_org = true);


--
-- Name: orgs_slug_uniq; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX orgs_slug_uniq ON public.orgs USING btree (lower(slug));


--
-- Name: orgs_status_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX orgs_status_idx ON public.orgs USING btree (status) WHERE (status <> 'active'::text);


--
-- Name: paddle_overage_dedupe_month_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX paddle_overage_dedupe_month_idx ON public.paddle_overage_dedupe USING btree (month);


--
-- Name: paddle_overage_dedupe_org_id_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX paddle_overage_dedupe_org_id_idx ON public.paddle_overage_dedupe USING btree (org_id) WHERE (org_id IS NOT NULL);


--
-- Name: paddle_overage_dedupe_pending_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX paddle_overage_dedupe_pending_idx ON public.paddle_overage_dedupe USING btree (claimed_at) WHERE (state = 'pending'::text);


--
-- Name: pg_ratelimit_counters_subject_id_app_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX pg_ratelimit_counters_subject_id_app_idx ON public.pg_ratelimit_counters USING btree (subject_id) WHERE (scope = 'app'::text);


--
-- Name: projects_install_repo_uniq; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX projects_install_repo_uniq ON public.projects USING btree (install_id, repo_full_name) WHERE ((install_id IS NOT NULL) AND (repo_full_name IS NOT NULL));


--
-- Name: projects_org_id_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX projects_org_id_idx ON public.projects USING btree (org_id) WHERE (org_id IS NOT NULL);


--
-- Name: recent_build_claims_account_id_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX recent_build_claims_account_id_idx ON public.recent_build_claims USING btree (account_id);


--
-- Name: recent_build_claims_claimed_at_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX recent_build_claims_claimed_at_idx ON public.recent_build_claims USING btree (claimed_at);


--
-- Name: recent_build_claims_org_id_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX recent_build_claims_org_id_idx ON public.recent_build_claims USING btree (org_id) WHERE (org_id IS NOT NULL);


--
-- Name: release_bundles_applied_at_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX release_bundles_applied_at_idx ON public.release_bundles USING btree (applied_at) WHERE (applied_at IS NOT NULL);


--
-- Name: release_bundles_git_sha_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX release_bundles_git_sha_idx ON public.release_bundles USING btree (git_sha);


--
-- Name: sessions_active_account_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX sessions_active_account_idx ON public.sessions USING btree (account_id, issued_at DESC) WHERE (revoked_at IS NULL);


--
-- Name: snapshot_storage_daily_account_day_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX snapshot_storage_daily_account_day_idx ON public.snapshot_storage_daily USING btree (account_id, day DESC);


--
-- Name: snapshots_deployment_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX snapshots_deployment_idx ON public.snapshots USING btree (deployment_id);


--
-- Name: snapshots_deployment_tier_key; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX snapshots_deployment_tier_key ON public.snapshots USING btree (deployment_id, tier) WHERE (stale = false);


--
-- Name: snapshots_live_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX snapshots_live_idx ON public.snapshots USING btree (deployment_id) WHERE (stale = false);


--
-- Name: INDEX snapshots_live_idx; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON INDEX public.snapshots_live_idx IS 'Supports pkg/state/pgstore.go::LatestSnapshotBytes inner scan — bounds to non-stale rows under live deployments. ADR-049 §B.3 + PR #428 review blocker #3.';


--
-- Name: stripe_push_dedupe_hour_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX stripe_push_dedupe_hour_idx ON public.stripe_push_dedupe USING btree (hour);


--
-- Name: stripe_push_dedupe_org_id_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX stripe_push_dedupe_org_id_idx ON public.stripe_push_dedupe USING btree (org_id) WHERE (org_id IS NOT NULL);


--
-- Name: tenant_hostnames_hostname_uniq; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX tenant_hostnames_hostname_uniq ON public.tenant_hostnames USING btree (hostname);


--
-- Name: tenant_hostnames_pending_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX tenant_hostnames_pending_idx ON public.tenant_hostnames USING btree (last_check_at) WHERE (verified_at IS NULL);


--
-- Name: tenant_hostnames_surface_hostname_uniq; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX tenant_hostnames_surface_hostname_uniq ON public.tenant_hostnames USING btree (surface_id, hostname);


--
-- Name: tenant_hostnames_surface_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX tenant_hostnames_surface_idx ON public.tenant_hostnames USING btree (surface_id);


--
-- Name: tenant_hostnames_verified_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX tenant_hostnames_verified_idx ON public.tenant_hostnames USING btree (surface_id) WHERE (verified_at IS NOT NULL);


--
-- Name: tenant_surfaces_account_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX tenant_surfaces_account_idx ON public.tenant_surfaces USING btree (account_id) WHERE (status <> 'deleted'::text);


--
-- Name: tenant_surfaces_account_name_uniq; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX tenant_surfaces_account_name_uniq ON public.tenant_surfaces USING btree (account_id, name) WHERE (status <> 'deleted'::text);


--
-- Name: tenant_surfaces_app_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX tenant_surfaces_app_idx ON public.tenant_surfaces USING btree (app_id) WHERE (app_id IS NOT NULL);


--
-- Name: tenant_surfaces_cert_expiry_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX tenant_surfaces_cert_expiry_idx ON public.tenant_surfaces USING btree (cert_not_after) WHERE (cert_state = 'issued'::text);


--
-- Name: trigger_dlq_trigger_reason_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX trigger_dlq_trigger_reason_idx ON public.trigger_dead_letter USING btree (trigger_id, reason);


--
-- Name: trigger_records_dlq_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX trigger_records_dlq_idx ON public.trigger_records USING btree (trigger_id, state) WHERE (state = 'dead_letter'::text);


--
-- Name: trigger_records_due_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX trigger_records_due_idx ON public.trigger_records USING btree (trigger_id, next_fire_at) WHERE (state = ANY (ARRAY['pending'::text, 'retry'::text]));


--
-- Name: triggers_account_kind_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX triggers_account_kind_idx ON public.triggers USING btree (account_id, kind);


--
-- Name: triggers_app_kind_enabled; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX triggers_app_kind_enabled ON public.triggers USING btree (app_id, kind) WHERE enabled;


--
-- Name: triggers_cron_id_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX triggers_cron_id_idx ON public.triggers USING btree (cron_id) WHERE (cron_id IS NOT NULL);


--
-- Name: usage_daily_account_day_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX usage_daily_account_day_idx ON public.usage_daily USING btree (account_id, day DESC);


--
-- Name: usage_daily_org_id_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX usage_daily_org_id_idx ON public.usage_daily USING btree (org_id) WHERE (org_id IS NOT NULL);


--
-- Name: usage_minutes_account_minute_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX usage_minutes_account_minute_idx ON public.usage_minutes USING btree (account_id, minute DESC);


--
-- Name: usage_minutes_job_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX usage_minutes_job_idx ON public.usage_minutes USING btree (account_id, minute DESC) WHERE (meter_kind = 'job'::text);


--
-- Name: usage_minutes_minute_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX usage_minutes_minute_idx ON public.usage_minutes USING btree (minute);


--
-- Name: usage_minutes_org_id_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX usage_minutes_org_id_idx ON public.usage_minutes USING btree (org_id) WHERE (org_id IS NOT NULL);


--
-- Name: warm_hint_node_id_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX warm_hint_node_id_idx ON public.warm_hint USING btree (node_id);


--
-- Name: webhook_deliveries_expires_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX webhook_deliveries_expires_idx ON public.webhook_deliveries USING btree (expires_at);


--
-- Name: data_upstream_probes_default_pkey; Type: INDEX ATTACH; Schema: public; Owner: -
--

ALTER INDEX public.data_upstream_probes_pkey ATTACH PARTITION public.data_upstream_probes_default_pkey;


--
-- Name: apps apps_egress_allowlist_cidr; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER apps_egress_allowlist_cidr BEFORE INSERT OR UPDATE OF egress_allowlist ON public.apps FOR EACH ROW EXECUTE FUNCTION public.apps_egress_allowlist_cidr_check();


--
-- Name: apps_public_auth_ip_allowlist_cidr_check(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.apps_public_auth_ip_allowlist_cidr_check() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
declare
  bad cidr;
begin
  if new.public_auth_ip_allowlist is null or cardinality(new.public_auth_ip_allowlist) = 0 then
    return new;
  end if;
  for bad in
    select c
      from unnest(new.public_auth_ip_allowlist) c
     where family(c) not in (4, 6)
     limit 1
  loop
    raise exception 'apps_public_auth_ip_allowlist: only v4 or v6 CIDRs (got family % for %)', family(bad), bad
      using errcode = '23514',
            constraint = 'apps_public_auth_ip_allowlist_cidr';
  end loop;
  for bad in
    select c
      from unnest(new.public_auth_ip_allowlist) c
     where masklen(c) = 0
     limit 1
  loop
    raise exception 'apps_public_auth_ip_allowlist: rejected % (masklen /0; ADR-118 non-/0 contract)', bad
      using errcode = '23514',
            constraint = 'apps_public_auth_ip_allowlist_cidr';
  end loop;
  return new;
end;
$$;


--
-- Name: apps apps_public_auth_ip_allowlist_cidr; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER apps_public_auth_ip_allowlist_cidr BEFORE INSERT OR UPDATE OF public_auth_ip_allowlist ON public.apps FOR EACH ROW EXECUTE FUNCTION public.apps_public_auth_ip_allowlist_cidr_check();


--
-- Name: apps apps_maintenance_mode_notify; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER apps_maintenance_mode_notify AFTER UPDATE ON public.apps FOR EACH ROW EXECUTE FUNCTION public.apps_maintenance_mode_notify();


--
-- Name: compute_nodes compute_node_changed_trg; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER compute_node_changed_trg AFTER INSERT OR UPDATE ON public.compute_nodes FOR EACH ROW EXECUTE FUNCTION public.compute_node_notify();


--
-- Name: compute_node_keys compute_node_keys_changed_trg; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER compute_node_keys_changed_trg AFTER INSERT OR DELETE OR UPDATE ON public.compute_node_keys FOR EACH STATEMENT EXECUTE FUNCTION public.compute_node_keys_notify();


--
-- Name: cors_presets cors_presets_set_updated_at_trg; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER cors_presets_set_updated_at_trg BEFORE UPDATE ON public.cors_presets FOR EACH ROW EXECUTE FUNCTION public.cors_presets_set_updated_at();


--
-- Name: data_upstreams data_upstreams_notify_trg; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER data_upstreams_notify_trg AFTER INSERT OR DELETE OR UPDATE ON public.data_upstreams FOR EACH ROW EXECUTE FUNCTION public.data_upstreams_notify();


--
-- Name: deployment_sidecar_layers deployment_sidecar_layers_cap_trg; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER deployment_sidecar_layers_cap_trg BEFORE INSERT OR UPDATE ON public.deployment_sidecar_layers FOR EACH ROW EXECUTE FUNCTION public.deployment_sidecar_layers_cap_check();


--
-- Name: edge_rules edge_rules_set_updated_at_trg; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER edge_rules_set_updated_at_trg BEFORE UPDATE ON public.edge_rules FOR EACH ROW EXECUTE FUNCTION public.edge_rules_set_updated_at();


--
-- Name: egress_policy egress_policy_changed_trg; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER egress_policy_changed_trg AFTER INSERT OR UPDATE ON public.egress_policy FOR EACH ROW EXECUTE FUNCTION public.egress_policy_notify();


--
-- Name: github_webhook_secrets github_webhook_secrets_notify_trg; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER github_webhook_secrets_notify_trg AFTER INSERT OR UPDATE ON public.github_webhook_secrets FOR EACH ROW EXECUTE FUNCTION public.github_webhook_secrets_notify();


--
-- Name: instances instances_started_at_set_trg; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER instances_started_at_set_trg BEFORE INSERT ON public.instances FOR EACH ROW EXECUTE FUNCTION public.instances_started_at_set();


--
-- Name: invocations invocation_done_trg; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER invocation_done_trg AFTER UPDATE ON public.invocations FOR EACH ROW EXECUTE FUNCTION public.invocation_done_notify();


--
-- Name: invocations invocation_due_trg; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER invocation_due_trg AFTER INSERT OR UPDATE OF state ON public.invocations FOR EACH ROW WHEN ((new.state = 'pending'::text)) EXECUTE FUNCTION public.invocation_due_notify();


--
-- Name: job_tasks job_tasks_notify_trg; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER job_tasks_notify_trg AFTER INSERT OR DELETE OR UPDATE ON public.job_tasks FOR EACH ROW EXECUTE FUNCTION public.job_tasks_notify();


--
-- Name: tenant_hostnames tenant_hostnames_emit_change; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER tenant_hostnames_emit_change AFTER INSERT OR DELETE OR UPDATE ON public.tenant_hostnames FOR EACH ROW EXECUTE FUNCTION public.notify_tenant_surface_changed();


--
-- Name: tenant_surfaces tenant_surfaces_emit_change; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER tenant_surfaces_emit_change AFTER INSERT OR DELETE OR UPDATE ON public.tenant_surfaces FOR EACH ROW EXECUTE FUNCTION public.notify_tenant_surface_changed();


--
-- Name: trigger_records trigger_ready_notify; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER trigger_ready_notify AFTER INSERT ON public.trigger_records FOR EACH ROW EXECUTE FUNCTION public.trg_notify_trigger_ready();


--
-- Name: account_credits account_credits_account_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.account_credits
    ADD CONSTRAINT account_credits_account_id_fkey FOREIGN KEY (account_id) REFERENCES public.accounts(id) ON DELETE CASCADE;


--
-- Name: account_passwords account_passwords_account_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.account_passwords
    ADD CONSTRAINT account_passwords_account_id_fkey FOREIGN KEY (account_id) REFERENCES public.accounts(id) ON DELETE CASCADE;


--
-- Name: alert_deliveries alert_deliveries_account_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.alert_deliveries
    ADD CONSTRAINT alert_deliveries_account_id_fkey FOREIGN KEY (account_id) REFERENCES public.accounts(id) ON DELETE CASCADE;


--
-- Name: alert_deliveries alert_deliveries_rule_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.alert_deliveries
    ADD CONSTRAINT alert_deliveries_rule_id_fkey FOREIGN KEY (rule_id) REFERENCES public.alert_rules(id) ON DELETE CASCADE;


--
-- Name: alert_rules alert_rules_account_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.alert_rules
    ADD CONSTRAINT alert_rules_account_id_fkey FOREIGN KEY (account_id) REFERENCES public.accounts(id) ON DELETE CASCADE;


--
-- Name: alert_rules alert_rules_app_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.alert_rules
    ADD CONSTRAINT alert_rules_app_id_fkey FOREIGN KEY (app_id) REFERENCES public.apps(id) ON DELETE CASCADE;


--
-- Name: alert_rules alert_rules_org_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.alert_rules
    ADD CONSTRAINT alert_rules_org_id_fkey FOREIGN KEY (org_id) REFERENCES public.orgs(id) ON DELETE RESTRICT;


--
-- Name: api_keys api_keys_account_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.api_keys
    ADD CONSTRAINT api_keys_account_id_fkey FOREIGN KEY (account_id) REFERENCES public.accounts(id) ON DELETE CASCADE;


--
-- Name: api_keys api_keys_org_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.api_keys
    ADD CONSTRAINT api_keys_org_id_fkey FOREIGN KEY (org_id) REFERENCES public.orgs(id) ON DELETE RESTRICT;


--
-- Name: api_keys api_keys_parent_key_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.api_keys
    ADD CONSTRAINT api_keys_parent_key_id_fkey FOREIGN KEY (parent_key_id) REFERENCES public.api_keys(id) ON DELETE SET NULL;


--
-- Name: api_keys api_keys_rotated_from_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.api_keys
    ADD CONSTRAINT api_keys_rotated_from_id_fkey FOREIGN KEY (rotated_from_id) REFERENCES public.api_keys(id);


--
-- Name: app_envs app_envs_account_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.app_envs
    ADD CONSTRAINT app_envs_account_id_fkey FOREIGN KEY (account_id) REFERENCES public.accounts(id) ON DELETE CASCADE;


--
-- Name: app_envs app_envs_org_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.app_envs
    ADD CONSTRAINT app_envs_org_id_fkey FOREIGN KEY (org_id) REFERENCES public.orgs(id) ON DELETE RESTRICT;


--
-- Name: app_error_requests app_error_requests_account_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.app_error_requests
    ADD CONSTRAINT app_error_requests_account_id_fkey FOREIGN KEY (account_id) REFERENCES public.accounts(id) ON DELETE CASCADE;


--
-- Name: app_error_requests app_error_requests_app_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.app_error_requests
    ADD CONSTRAINT app_error_requests_app_id_fkey FOREIGN KEY (app_id) REFERENCES public.apps(id) ON DELETE CASCADE;


--
-- Name: app_error_requests app_error_requests_deployment_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.app_error_requests
    ADD CONSTRAINT app_error_requests_deployment_id_fkey FOREIGN KEY (deployment_id) REFERENCES public.deployments(id) ON DELETE SET NULL;


--
-- Name: app_errors app_errors_account_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.app_errors
    ADD CONSTRAINT app_errors_account_id_fkey FOREIGN KEY (account_id) REFERENCES public.accounts(id) ON DELETE CASCADE;


--
-- Name: app_errors app_errors_app_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.app_errors
    ADD CONSTRAINT app_errors_app_id_fkey FOREIGN KEY (app_id) REFERENCES public.apps(id) ON DELETE CASCADE;


--
-- Name: app_errors app_errors_deployment_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.app_errors
    ADD CONSTRAINT app_errors_deployment_id_fkey FOREIGN KEY (deployment_id) REFERENCES public.deployments(id) ON DELETE SET NULL;


--
-- Name: app_registry_credentials app_registry_credentials_account_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.app_registry_credentials
    ADD CONSTRAINT app_registry_credentials_account_id_fkey FOREIGN KEY (account_id) REFERENCES public.accounts(id) ON DELETE CASCADE;


--
-- Name: app_registry_credentials app_registry_credentials_app_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.app_registry_credentials
    ADD CONSTRAINT app_registry_credentials_app_id_fkey FOREIGN KEY (app_id) REFERENCES public.apps(id) ON DELETE CASCADE;


--
-- Name: app_secrets app_secrets_account_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.app_secrets
    ADD CONSTRAINT app_secrets_account_id_fkey FOREIGN KEY (account_id) REFERENCES public.accounts(id) ON DELETE CASCADE;


--
-- Name: app_secrets app_secrets_org_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.app_secrets
    ADD CONSTRAINT app_secrets_org_id_fkey FOREIGN KEY (org_id) REFERENCES public.orgs(id) ON DELETE RESTRICT;


--
-- Name: app_trusted_signers app_trusted_signers_account_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.app_trusted_signers
    ADD CONSTRAINT app_trusted_signers_account_id_fkey FOREIGN KEY (account_id) REFERENCES public.accounts(id) ON DELETE CASCADE;


--
-- Name: app_webhook_deliveries app_webhook_deliveries_account_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.app_webhook_deliveries
    ADD CONSTRAINT app_webhook_deliveries_account_id_fkey FOREIGN KEY (account_id) REFERENCES public.accounts(id) ON DELETE CASCADE;


--
-- Name: app_webhook_deliveries app_webhook_deliveries_app_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.app_webhook_deliveries
    ADD CONSTRAINT app_webhook_deliveries_app_id_fkey FOREIGN KEY (app_id) REFERENCES public.apps(id) ON DELETE CASCADE;


--
-- Name: app_webhook_deliveries app_webhook_deliveries_webhook_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.app_webhook_deliveries
    ADD CONSTRAINT app_webhook_deliveries_webhook_id_fkey FOREIGN KEY (webhook_id) REFERENCES public.app_webhooks(id) ON DELETE CASCADE;


--
-- Name: app_webhooks app_webhooks_account_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.app_webhooks
    ADD CONSTRAINT app_webhooks_account_id_fkey FOREIGN KEY (account_id) REFERENCES public.accounts(id) ON DELETE CASCADE;


--
-- Name: app_webhooks app_webhooks_app_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.app_webhooks
    ADD CONSTRAINT app_webhooks_app_id_fkey FOREIGN KEY (app_id) REFERENCES public.apps(id) ON DELETE CASCADE;


--
-- Name: apps apps_account_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.apps
    ADD CONSTRAINT apps_account_id_fkey FOREIGN KEY (account_id) REFERENCES public.accounts(id);


--
-- Name: apps apps_github_install_account_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.apps
    ADD CONSTRAINT apps_github_install_account_id_fkey FOREIGN KEY (github_install_account_id) REFERENCES public.accounts(id) ON DELETE SET NULL;


--
-- Name: apps apps_node_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.apps
    ADD CONSTRAINT apps_node_id_fkey FOREIGN KEY (node_id) REFERENCES public.compute_nodes(id) ON DELETE RESTRICT;


--
-- Name: apps apps_org_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.apps
    ADD CONSTRAINT apps_org_id_fkey FOREIGN KEY (org_id) REFERENCES public.orgs(id) ON DELETE RESTRICT;


--
-- Name: apps apps_overflow_node_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.apps
    ADD CONSTRAINT apps_overflow_node_fkey FOREIGN KEY (overflow_node) REFERENCES public.compute_nodes(id) ON DELETE SET NULL;


--
-- Name: apps apps_project_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.apps
    ADD CONSTRAINT apps_project_id_fkey FOREIGN KEY (project_id) REFERENCES public.projects(id) ON DELETE SET NULL;


--
-- Name: build_provenance build_provenance_build_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.build_provenance
    ADD CONSTRAINT build_provenance_build_id_fkey FOREIGN KEY (build_id) REFERENCES public.builds(id);


--
-- Name: builder_usage builder_usage_org_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.builder_usage
    ADD CONSTRAINT builder_usage_org_id_fkey FOREIGN KEY (org_id) REFERENCES public.orgs(id) ON DELETE RESTRICT;


--
-- Name: builds builds_deployment_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.builds
    ADD CONSTRAINT builds_deployment_id_fkey FOREIGN KEY (deployment_id) REFERENCES public.deployments(id);


--
-- Name: cli_auth_codes cli_auth_codes_account_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.cli_auth_codes
    ADD CONSTRAINT cli_auth_codes_account_id_fkey FOREIGN KEY (account_id) REFERENCES public.accounts(id) ON DELETE CASCADE;


--
-- Name: compute_node_heartbeats compute_node_heartbeats_node_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.compute_node_heartbeats
    ADD CONSTRAINT compute_node_heartbeats_node_id_fkey FOREIGN KEY (node_id) REFERENCES public.compute_nodes(id) ON DELETE CASCADE;


--
-- Name: compute_node_keys compute_node_keys_compute_node_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.compute_node_keys
    ADD CONSTRAINT compute_node_keys_compute_node_id_fkey FOREIGN KEY (compute_node_id) REFERENCES public.compute_nodes(id) ON DELETE CASCADE;


--
-- Name: cors_presets cors_presets_account_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.cors_presets
    ADD CONSTRAINT cors_presets_account_id_fkey FOREIGN KEY (account_id) REFERENCES public.accounts(id) ON DELETE CASCADE;


--
-- Name: cors_presets cors_presets_app_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.cors_presets
    ADD CONSTRAINT cors_presets_app_id_fkey FOREIGN KEY (app_id) REFERENCES public.apps(id) ON DELETE CASCADE;


--
-- Name: credit_ledger credit_ledger_account_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.credit_ledger
    ADD CONSTRAINT credit_ledger_account_id_fkey FOREIGN KEY (account_id) REFERENCES public.accounts(id) ON DELETE CASCADE;


--
-- Name: credit_ledger credit_ledger_credit_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.credit_ledger
    ADD CONSTRAINT credit_ledger_credit_id_fkey FOREIGN KEY (credit_id) REFERENCES public.account_credits(id) ON DELETE CASCADE;


--
-- Name: cron_fire_now_requests cron_fire_now_requests_cron_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.cron_fire_now_requests
    ADD CONSTRAINT cron_fire_now_requests_cron_id_fkey FOREIGN KEY (cron_id) REFERENCES public.crons(id) ON DELETE CASCADE;


--
-- Name: crons crons_app_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.crons
    ADD CONSTRAINT crons_app_id_fkey FOREIGN KEY (app_id) REFERENCES public.apps(id);


--
-- Name: crons crons_org_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.crons
    ADD CONSTRAINT crons_org_id_fkey FOREIGN KEY (org_id) REFERENCES public.orgs(id) ON DELETE RESTRICT;


--
-- Name: custom_domains custom_domains_app_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.custom_domains
    ADD CONSTRAINT custom_domains_app_id_fkey FOREIGN KEY (app_id) REFERENCES public.apps(id);


--
-- Name: custom_domains custom_domains_app_id_redirect_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.custom_domains
    ADD CONSTRAINT custom_domains_app_id_redirect_fkey FOREIGN KEY (app_id_redirect) REFERENCES public.apps(id);


--
-- Name: custom_domains custom_domains_org_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.custom_domains
    ADD CONSTRAINT custom_domains_org_id_fkey FOREIGN KEY (org_id) REFERENCES public.orgs(id) ON DELETE RESTRICT;


--
-- Name: data_upstreams data_upstreams_account_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.data_upstreams
    ADD CONSTRAINT data_upstreams_account_id_fkey FOREIGN KEY (account_id) REFERENCES public.accounts(id) ON DELETE CASCADE;


--
-- Name: data_upstreams data_upstreams_app_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.data_upstreams
    ADD CONSTRAINT data_upstreams_app_id_fkey FOREIGN KEY (app_id) REFERENCES public.apps(id) ON DELETE CASCADE;


--
-- Name: deployment_logs deployment_logs_deployment_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.deployment_logs
    ADD CONSTRAINT deployment_logs_deployment_id_fkey FOREIGN KEY (deployment_id) REFERENCES public.deployments(id) ON DELETE CASCADE;


--
-- Name: deployment_sidecar_layers deployment_sidecar_layers_deployment_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.deployment_sidecar_layers
    ADD CONSTRAINT deployment_sidecar_layers_deployment_id_fkey FOREIGN KEY (deployment_id) REFERENCES public.deployments(id) ON DELETE CASCADE;


--
-- Name: deployments deployments_app_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.deployments
    ADD CONSTRAINT deployments_app_id_fkey FOREIGN KEY (app_id) REFERENCES public.apps(id);


--
-- Name: edge_rules edge_rules_account_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.edge_rules
    ADD CONSTRAINT edge_rules_account_id_fkey FOREIGN KEY (account_id) REFERENCES public.accounts(id) ON DELETE CASCADE;


--
-- Name: edge_rules edge_rules_app_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.edge_rules
    ADD CONSTRAINT edge_rules_app_id_fkey FOREIGN KEY (app_id) REFERENCES public.apps(id) ON DELETE CASCADE;


--
-- Name: events events_actor_account_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.events
    ADD CONSTRAINT events_actor_account_id_fkey FOREIGN KEY (actor_account_id) REFERENCES public.accounts(id) ON DELETE SET NULL;


--
-- Name: gdpr_requests gdpr_requests_org_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.gdpr_requests
    ADD CONSTRAINT gdpr_requests_org_id_fkey FOREIGN KEY (org_id) REFERENCES public.orgs(id) ON DELETE RESTRICT;


--
-- Name: github_installations github_installations_account_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.github_installations
    ADD CONSTRAINT github_installations_account_id_fkey FOREIGN KEY (account_id) REFERENCES public.accounts(id) ON DELETE CASCADE;


--
-- Name: github_installations github_installations_org_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.github_installations
    ADD CONSTRAINT github_installations_org_id_fkey FOREIGN KEY (org_id) REFERENCES public.orgs(id) ON DELETE RESTRICT;


--
-- Name: idempotency_keys idempotency_keys_account_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.idempotency_keys
    ADD CONSTRAINT idempotency_keys_account_id_fkey FOREIGN KEY (account_id) REFERENCES public.accounts(id);


--
-- Name: instances instances_app_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.instances
    ADD CONSTRAINT instances_app_id_fkey FOREIGN KEY (app_id) REFERENCES public.apps(id);


--
-- Name: instances instances_deployment_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.instances
    ADD CONSTRAINT instances_deployment_id_fkey FOREIGN KEY (deployment_id) REFERENCES public.deployments(id);


--
-- Name: instances instances_job_id_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.instances
    ADD CONSTRAINT instances_job_id_fk FOREIGN KEY (job_id) REFERENCES public.jobs(id) ON DELETE SET NULL;


--
-- Name: instances instances_migrated_from_node_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.instances
    ADD CONSTRAINT instances_migrated_from_node_id_fkey FOREIGN KEY (migrated_from_node_id) REFERENCES public.compute_nodes(id) ON DELETE SET NULL;


--
-- Name: instances instances_node_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.instances
    ADD CONSTRAINT instances_node_id_fkey FOREIGN KEY (node_id) REFERENCES public.compute_nodes(id);


--
-- Name: instances instances_org_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.instances
    ADD CONSTRAINT instances_org_id_fkey FOREIGN KEY (org_id) REFERENCES public.orgs(id) ON DELETE RESTRICT;


--
-- Name: invocations invocations_account_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.invocations
    ADD CONSTRAINT invocations_account_id_fkey FOREIGN KEY (account_id) REFERENCES public.accounts(id);


--
-- Name: invocations invocations_app_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.invocations
    ADD CONSTRAINT invocations_app_id_fkey FOREIGN KEY (app_id) REFERENCES public.apps(id);


--
-- Name: invocations invocations_cron_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.invocations
    ADD CONSTRAINT invocations_cron_id_fkey FOREIGN KEY (cron_id) REFERENCES public.crons(id);


--
-- Name: invocations invocations_org_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.invocations
    ADD CONSTRAINT invocations_org_id_fkey FOREIGN KEY (org_id) REFERENCES public.orgs(id) ON DELETE RESTRICT;


--
-- Name: invoices invoices_account_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.invoices
    ADD CONSTRAINT invoices_account_id_fkey FOREIGN KEY (account_id) REFERENCES public.accounts(id) ON DELETE CASCADE;


--
-- Name: invoices invoices_org_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.invoices
    ADD CONSTRAINT invoices_org_id_fkey FOREIGN KEY (org_id) REFERENCES public.orgs(id) ON DELETE RESTRICT;


--
-- Name: job_runs job_runs_account_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.job_runs
    ADD CONSTRAINT job_runs_account_id_fkey FOREIGN KEY (account_id) REFERENCES public.accounts(id) ON DELETE CASCADE;


--
-- Name: job_runs job_runs_job_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.job_runs
    ADD CONSTRAINT job_runs_job_id_fkey FOREIGN KEY (job_id) REFERENCES public.jobs(id) ON DELETE CASCADE;


--
-- Name: job_tasks job_tasks_instance_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.job_tasks
    ADD CONSTRAINT job_tasks_instance_id_fkey FOREIGN KEY (instance_id) REFERENCES public.instances(id);


--
-- Name: job_tasks job_tasks_run_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.job_tasks
    ADD CONSTRAINT job_tasks_run_id_fkey FOREIGN KEY (run_id) REFERENCES public.job_runs(id) ON DELETE CASCADE;


--
-- Name: jobs jobs_account_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.jobs
    ADD CONSTRAINT jobs_account_id_fkey FOREIGN KEY (account_id) REFERENCES public.accounts(id) ON DELETE CASCADE;


--
-- Name: login_tokens login_tokens_account_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.login_tokens
    ADD CONSTRAINT login_tokens_account_id_fkey FOREIGN KEY (account_id) REFERENCES public.accounts(id) ON DELETE CASCADE;


--
-- Name: oauth_links oauth_links_account_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.oauth_links
    ADD CONSTRAINT oauth_links_account_id_fkey FOREIGN KEY (account_id) REFERENCES public.accounts(id) ON DELETE CASCADE;


--
-- Name: oidc_exchanged_tokens oidc_exchanged_tokens_account_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.oidc_exchanged_tokens
    ADD CONSTRAINT oidc_exchanged_tokens_account_id_fkey FOREIGN KEY (account_id) REFERENCES public.accounts(id) ON DELETE CASCADE;


--
-- Name: oidc_trust_policies oidc_trust_policies_account_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.oidc_trust_policies
    ADD CONSTRAINT oidc_trust_policies_account_id_fkey FOREIGN KEY (account_id) REFERENCES public.accounts(id) ON DELETE CASCADE;


--
-- Name: org_invitations org_invitations_accepting_account_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.org_invitations
    ADD CONSTRAINT org_invitations_accepting_account_id_fkey FOREIGN KEY (accepting_account_id) REFERENCES public.accounts(id) ON DELETE SET NULL;


--
-- Name: org_invitations org_invitations_invited_by_account_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.org_invitations
    ADD CONSTRAINT org_invitations_invited_by_account_id_fkey FOREIGN KEY (invited_by_account_id) REFERENCES public.accounts(id) ON DELETE SET NULL;


--
-- Name: org_invitations org_invitations_org_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.org_invitations
    ADD CONSTRAINT org_invitations_org_id_fkey FOREIGN KEY (org_id) REFERENCES public.orgs(id) ON DELETE CASCADE;


--
-- Name: org_memberships org_memberships_account_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.org_memberships
    ADD CONSTRAINT org_memberships_account_id_fkey FOREIGN KEY (account_id) REFERENCES public.accounts(id) ON DELETE CASCADE;


--
-- Name: org_memberships org_memberships_invited_by_account_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.org_memberships
    ADD CONSTRAINT org_memberships_invited_by_account_id_fkey FOREIGN KEY (invited_by_account_id) REFERENCES public.accounts(id) ON DELETE SET NULL;


--
-- Name: org_memberships org_memberships_org_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.org_memberships
    ADD CONSTRAINT org_memberships_org_id_fkey FOREIGN KEY (org_id) REFERENCES public.orgs(id) ON DELETE CASCADE;


--
-- Name: orgs orgs_personal_owner_account_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.orgs
    ADD CONSTRAINT orgs_personal_owner_account_id_fkey FOREIGN KEY (personal_owner_account_id) REFERENCES public.accounts(id) ON DELETE CASCADE;


--
-- Name: paddle_overage_dedupe paddle_overage_dedupe_account_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.paddle_overage_dedupe
    ADD CONSTRAINT paddle_overage_dedupe_account_id_fkey FOREIGN KEY (account_id) REFERENCES public.accounts(id) ON DELETE CASCADE;


--
-- Name: paddle_overage_dedupe paddle_overage_dedupe_org_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.paddle_overage_dedupe
    ADD CONSTRAINT paddle_overage_dedupe_org_id_fkey FOREIGN KEY (org_id) REFERENCES public.orgs(id) ON DELETE RESTRICT;


--
-- Name: projects projects_account_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.projects
    ADD CONSTRAINT projects_account_id_fkey FOREIGN KEY (account_id) REFERENCES public.accounts(id) ON DELETE CASCADE;


--
-- Name: projects projects_org_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.projects
    ADD CONSTRAINT projects_org_id_fkey FOREIGN KEY (org_id) REFERENCES public.orgs(id) ON DELETE RESTRICT;


--
-- Name: recent_build_claims recent_build_claims_account_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.recent_build_claims
    ADD CONSTRAINT recent_build_claims_account_id_fkey FOREIGN KEY (account_id) REFERENCES public.accounts(id) ON DELETE CASCADE;


--
-- Name: recent_build_claims recent_build_claims_org_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.recent_build_claims
    ADD CONSTRAINT recent_build_claims_org_id_fkey FOREIGN KEY (org_id) REFERENCES public.orgs(id) ON DELETE RESTRICT;


--
-- Name: sessions sessions_account_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.sessions
    ADD CONSTRAINT sessions_account_id_fkey FOREIGN KEY (account_id) REFERENCES public.accounts(id) ON DELETE CASCADE;


--
-- Name: snapshots snapshots_deployment_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.snapshots
    ADD CONSTRAINT snapshots_deployment_id_fkey FOREIGN KEY (deployment_id) REFERENCES public.deployments(id);


--
-- Name: stripe_push_dedupe stripe_push_dedupe_account_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.stripe_push_dedupe
    ADD CONSTRAINT stripe_push_dedupe_account_id_fkey FOREIGN KEY (account_id) REFERENCES public.accounts(id) ON DELETE CASCADE;


--
-- Name: stripe_push_dedupe stripe_push_dedupe_org_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.stripe_push_dedupe
    ADD CONSTRAINT stripe_push_dedupe_org_id_fkey FOREIGN KEY (org_id) REFERENCES public.orgs(id) ON DELETE RESTRICT;


--
-- Name: tenant_hostnames tenant_hostnames_surface_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.tenant_hostnames
    ADD CONSTRAINT tenant_hostnames_surface_id_fkey FOREIGN KEY (surface_id) REFERENCES public.tenant_surfaces(id) ON DELETE CASCADE;


--
-- Name: tenant_surfaces tenant_surfaces_account_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.tenant_surfaces
    ADD CONSTRAINT tenant_surfaces_account_id_fkey FOREIGN KEY (account_id) REFERENCES public.accounts(id) ON DELETE CASCADE;


--
-- Name: tenant_surfaces tenant_surfaces_app_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.tenant_surfaces
    ADD CONSTRAINT tenant_surfaces_app_id_fkey FOREIGN KEY (app_id) REFERENCES public.apps(id) ON DELETE CASCADE;


--
-- Name: trigger_dead_letter trigger_dead_letter_record_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.trigger_dead_letter
    ADD CONSTRAINT trigger_dead_letter_record_id_fkey FOREIGN KEY (record_id) REFERENCES public.trigger_records(id) ON DELETE CASCADE;


--
-- Name: trigger_records trigger_records_trigger_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.trigger_records
    ADD CONSTRAINT trigger_records_trigger_id_fkey FOREIGN KEY (trigger_id) REFERENCES public.triggers(id) ON DELETE CASCADE;


--
-- Name: triggers triggers_account_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.triggers
    ADD CONSTRAINT triggers_account_id_fkey FOREIGN KEY (account_id) REFERENCES public.accounts(id) ON DELETE CASCADE;


--
-- Name: triggers triggers_app_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.triggers
    ADD CONSTRAINT triggers_app_id_fkey FOREIGN KEY (app_id) REFERENCES public.apps(id) ON DELETE CASCADE;


--
-- Name: triggers triggers_cron_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.triggers
    ADD CONSTRAINT triggers_cron_id_fkey FOREIGN KEY (cron_id) REFERENCES public.crons(id) ON DELETE CASCADE;


--
-- Name: usage_daily usage_daily_org_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.usage_daily
    ADD CONSTRAINT usage_daily_org_id_fkey FOREIGN KEY (org_id) REFERENCES public.orgs(id) ON DELETE RESTRICT;


--
-- Name: usage_minutes usage_minutes_org_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.usage_minutes
    ADD CONSTRAINT usage_minutes_org_id_fkey FOREIGN KEY (org_id) REFERENCES public.orgs(id) ON DELETE RESTRICT;


--
-- Name: upload_commit_outcomes; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.upload_commit_outcomes (
    upload_id text NOT NULL,
    deployment_id text NOT NULL,
    build_id text NOT NULL,
    finalized_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: upload_sessions; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.upload_sessions (
    id text NOT NULL,
    account_id uuid NOT NULL,
    app_slug text NOT NULL,
    total_size bigint NOT NULL,
    received_bytes bigint DEFAULT 0 NOT NULL,
    chunk_size integer DEFAULT 8388608 NOT NULL,
    sha256_hex text,
    part_path text NOT NULL,
    status text DEFAULT 'open' NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    last_patched_at timestamp with time zone DEFAULT now() NOT NULL,
    expires_at timestamp with time zone DEFAULT (now() + '00:24:00'::interval) NOT NULL,
    deployment_id text,
    CONSTRAINT upload_sessions_chunk_size_check CHECK ((chunk_size > 0) AND (chunk_size <= 67108864)),
    CONSTRAINT upload_sessions_received_bytes_check CHECK ((received_bytes >= 0) AND (received_bytes <= total_size)),
    CONSTRAINT upload_sessions_status_check CHECK ((status = ANY (ARRAY['open'::text, 'committed'::text, 'cancelled'::text, 'expired'::text]))),
    CONSTRAINT upload_sessions_total_size_check CHECK ((total_size > 0) AND (total_size <= 1073741824))
);


--
-- Name: upload_commit_outcomes upload_commit_outcomes_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.upload_commit_outcomes
    ADD CONSTRAINT upload_commit_outcomes_pkey PRIMARY KEY (upload_id);


--
-- Name: upload_sessions upload_sessions_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.upload_sessions
    ADD CONSTRAINT upload_sessions_pkey PRIMARY KEY (id);


--
-- Name: upload_sessions_account_open_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX upload_sessions_account_open_idx ON public.upload_sessions USING btree (account_id, app_slug) WHERE (status = 'open'::text);


--
-- Name: upload_sessions_expires_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX upload_sessions_expires_idx ON public.upload_sessions USING btree (expires_at) WHERE (status = 'open'::text);


--
-- Name: upload_commit_outcomes upload_commit_outcomes_upload_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.upload_commit_outcomes
    ADD CONSTRAINT upload_commit_outcomes_upload_id_fkey FOREIGN KEY (upload_id) REFERENCES public.upload_sessions(id) ON DELETE CASCADE;


--
--
