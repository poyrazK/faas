# Error code reference

<!-- GENERATED — do not edit by hand; regenerate with `make error-codes-md`. -->

Every error Gregale returns carries a stable `code`. [Errors](errors.md) explains how an error is shaped and what to do about the most common ones. This page lists each code with the HTTP status Gregale sends it with, the title and hint Gregale writes for it, and the docs page it points to. A real response's `detail` adds the specifics, such as the observed value, the limit or the field that failed.

The list is generated from the code that builds each error, so a new code appears here when it ships. Codes for features whose docs are not published yet are left out. Each code's name is its anchor: link to `error-codes#plan_limit_apps`, for example.

## A

### `account_abuse_hold`

HTTP 403 · Account on hold

See [Apps](apps.md#egress-limits).

### `account_deletion_confirm_required`

HTTP 409

### `account_deletion_managed_postgres_databases`

HTTP 409 · Managed PostgreSQL databases must be deleted first

Run `gregale postgres list`, then `gregale postgres delete <database> --purge` for each database

See [Managed PostgreSQL](postgres.md).

### `account_deletion_pending`

HTTP 409

### `account_deploy_blocked`

HTTP 409 · Deployment unavailable

### `account_egress_allowlist_extra_out_of_range`

HTTP 400 · Account egress allowlist extra out of range

See [Account and organization management](account.md#egress-allowlist-extra).

### `account_exists`

HTTP 400

### `account_not_restorable`

HTTP 409 · Grace expired; Not restorable

### `admin_required`

HTTP 403 · Admin access required

### `admission_refused`

HTTP 402 · Spend cap reached

See [Billing](billing.md#spend-cap).

### `alert_preset_disabled`

HTTP 400 · Alert preset not yet available

See [Alerts](alerts.md#presets).

### `alert_preset_invalid`

HTTP 400 · Invalid alert preset

See [Alerts](alerts.md#presets).

### `alert_preset_test_rate_limited`

HTTP 429 · Alert preset test rate limited

See [Alerts](alerts.md).

### `alert_rule_invalid`

HTTP 400 · Invalid alert rule

See [Alerts](alerts.md).

### `api_key_expired`

HTTP 401 · API key expired

See [Authentication](auth.md#expiry).

### `api_key_limit_exceeded`

HTTP 409 · API key limit reached

See [Authentication](auth.md#quotas).

### `api_key_revoked`

HTTP 401 · API key revoked

See [Authentication](auth.md#revocation).

### `api_rate_limited`

HTTP 429 · API rate limit reached

See [Errors](errors.md).

### `app_admission_unavailable`

HTTP 503 · App admission unavailable

### `app_arch_mismatch`

HTTP 422 · Unsupported CPU architecture

### `app_concurrency_reached`

HTTP 429 · App concurrency reached

See [Plans and pricing](plans.md#concurrency).

### `app_deleted`

HTTP 410 · App deleted; App was deleted while wake was in flight

See [Alerts](alerts.md), [Crons](crons.md).

### `app_has_buckets`

HTTP 409 · Conflict

### `app_has_dependents`

HTTP 409 · Conflict

Remove the service and its references from your Compose file, then run a reviewed deploy (gregale deploy), or delete the dependent apps first.

See [Deploy a multi-service app](multi-service-apps.md).

### `app_has_postgres_bindings`

HTTP 409 · Conflict

See [Managed PostgreSQL](postgres.md).

### `app_health_unavailable`

HTTP 503 · App health is unavailable

See [Errors](errors.md).

### `app_healthz_unauthorized`

HTTP 422 · App_healthz_unauthorized; Health endpoint returning 401

### `app_layer_too_large`

HTTP 403 · App too large

See [Builds and framework detection](build.md#limits), [Container compatibility](container-compatibility.md).

### `app_log_drain_invalid`

HTTP 400 · Bad request; Invalid log drain

### `app_logs_unavailable`

HTTP 503 · App logs unavailable; Live logs temporarily unavailable

Retry `gregale logs APP` in a few seconds.

See [CLI guide](cli-setup.md), [Logs](logs.md), [Errors](errors.md).

### `app_loopback_bound`

HTTP 422 · App bound to loopback; Application bound to loopback

See [Errors](errors.md).

### `app_maintenance_mode`

HTTP 503 · App in maintenance

### `app_manifest_invalid`

HTTP 409 or 422 · Many operations send this code; the response's `title` and `detail` name the failure.

### `app_not_listening`

HTTP 422 · No process listening on $PORT; App listens on a different port

See [Deployment overrides](deploy-overrides.md), [Errors](errors.md).

### `app_not_restorable`

HTTP 409 · App not restorable

### `app_protocol_invalid`

HTTP 400 · Invalid app_protocol; Invalid app protocol

### `app_rename_failed`

HTTP 409 · Slug taken

### `app_runtime_oom`

HTTP 422 or 503 · Container out of memory; App is out of memory; App ran out of memory during startup

See [Errors](errors.md).

### `app_startup_crash`

HTTP 422 · App exited during startup

See [Errors](errors.md).

### `app_startup_timeout`

HTTP 422 · Many operations send this code; the response's `title` and `detail` name the failure.

### `app_static_egress_ip_invalid`

HTTP 400 · Invalid static egress IP

See [Apps](apps.md#static-egress-ip).

### `app_unavailable`

HTTP 503 · App temporarily unavailable

Retry the request in a few seconds.

See [Errors](errors.md).

### `app_visibility_invalid`

HTTP 400 · Invalid app visibility

See [Networking](networking.md).

### `app_webhook_invalid`

HTTP 400 · Bad request; Invalid webhook

### `approval_denied`

HTTP 403 · Approval denied

### `approval_expired`

HTTP 410 · Approval expired

### `approval_required`

HTTP 428 or 429 · Approval required; Too many requests awaiting approval

### `auth_rate_limited`

HTTP 429 · Too many codes; Too many failed authentication attempts; Too many MFA disable emails

See [Authentication](auth.md), [Errors](errors.md).

## B

### `bad_gateway`

HTTP 503 · App connection failed; Edge rule compile error

Retry the request; if it still fails, check the app logs.

See [Quickstart](quickstart.md), [Errors](errors.md).

### `bad_request`

HTTP 400 · Many operations send this code; the response's `title` and `detail` name the failure.

### `before_checkpoint_failed`

HTTP 422 · Before checkpoint callback failed

See [How scaling to zero works](cold-wake.md), [Functions and runtimes](functions.md).

### `billing_already_cancelled`

HTTP 409 · No active subscription

### `billing_cancel_failed`

HTTP 503 · Billing cancel failed

### `billing_contact_member`

HTTP 409 · Member is the billing contact

See [Billing](billing.md#billing-email).

### `billing_email_change_failed`

HTTP 503 · Billing email change failed

### `billing_email_in_use`

HTTP 409 · Billing email in use

See [Billing](billing.md#billing-email).

### `billing_past_due`

HTTP 402 · Account suspended; Deploys blocked; Payment required

See [Billing](billing.md).

### `billing_plan_change_failed`

HTTP 503 · Billing plan change failed

### `billing_provider_unavailable`

HTTP 503 · Payment provider unavailable

### `billing_subscription_ending`

HTTP 409 · Subscription is scheduled to end

See [Billing](billing.md#cancel-and-resume).

### `billing_unavailable`

HTTP 501 · Billing provider not initialised; State store not initialised

### `binding_verification_budget_full`

HTTP 429 · Verification budget full

See [Networking](networking.md), [Binding verification runs](binding-verification-runs.md).

### `binding_verification_run_changed`

HTTP 409 · Verification run blocked

### `binding_verification_run_limit`

HTTP 429 · Verification run limit reached

See [Networking](networking.md).

### `bindings_check_changed`

HTTP 409 · Bindings check changed; Release policy changed; Service graph changed

See [Errors](errors.md).

### `bindings_check_failed`

HTTP 409 · Bindings check failed

See [Errors](errors.md).

### `bindings_gate_unavailable`

HTTP 503 · Bindings gate unavailable

See [Errors](errors.md).

### `bindings_release_policy_changed`

HTTP 409 · Release policy changed

### `bindings_release_required`

HTTP 409 · Binding release policy blocked traffic; Exact rollback required

Verify the exact deployment receiving traffic, then promote, advance its canary, or abort with both deployment_id and expected_predecessor_deployment_id. Recovery without exact selectors requires explicitly setting the release policy to off with its current revision and a reason.

### `build_failed`

The response's `title` and `detail` describe the failure.

### `build_infrastructure_failed`

The response's `title` and `detail` describe the failure.

### `build_not_found`

HTTP 404 · No such build

See [Builds and framework detection](build.md#status).

### `build_oom`

HTTP 503

### `build_provenance_not_found`

HTTP 404 · Build provenance not found

See [Builds and framework detection](build.md#provenance).

### `build_sbom_unavailable`

HTTP 503 · Build SBOM unavailable

See [Builds and framework detection](build.md#sbom).

### `build_timeout`

HTTP 503

### `build_undetected`

HTTP 400

## C

### `canary_step_conflict`

HTTP 409 · Canary step changed

See [Deploys](deploys.md#canary).

### `capability_not_released`

HTTP 403 · Feature not available

See [Plans and pricing](plans.md), [Edge rules](edge-rules.md).

### `capacity_unavailable`

HTTP status varies by operation · Many operations send this code; the response's `title` and `detail` name the failure.

### `challenge_required`

HTTP 403 · Challenge required

See [Edge rules](edge-rules.md), [Security](security.md).

### `cli_auth_code_pending`

HTTP 404 · Awaiting approval

### `cli_auth_code_unavailable`

HTTP 410 · Code unavailable

### `cold_wake_rate_limited`

HTTP 429 · Too many app wakes

### `companion_preset_unavailable`

HTTP 503 · Companion preset unavailable

See [Application companions](companions.md#managed-presets).

### `concurrency_queue_full`

HTTP 429 · Concurrency queue full

See [Scaling policy](scaling-policy.md).

### `concurrency_queue_timeout`

HTTP 503 · Concurrency queue wait expired

See [Scaling policy](scaling-policy.md).

### `concurrency_throttled`

HTTP 429 · App busy; Concurrency limit reached

### `config_guard_not_found`

HTTP 404 · No config guard

See [Environment variables](env.md#config-guard).

### `config_revision_not_found`

HTTP 404 · Config revision not found

See [Environment variables](env.md#config-history).

### `conflict`

HTTP 409 or 410 · Many operations send this code; the response's `title` and `detail` name the failure.

### `control_plane_busy`

HTTP 503 · Control plane busy

### `control_plane_unavailable`

HTTP 503 · Control plane unavailable

### `cors_origin_not_allowed`

HTTP 403 · CORS origin not allowed

### `cors_preset_too_large`

HTTP 422 · CORS preset too large

See [Plans and pricing](plans.md#cors-presets).

### `cron_fire_now_busy`

HTTP 429 · Cron run already pending

See [Crons](crons.md#run-a-cron-now).

### `cron_invalid`

HTTP 400 · Invalid cron path; Cron environment requires a project; Invalid cron schedule

See [Crons](crons.md).

### `csrf_mismatch`

HTTP 400 or 403 · CSRF Error; CSRF mismatch

### `csrf_missing`

HTTP 403 · Missing CSRF state

### `custom_metric_limit`

HTTP 422 · Custom metric limit reached

## D

### `data_upstreams_disabled`

HTTP 503 · Data placement is disabled

See [Plans and pricing](plans.md#data-placement).

### `database_cutover_fenced`

HTTP 409 · Database cutover in progress

### `database_switch_in_progress`

HTTP 409 · Database switch in progress

### `database_switch_not_cancellable`

HTTP 409 · Database switch cannot be cancelled

### `database_switch_unavailable`

HTTP 409 · Database switch unavailable

See [Deploys](deploys.md#rollbacks-that-restore-the-database).

### `database_switches_active`

HTTP 429 · Too many database switches in progress

See [Safe releases](releases.md).

### `debug_regressions_unavailable`

HTTP 503 · Debugger data unavailable

### `debug_replay_unsupported`

HTTP 409 · Debug replay is unavailable; Mirror replay is unavailable

### `dep_install_failed`

HTTP 422 · Dependency installation failure

See [Errors](errors.md).

### `deploy_failed`

HTTP 422 or 503 · Deploy failed; Deployment artifact unavailable

See [Deploys](deploys.md).

### `deploy_rate_limited`

HTTP 429 · Deploy rate limited

See [Deploys](deploys.md#rate-limit).

### `deploy_settings_conflict`

HTTP 409 · Deploy settings changed

### `deploy_signature_invalid`

HTTP 403 · Signed-image enforcement rejected the deploy

See [Deploys](deploys.md#signed-images).

### `deployment_attempt_accepted`

HTTP 409 · Deployment attempt already accepted

### `deployment_attempt_pending`

The response's `title` and `detail` describe the failure.

### `deployment_cancel_live_forbidden`

HTTP 409 · Cannot cancel a live deployment

Use 'gregale deploys rollback &lt;id>' to swap to a previous deployment.

See [Deploys](deploys.md#cancel).

### `deployment_cancel_not_cancellable`

HTTP 409 · Deployment is not in a cancellable state

See [Deploys](deploys.md#cancel).

### `deployment_check_target_changed`

HTTP 409 · Deployment target unavailable

### `deployment_cron_conflict`

HTTP 409 · Deployment cron staging changed

### `deployment_hide_in_flight`

HTTP 409 · Cannot hide a deployment that is still in progress

Cancel the deployment first, then hide it.

See [Deploys](deploys.md#cancel).

### `deployment_not_live`

HTTP 409 · Deployment is not live

See [Deploys](deploys.md#traffic-percent).

### `deployment_promotion_attempt_accepted`

HTTP 409 · Promotion already accepted

### `deployment_promotion_attempt_conflict`

HTTP 409 · Promotion attempt conflict

### `deployment_queue_timeout`

The response's `title` and `detail` describe the failure.

See [Builds and framework detection](build.md), [Deploys](deploys.md).

### `deployment_reorder_not_pending`

HTTP 409 · Reorder only valid for pending deployments

Cancel this deployment and re-deploy with --priority if you need queue control.

See [Deploys](deploys.md#reorder).

### `deployment_reorder_priority_invalid`

HTTP 422 · Priority must be in [0, 1000]

See [Deploys](deploys.md#reorder).

### `deployment_scope_collision`

HTTP 409 · Scope already live

### `deployment_smoke_failed`

HTTP 422

See [Container compatibility](container-compatibility.md).

### `deployment_stalled`

The response's `title` and `detail` describe the failure.

See [Deploys](deploys.md).

### `deployment_verification_unavailable`

HTTP 503

See [Container compatibility](container-compatibility.md).

### `dev_debug_not_developer_environment`

HTTP 409 · Debugging unavailable

### `dev_debug_session_limit`

HTTP 429 · Too many debugger sessions

### `dev_session_tearing_down`

HTTP 409 · Developer environment is being removed

See [CLI guide](cli-setup.md#dev).

### `dev_source_base_missing`

HTTP 409 · Developer source base unavailable

See [CLI guide](cli-setup.md#dev).

### `dev_source_diverged`

HTTP 409 · Instance not snapshotted

### `dev_watch_unsupported`

HTTP 422 · Watch mode is for apps; Watch mode needs an authenticated environment; Watch mode needs more memory

See [CLI guide](cli-setup.md#dev), [`gregale dev`](gregale-dev.md).

### `developer_seed_failed`

The response's `title` and `detail` describe the failure.

See [`gregale dev`](gregale-dev.md).

### `developer_sync_history_unavailable`

HTTP 503 · Developer sync history unavailable

### `domain_cert_not_issued`

HTTP 422 · Domain cert not issued

See [Domains](domains.md#verify).

### `domain_not_verified`

HTTP 409 · Domain not verified

See [Domains](domains.md#verify).

### `domain_verification_failed`

HTTP 422 · Domain verification failed

See [Domains](domains.md#verify).

### `dry_run_requires_post`

HTTP 405 · Dry-run is POST-only; use POST /v1/apps/{slug}/openapi/dry-run

## E

### `edge_feature_unavailable`

HTTP 422 · Edge feature not released

See [Security](security.md).

### `edge_overloaded`

HTTP 503 · Edge overloaded

### `edge_rule_conflict`

HTTP 409 · Edge rule conflict

### `edge_rule_list_exists`

HTTP 409 · Edge rule list exists

### `edge_rule_list_in_use`

HTTP 409 · Edge rule list in use

### `edge_rule_list_not_found`

HTTP 404 · Edge rule list not found

### `edge_rule_maintenance`

HTTP 503 · Endpoint in maintenance

See [Edge rules](edge-rules.md).

### `edge_rule_not_found`

HTTP 404 · Edge rule not found

### `edge_rules_version_mismatch`

HTTP 412 · Edge rules changed

### `egress_allowlist_too_long`

HTTP 400 · Egress allowlist too long

See [Apps](apps.md#egress-allowlist).

### `egress_ports_too_many`

HTTP 400 · Too many egress ports

See [Apps](apps.md#egress-ports).

### `email_missing`

HTTP 400 · Missing Email

### `email_not_verified`

HTTP 401 · Email not verified

See [Authentication](auth.md#oauth).

### `email_verification_required`

HTTP 403 · Email verification required

See [Authentication](auth.md#email-verification).

### `empty_body`

HTTP 400 · Request body is empty

### `env_batch_request_conflict`

HTTP 409 · Request ID already used

### `env_batch_revision_conflict`

HTTP 409 · Configuration changed

### `env_scope_invalid`

HTTP 400 · Invalid env scope

See [Environment variables](env.md#scopes).

### `env_scope_reserved`

HTTP 400 · Env scope reserved

See [Environment variables](env.md#scopes).

### `env_value_too_large`

HTTP 413 · Invalid sidecar env; Env var value too large

See [Environment variables](env.md#limits).

### `env_var_invalid_key`

HTTP 400 · Invalid env var key

See [Environment variables](env.md#keys).

### `env_var_missing`

HTTP 422 · Missing environment variable

### `env_var_not_found`

HTTP 400 · Env var not set

See [Environment variables](env.md).

### `environment_cleanup_pending`

HTTP 409 · Environment cleanup in progress

### `environment_field_ownership_conflict`

HTTP 409 · Field ownership conflict

### `environment_full_clone_unavailable`

HTTP 409 · Full environment clone unavailable

### `environment_promotion_attempt_accepted`

HTTP 409 · Environment promotion already accepted

### `environment_promotion_attempt_conflict`

HTTP 409 · Environment promotion attempt conflict

### `environment_queue_collection_unavailable`

HTTP 409 · Stage queue collection unavailable

### `environment_rollback_attempt_accepted`

HTTP 409 · Environment rollback already accepted

### `environment_rollback_attempt_conflict`

HTTP 409 · Environment rollback attempt conflict

### `environment_work_busy`

HTTP 409 · Environment work is running

### `environment_work_ownership_conflict`

HTTP 409 · Environment work ownership conflict

### `environment_work_policy_collection_unavailable`

HTTP 409 · Stage policy collection unavailable

### `event_publish_batch_too_large`

HTTP 413 · Batch too large

See [Errors](errors.md).

### `event_publish_not_attempted`

HTTP 503 · Event not attempted

See [Errors](errors.md).

### `event_storage_capacity_exhausted`

HTTP 429 · Event storage capacity exhausted

See [Errors](errors.md).

### `event_stream_unavailable`

The response's `title` and `detail` describe the failure.

See [Errors](errors.md).

### `exclude_only_overlap`

HTTP 409 · Workload is in both --only and --exclude

### `exclude_unknown_slug`

HTTP 400 · Exclude slug is not a workload in this commit

### `execution_mode_conflict`

HTTP 422 · Execution mode conflicts with the app's behavior

See [Errors](errors.md).

### `export_rate_limited`

HTTP 429 · Export rate limited

See [Account and organization management](account.md#export-rate-limit).

## F

### `financial_budget_activation_unavailable`

HTTP 422 · Budget activation unavailable; Organization budgets unavailable; Strict budgets not available for organizations

See [Billing](billing.md).

### `financial_budget_held`

HTTP 402 · Compute budget held; Preview compute budget reached

### `financial_compute_budget_too_low`

HTTP 422 · Compute budget below committed funding

### `financial_compute_not_ready`

HTTP 422 · Strict compute activation unavailable; Job compute authority unavailable

### `financial_compute_unavailable`

HTTP 503 · Compute admission unavailable

### `forged`

HTTP 403 · GitHub installation could not be verified

### `function_init_failed`

HTTP 422 · Function init failed

See [Errors](errors.md), [Functions and runtimes](functions.md).

### `function_input_invalid`

The response's `title` and `detail` describe the failure.

See [Errors](errors.md), [Functions and runtimes](functions.md).

### `function_send_rate_limited`

HTTP 429 · Function send rate limited

See [Event-driven workloads](event-driven.md#async-backlog).

## G

### `gateway_overloaded`

HTTP 503 · Gateway overloaded

### `geo_denied`

HTTP 403 · Country not allowed; Country denied

See [Edge rules](edge-rules.md), [Errors](errors.md).

### `github_account_conflict`

HTTP 409 · GitHub deployment connection could not be saved

See [Deploys](deploys.md).

### `github_app_not_installed`

HTTP 404 · Gregale GitHub App not installed

Install the Gregale GitHub App on your account or organization, then connect again.

See [Deploys](deploys.md).

### `github_install_ambiguous`

HTTP 409 · GitHub installation is ambiguous

### `github_install_not_found`

HTTP 404 or 409 · GitHub connection not found; GitHub installation is no longer available; No GitHub installation

See [Deploy from a GitHub source ref](source-ref.md#prereq).

### `github_install_not_owned`

HTTP 403 · GitHub installation is not connected to this account

### `github_installation_choice_required`

HTTP 409 · Choose a GitHub installation

See [Deploys](deploys.md).

### `github_installation_not_accessible`

HTTP 403 · GitHub installation not accessible

Sign in to GitHub as a user with access to that installation, then connect again.

See [Deploys](deploys.md).

### `github_installation_owner_required`

HTTP 403 · Only the owner can connect this installation

Sign in to GitHub as that user, or install the Gregale GitHub App on your own account.

See [Deploys](deploys.md).

### `github_installation_required`

HTTP 409 · Choose a GitHub installation

See [Deploys](deploys.md).

### `github_integration_unavailable`

HTTP 503 · GitHub integration unavailable

### `github_not_bound`

HTTP 409 · GitHub is not connected to this app

### `github_org_admin_required`

HTTP 403 · Organization admin required

Ask an organization admin to connect GitHub, or to make you an admin.

See [Deploys](deploys.md).

### `github_permission_pending`

HTTP 403 · GitHub permission not accepted

Ask an organization owner to review the pending permission request in the organization's GitHub App settings.

See [Deploys](deploys.md).

### `github_repo_not_accessible`

HTTP 403 · Repository is not accessible

See [Deploys](deploys.md).

### `github_unreachable`

HTTP 503 · GitHub Unreachable; Could not reach GitHub

Gregale could not complete the GitHub connection. Try again in a minute; if it continues, contact support.

See [Deploys](deploys.md).

### `gitlab_company_project_unavailable`

HTTP 409 · GitLab unavailable for company projects

### `gitlab_connection_required`

HTTP 409 · GitLab connection required

### `google_id_token_invalid`

HTTP 400 · OAuth Failed

### `google_id_token_missing`

HTTP 400 · OAuth Failed

### `google_identity_mismatch`

HTTP 400 · OAuth Failed

### `google_unreachable`

HTTP 503 · Google Unreachable

## H

### `handler_missing`

HTTP 400 · Handler required

See [Functions and runtimes](functions.md).

### `header_mutation_forbidden`

HTTP 422 · Header mutation forbidden

### `http_admission_unavailable`

HTTP 503 · Request admission unavailable

### `http_error`

The response's `title` and `detail` describe the failure.

## I

### `idempotency_conflict`

HTTP 409 · Idempotency key conflict

### `idempotency_key_reused`

HTTP 422 · Idempotency-Key reused

See [Errors](errors.md).

### `image_abuse_detected`

The response's `title` and `detail` describe the failure.

See [Security](security.md).

### `image_egress_denied`

HTTP 403 · Egress denied

### `image_manifest_invalid`

HTTP 422

See [Container compatibility](container-compatibility.md).

### `image_not_found`

HTTP 422

### `image_required`

HTTP 400 · Image digest required

### `image_secret_detected`

The response's `title` and `detail` describe the failure.

### `inbound_webhook_backlog_full`

HTTP 429 · Inbound webhook backlog full

See [Event-driven workloads](event-driven.md#inbound-webhook-backlog).

### `inbound_webhook_bad_signature`

HTTP 400 · Invalid webhook signature

### `inbound_webhook_invalid`

HTTP 400 · Invalid inbound webhook

### `inbound_webhook_rate_limited`

HTTP 429 · Inbound webhook rate limited

See [Event-driven workloads](event-driven.md#inbound-webhook-rate-limit).

### `inbound_webhook_routing_conflict`

HTTP 409 · Endpoint already routed

### `inbound_webhook_too_large`

HTTP 413 · Webhook body too large

### `init_sidecar_failed`

The response's `title` and `detail` describe the failure.

### `instance_busy`

HTTP 409 or 503 · Function still running; Instance busy

### `insufficient_scope`

HTTP 403 · Many operations send this code; the response's `title` and `detail` name the failure.

### `internal`

HTTP 500 · List failed; Delete failed; Lookup failed

See [Application companions](companions.md), [Networking](networking.md).

### `internal_error`

HTTP 500 or 503 · Many operations send this code; the response's `title` and `detail` name the failure.

### `invalid_autoscale_target_cpu_pct`

HTTP 422 · Invalid autoscale target CPU%

### `invalid_autoscale_target_rps`

HTTP 422 · Invalid autoscale target RPS

### `invalid_body`

HTTP 400 · Invalid JSON body

### `invalid_canary_preset`

HTTP 422 · Invalid canary preset; Invalid canary rollout

See [Deploys](deploys.md#canary-presets).

### `invalid_cooldown`

HTTP 422 · Invalid cooldown

See [Apps](apps.md#scaling-policy).

### `invalid_cpu_millicores`

HTTP 422 · Invalid CPU

### `invalid_cpu_ram_pair`

HTTP 422 · Invalid CPU/RAM pair

See [Plans and pricing](plans.md#resources).

### `invalid_credentials`

HTTP 401 · Sign in failed

See [Authentication](auth.md#sign-in).

### `invalid_cursor`

HTTP 400 · Invalid cursor

### `invalid_egress_allowlist`

HTTP 400 · Invalid egress allowlist entry

See [Apps](apps.md#egress-allowlist).

### `invalid_egress_port`

HTTP 400 · Invalid egress port

See [Apps](apps.md#egress-ports).

### `invalid_max_instances`

HTTP 422 · Invalid max_instances

See [Apps](apps.md#max-instances).

### `invalid_min_instances`

HTTP 422 · Invalid min_instances; Min_instances must be >= 0

See [Apps](apps.md#min-instances).

### `invalid_mirror_percent`

HTTP 422 · Invalid mirror percent

See [Apps](apps.md#mirror-rules).

### `invalid_mirror_window`

HTTP 422 · Invalid mirror window

See [Apps](apps.md#mirror-summary).

### `invalid_nonce`

HTTP 400 · Invalid OAuth Request

### `invalid_openapi_shape`

HTTP 400 · Doc must be a JSON object with a top-level 'openapi' or 'swagger' key

### `invalid_pkce`

HTTP 400 · Invalid OAuth Request

### `invalid_public_auth_ip_allowlist`

HTTP 400 · Invalid public-auth IP allowlist entry

See [Apps](apps.md#public-auth-ip-allowlist).

### `invalid_ram_mb`

HTTP 422 · Invalid RAM

### `invalid_recover_action`

HTTP 422 · Invalid recover_rollout action

See [Deploys](deploys.md#recover-rollout).

### `invalid_ref`

HTTP 400 · Invalid ref

See [Deploy from a GitHub source ref](source-ref.md).

### `invalid_registry_host`

HTTP 400 · Invalid registry host

See [Registry credentials](registry-credentials.md#registry-format).

### `invalid_request`

HTTP 400 · Many operations send this code; the response's `title` and `detail` name the failure.

### `invalid_resource_profile`

HTTP 422 · Conflicting resource profile; Invalid resource profile

### `invalid_scheduled_at`

HTTP 400 · Invalid scheduled_at

See [Event-driven workloads](event-driven.md#delayed-tasks).

### `invalid_source`

HTTP 400 · Source must be one of: cold_boot, manual_upload; Source must be one of: manual_import, auto

### `invalid_state`

HTTP 400 · Invalid State

### `invalid_traffic_percent`

HTTP 422 · Invalid traffic_percent

See [Deploys](deploys.md#traffic-percent).

### `invalid_warm_pool_size`

HTTP 422 · Invalid warm_pool_size; Invalid max_concurrency for warm_pool_size

### `invalid_warm_snapshot_min_ms`

HTTP 422 · Invalid warm_snapshot_min_ms

### `invalid_warm_snapshot_min_requests`

HTTP 422 · Invalid warm_snapshot_min_requests

### `invocation_environment_work_isolation_unavailable`

HTTP 409 · Stage work unavailable

### `invocation_not_found`

HTTP 404 · Invocation not found

See [Event-driven workloads](event-driven.md#invocations).

### `invocation_not_replayable`

HTTP 409 · Invocation not replayable; Invocation is not in a replayable state

See [Event-driven workloads](event-driven.md#invocations).

### `invocation_replay_unavailable`

HTTP 409 · Invocation replay unavailable

### `invocation_version_conflict`

HTTP 409 · Invocation version conflict

### `invocation_version_unavailable`

HTTP 410 · Invocation version unavailable

### `invocation_wait_interrupted`

Invocation wait interrupted

See [CLI guide](cli-setup.md).

### `invocation_wait_timeout`

HTTP 408 · Invocation wait timed out

See [CLI guide](cli-setup.md).

### `ip_denied`

HTTP 403 · IP address not allowed; IP denied; IP not allowed

See [Edge rules](edge-rules.md), [Errors](errors.md).

## J

### `job_command_invalid`

HTTP 422 · Job command invalid

See [Jobs](jobs.md#command).

### `job_deadline_exceeded`

HTTP 410 · Job run deadline exceeded

See [Jobs](jobs.md#deadlines).

### `job_has_live_instances`

HTTP 409 · Job has active work; Job has live instances

See [Jobs](jobs.md#delete).

### `job_image_missing`

HTTP 422 · Job image not found

See [Jobs](jobs.md#image).

### `job_quota_exceeded`

HTTP 403 · Job limit reached

See [Plans and pricing](plans.md#jobs).

### `job_registry_credential_not_found`

HTTP 400 · Job registry credential not set

See [Jobs](jobs.md#registry-credentials).

### `job_run_cancelled`

HTTP 409 · Job run is already terminal

See [Jobs](jobs.md#cancel).

### `job_task_max_retries_reached`

HTTP 409 · Job task retry budget exhausted

See [Jobs](jobs.md#retry).

### `job_task_not_found`

HTTP 404 · Job task not found; Task not found

See [Jobs](jobs.md#tasks).

### `job_task_not_retriable`

HTTP 409 · Job task cannot be retried

See [Jobs](jobs.md#retry).

### `jobs_not_allowed`

HTTP 402 · Jobs unavailable on this plan

See [Plans and pricing](plans.md#jobs).

### `json_error`

HTTP 500 · Internal Error

### `jwt_audience_mismatch`

HTTP 401 · JWT audience mismatch

### `jwt_missing_issuer`

HTTP 401 · JWT issuer mismatch

### `jwt_missing_token`

HTTP 401 · JWT bearer token required

### `jwt_signature_invalid`

HTTP 401 · JWT signature invalid

## L

### `log_archive_degraded`

HTTP 503 · Archived logs unavailable

See [CLI guide](cli-setup.md).

### `log_archive_invalid_query`

HTTP 400 · Invalid archived log request

Choose an instance and a date formatted as YYYY-MM-DD, then retry.

See [Logs](logs.md), [Errors](errors.md).

### `log_archive_missing`

HTTP 404 · Archived logs not found

See [CLI guide](cli-setup.md).

### `log_archive_retention_exceeded`

HTTP 403 · Archived log date is outside the retention window

Choose a more recent date, or review plans with longer log retention.

See [Plans and pricing](plans.md#log-archive), [Errors](errors.md).

### `log_archive_unconfigured`

HTTP 503 · Archived logs temporarily unavailable

Retry shortly, or use `gregale logs APP` for live logs.

See [Logs](logs.md), [Errors](errors.md).

### `log_reads_busy`

HTTP 429 · Too many log reads

### `log_streams_limit`

HTTP 429 · Too many log streams

See [Logs](logs.md#limits).

### `long_poll_timeout`

HTTP 503 · Long-poll wait budget ran out

See [Event-driven workloads](event-driven.md#long-poll).

## M

### `managed_postgres_ambiguous`

HTTP 409 · Managed PostgreSQL database reference is ambiguous

### `managed_postgres_binding_changed`

HTTP 409 · Gregale.yaml changes an existing binding

Bind the new database or access under another env key and switch the app to it, or delete the old binding (gregale postgres bindings) and deploy again.

See [Managed PostgreSQL](postgres.md).

### `managed_postgres_binding_limit`

HTTP 403 · Managed PostgreSQL binding limit reached

Delete a binding the app no longer uses (gregale postgres bindings delete) or upgrade the plan.

See [Plans and pricing](plans.md).

### `managed_postgres_binding_not_ready`

HTTP 409 · Managed PostgreSQL binding is not ready

### `managed_postgres_binding_target_missing`

HTTP 409 · Database binding target missing

### `managed_postgres_forbidden`

HTTP 403 · Cannot create managed PostgreSQL database

### `managed_postgres_not_found`

HTTP 404 · Managed PostgreSQL database not found; Managed PostgreSQL resource not found

### `managed_postgres_not_in_plan`

HTTP 403 · Managed PostgreSQL is not included in this plan

### `managed_postgres_not_ready`

HTTP 409 · Managed PostgreSQL database is not ready

### `managed_postgres_plan_unknown`

HTTP 422 · Managed PostgreSQL plan unknown

### `managed_postgres_quota_exceeded`

HTTP 403 · Managed PostgreSQL quota exceeded

Delete a database you no longer need or upgrade the plan, then try again; or restore the database with gregale postgres restore and attach the copy yourself.

See [Plans and pricing](plans.md), [Managed PostgreSQL](postgres.md#storage), [Safe releases](releases.md).

### `managed_postgres_unavailable`

HTTP 503 · Managed PostgreSQL unavailable

See [Managed PostgreSQL](postgres.md).

### `max_min_instances_exceeded`

HTTP 422 · Min_instances exceeds plan cap

See [Apps](apps.md#min-instances).

### `method_not_allowed`

HTTP 405 · MCP instance probe failed; Method not allowed; Routes read failed

### `mfa_disable_cooldown`

HTTP 425 · MFA disable cooldown active

### `mfa_invalid_code`

HTTP 401 · Invalid code; Invalid credentials; Invalid token

### `mfa_last_recovery_code`

HTTP 409 · Last recovery code

### `mfa_required`

HTTP 403 or 409 · MFA required; MFA setup incomplete; Session required

### `migration_recovery_blocked`

HTTP 409 · Migration recovery blocked

### `mirror_cross_app_mismatch`

HTTP 422 · Source and mirror deployments must belong to the same app

See [Apps](apps.md#mirror-rules).

### `mirror_deployment_not_live`

HTTP 409 · Referenced deployment is not live

See [Apps](apps.md#mirror-rules).

### `mirror_rule_not_found`

HTTP 404 · Mirror rule not found

See [Apps](apps.md#mirror-rules).

### `mirror_rule_quota_exceeded`

HTTP 422 · Mirror rule quota exceeded

See [Apps](apps.md#mirror-rules).

### `mirror_slot_at_capacity`

HTTP 503

### `mirror_source_target_same`

HTTP 422 · Source and mirror deployments must differ

See [Apps](apps.md#mirror-rules).

### `misdirected_request`

HTTP 421 · Misdirected request

### `missing_code`

HTTP 400 · Authorization Error

### `missing_doc`

HTTP 400 · Doc field is empty; Set_doc=true with a non-empty doc is required

## N

### `no_live_deployment`

HTTP 409 · App has no live deployment; No live deployment

See [CLI guide](cli-setup.md).

### `no_rollback_target`

HTTP 409 · A release is still rolling out; Choose a rollback target; More than one environment serves this app

See [Deploys](deploys.md#rollback).

### `not_found`

HTTP 404 or 410 · Many operations send this code; the response's `title` and `detail` name the failure.

### `notification_channel_in_use`

HTTP 409 · Channel has open incidents; Notification channel in use

See [Alerts](alerts.md#notification-channels).

### `notification_channel_invalid`

HTTP 400 or 409 · Channel name already exists; Invalid notification channel

See [Alerts](alerts.md#notification-channels).

### `notification_channel_limit_reached`

HTTP 422 · Notification channel limit reached

See [Alerts](alerts.md#notification-channels).

### `notification_channel_test_rate_limited`

HTTP 429 · Notification channel test rate limited

See [Alerts](alerts.md#notification-channels).

## O

### `oauth_exchange_failed`

HTTP 400 or 503 · OAuth Failed

### `oauth_provider_unavailable`

HTTP 503 · GitHub connection unavailable; OAuth Provider Unavailable

Try again later, or contact support if you need to connect GitHub.

Use another sign-in method, or contact support if you need this provider.

See [Authentication](auth.md), [Errors](errors.md).

### `openapi_docs_not_allowed`

HTTP 402 · Endpoint discovery unavailable on this plan; Stored route checks unavailable

See [Plans and pricing](plans.md#endpoint-discovery).

### `openapi_import_invalid`

HTTP 422 or 500 · Persisted OpenAPI document could not be analyzed; Failed to walk imported doc for dry-run; Imported OpenAPI doc failed validation

### `openapi_import_quota_reached`

HTTP 403 · Per-account OpenAPI import quota reached

### `openapi_import_too_large`

HTTP 413 · Imported doc exceeds size cap

### `openapi_import_too_many_endpoints`

HTTP 422 · Imported doc declares too many endpoints

### `openapi_policy_confirmation_required`

HTTP 400 · OpenAPI policy confirmation is required

### `openapi_policy_stale`

HTTP 409 · OpenAPI policy plan is stale

### `org_already_member`

HTTP 409 · Already a member of this organization

See [Account and organization management](account.md#members).

### `org_api_key_requires_org`

HTTP 409 · Organization key required; API key must be bound to an organization; Organization project path required

See [Account and organization management](account.md#api-keys).

### `org_free_limit_exceeded`

HTTP 403 · Free organization limit reached

See [Account and organization management](account.md#free-organizations).

### `org_invitation_cap_exceeded`

HTTP 403 · Organization invitation limit reached

See [Account and organization management](account.md#invitation-cap).

### `org_invitation_expired`

HTTP 410 · Invitation expired

See [Account and organization management](account.md#invitations).

### `org_invitation_invalid`

HTTP 410 · Invitation invalid

See [Account and organization management](account.md#invitations).

### `org_last_owner`

HTTP 409 · Cannot remove the last owner; Owner cannot leave

See [Account and organization management](account.md#ownership).

### `org_member_cap_exceeded`

HTTP 403 · Organization member limit reached

See [Account and organization management](account.md#member-cap).

### `org_mfa_required`

HTTP 403 or 409 · Turn on two-factor authentication first; Two-factor authentication required

See [Account and organization management](account.md#require-two-factor-authentication).

### `org_not_found`

HTTP 404 · Organization not found

See [Account and organization management](account.md).

### `org_personal_immutable`

HTTP 409 · Personal organizations are immutable

See [Account and organization management](account.md#personal-orgs).

### `org_role_forbidden`

HTTP 403 · Application standards mutation forbidden; Approver not allowed; Insufficient role for this action

Ask a teammate the policy lets approve to approve this promotion, then promote with their approval.

See [Account and organization management](account.md#roles), [Deploys](deploys.md#release-approvals).

### `org_slug_invalid`

HTTP 422 · Invalid org plan; Invalid organization slug

See [Account and organization management](account.md#slugs).

### `org_slug_taken`

HTTP 409 · Organization slug in use

See [Account and organization management](account.md#slugs).

### `outbound_probe_unavailable`

The response's `title` and `detail` describe the failure.

See [Errors](errors.md).

### `overage_cap_above_max`

HTTP 422 · Spend cap above plan maximum

See [Billing](billing.md#spend-cap).

### `overage_cap_required`

HTTP 422 · Spend cap required

See [Billing](billing.md#spend-cap).

## P

### `password_too_weak`

HTTP 400 · Password too weak

See [Authentication](auth.md#password).

### `payment_authentication_required`

HTTP 402 · Bank confirmation required

See [Billing](billing.md#upgrades).

### `payment_required`

HTTP 402 · Billing confirmation required; Upgrade not charged; Billing subscription required

### `penalty_box`

HTTP 403 · Temporarily blocked

See [Edge rules](edge-rules.md), [Security](security.md).

### `plan_alert_presets_not_allowed`

HTTP 402 · Alert preset not available on this plan

See [Plans and pricing](plans.md#alerts).

### `plan_alert_rule_quota`

HTTP 403 · Alert rule limit reached

See [Plans and pricing](plans.md#alerts).

### `plan_alert_rules_not_allowed`

HTTP 402 · Alert rules unavailable on this plan

See [Plans and pricing](plans.md#alerts).

### `plan_app_errors_not_allowed`

HTTP 402 · App error grouping unavailable on this plan

See [Plans and pricing](plans.md#app-errors).

### `plan_app_protocol_grpc_not_allowed`

HTTP 403 · Per-app gRPC wire protocol is not allowed on this plan

### `plan_app_usage_summary_not_allowed`

HTTP 402 · App usage summary unavailable on this plan

See [Plans and pricing](plans.md#app-usage-summary).

### `plan_async_backlog`

HTTP 429 · Per-app async backlog full

See [Event-driven workloads](event-driven.md#async-backlog).

### `plan_autoscale_not_allowed`

HTTP 403 · Autoscale target CPU% is not allowed on this plan; Autoscale target RPS is not allowed on this plan

### `plan_cors_preset_quota_reached`

HTTP 403 · CORS preset limit reached

See [Plans and pricing](plans.md#cors-presets).

### `plan_cors_presets_not_allowed`

HTTP 402 · CORS presets unavailable on this plan

See [Plans and pricing](plans.md#cors-presets).

### `plan_cron_quota`

HTTP 403 · Cron limit reached; Cron over plan limit

See [Plans and pricing](plans.md#crons).

### `plan_crons_not_allowed`

HTTP 402 · Crons unavailable on this plan

See [Plans and pricing](plans.md#crons).

### `plan_custom_metrics_not_allowed`

HTTP 402 · Custom metrics not available on this plan

### `plan_delayed_tasks_cap`

HTTP 403 · Per-app delayed-task cap exceeded

See [Event-driven workloads](event-driven.md#delayed-tasks).

### `plan_edge_rule_kind_not_allowed`

HTTP 402 · Edge rule kind unavailable on this plan

See [Plans and pricing](plans.md#edge-rules), [Edge rules](edge-rules.md).

### `plan_edge_rule_kind_quota_reached`

HTTP 403 · Edge rule kind quota reached

See [Plans and pricing](plans.md#edge-rules), [Edge rules](edge-rules.md).

### `plan_egress_allowlist_not_allowed`

HTTP 403 · Plan doesn't allow an egress allowlist

See [Apps](apps.md#egress-allowlist).

### `plan_egress_overage_not_allowed`

HTTP 402 · Plan cannot elect egress overage

See [Billing](billing.md).

### `plan_egress_ports_not_allowed`

HTTP 403 · Plan doesn't allow extra egress ports

See [Apps](apps.md#egress-ports).

### `plan_empty`

HTTP 422 · Project plan is empty

### `plan_eviction_priority_reserved_not_allowed`

HTTP 402 · Reserved eviction priority is not available on this plan

See [Plans and pricing](plans.md#eviction-priority).

### `plan_eviction_priority_reserved_quota`

HTTP 422 · Reserved eviction priority quota reached

See [Plans and pricing](plans.md#eviction-priority).

### `plan_feature_gated`

HTTP 402 · Many operations send this code; the response's `title` and `detail` name the failure.

### `plan_feature_unavailable`

HTTP 403 · Bucket triggers unavailable

### `plan_gate_blocked`

HTTP 403 · Plan gate blocked

### `plan_health_path_wakes_not_allowed`

HTTP 403 · Health-path wakes are not allowed on this plan

### `plan_inbound_webhook_quota`

HTTP 403 · Inbound webhook endpoint limit reached

See [Event-driven workloads](event-driven.md#inbound-webhooks).

### `plan_inbound_webhooks_not_allowed`

HTTP 402 · Inbound webhooks unavailable on this plan

See [Event-driven workloads](event-driven.md#inbound-webhooks).

### `plan_internal_ingress_not_allowed`

HTTP 402 · Plan does not unlock internal-only ingress

See [Networking](networking.md).

### `plan_job_registry_credential_quota`

HTTP 413 · Per-job registry credential quota reached

See [Jobs](jobs.md#registry-credentials).

### `plan_job_registry_credentials_not_allowed`

HTTP 403 · Plan doesn't allow private-registry credentials for jobs

See [Jobs](jobs.md#registry-credentials).

### `plan_limit_apps`

HTTP 403 · App limit reached; Environment branch limit reached; PR preview environment too large

See [Plans and pricing](plans.md#apps).

### `plan_limit_build_minutes`

HTTP 403 · Build minutes used up

See [Plans and pricing](plans.md#builds).

### `plan_limit_concurrency`

HTTP 403 or 429 · Concurrency limit reached; Concurrency over plan limit; Execution concurrency limit reached

See [Plans and pricing](plans.md#concurrency), [Gregale API specification](faas_openapi_spec.md).

### `plan_limit_deployment_aliases`

HTTP 403 · Deployment alias limit reached

See [Deploys](deploys.md#deployment-aliases).

### `plan_limit_developer_apps`

HTTP 403 · Developer environment limit reached

See [Plans and pricing](plans.md#developer-environments).

### `plan_limit_developer_lease`

HTTP 403 · Developer environment lease over plan limit

See [Plans and pricing](plans.md#developer-environments), [`gregale dev`](gregale-dev.md).

### `plan_limit_edge_rule_list_items`

HTTP 403 · Edge rule list too large

See [Plans and pricing](plans.md#edge-rules).

### `plan_limit_edge_rule_lists`

HTTP 403 · Edge rule list limit reached

See [Plans and pricing](plans.md#edge-rules).

### `plan_limit_edge_rules`

HTTP 403 · Edge rule limit reached

See [Plans and pricing](plans.md#edge-rules), [Edge rules](edge-rules.md).

### `plan_limit_env_vars`

HTTP 403 · Env var count limit reached

See [Environment variables](env.md#limits).

### `plan_limit_project_secrets`

HTTP 403 · Project secret limit reached

See [Delivered secrets](secrets.md#project-environment-secrets).

### `plan_limit_ram`

HTTP 403 · RAM over plan limit

See [Plans and pricing](plans.md#ram).

### `plan_limit_secrets`

HTTP 403 · Secret count limit reached

See [Delivered secrets](secrets.md#limits).

### `plan_limit_trusted_signers`

HTTP 403 · Trusted signer count limit reached; Trusted signers are not allowed on this plan

See [Deploys](deploys.md#trusted-signers).

### `plan_liveness_probe_not_allowed`

HTTP 403 · GRPC liveness probes are not allowed on this plan; Liveness probes are not allowed on this plan; Plan doesn't allow a liveness probe

See [Deployment overrides](deploy-overrides.md#liveness-probe).

### `plan_log_archive_not_allowed`

HTTP 402 · Log archive unavailable on this plan

See [Plans and pricing](plans.md#log-archive).

### `plan_log_drain_quota`

HTTP 403 · Log drain limit reached

See [Plans and pricing](plans.md#observability).

### `plan_log_drains_not_allowed`

HTTP 402 · Log drains unavailable on this plan

See [Plans and pricing](plans.md#observability).

### `plan_max_instances_not_allowed`

HTTP 403 · Plan doesn't allow a max_instances ceiling

See [Apps](apps.md#max-instances).

### `plan_min_instances_not_allowed`

HTTP 403 · Plan doesn't allow a min-instances floor

See [Plans and pricing](plans.md#min-instances).

### `plan_mirror_not_allowed`

HTTP 403 · Plan doesn't allow traffic mirroring

See [Plans and pricing](plans.md#traffic-mirror).

### `plan_openapi_doc_quota_reached`

HTTP 403 · OpenAPI document limit reached

See [Plans and pricing](plans.md#endpoint-discovery).

### `plan_openapi_doc_too_large`

HTTP 413 · OpenAPI document exceeds the per-plan byte cap

See [Plans and pricing](plans.md#endpoint-discovery).

### `plan_per_app_metrics_not_allowed`

HTTP 402 · Per-app metrics unavailable on this plan

See [Plans and pricing](plans.md#per-app-metrics).

### `plan_private_network_not_allowed`

HTTP 402 · Plan does not unlock private network attachments

See [Networking](networking.md).

### `plan_public_auth_basic_not_allowed`

HTTP 402 · Basic public-URL auth is not available on this plan

See [Plans and pricing](plans.md#public-auth).

### `plan_public_auth_bearer_not_allowed`

HTTP 402 · Bearer public-URL auth is not available on this plan

See [Plans and pricing](plans.md#public-auth).

### `plan_public_auth_ip_allowlist_not_allowed`

HTTP 403 · Plan doesn't allow a public-auth IP allowlist

See [Apps](apps.md#public-auth-ip-allowlist).

### `plan_queue_backlog_bytes`

HTTP 403 · Account queue backlog full

See [Event-driven workloads](event-driven.md#queue-backlog).

### `plan_queue_depth`

HTTP 403 · Per-app queue depth exceeded

See [Event-driven workloads](event-driven.md#queue-depth).

### `plan_registry_credential_quota`

HTTP 413 · Per-app registry credential quota reached

See [Registry credentials](registry-credentials.md#quota).

### `plan_registry_credentials_not_allowed`

HTTP 403 · Plan doesn't allow private-registry credentials

See [Registry credentials](registry-credentials.md).

### `plan_reorder_disabled`

HTTP 402 · Plan doesn't allow reorder or deploy-immediately

Cancel + clear-obsolete remain available on Free; upgrade to unlock reorder/deploy-immediately.

See [Plans and pricing](plans.md#queue-controls).

### `plan_require_authn_not_allowed`

HTTP 403 · Per-app authentication is not allowed on this plan; Per-deployment authentication is not allowed on this plan

### `plan_rollback_on_5xx_not_allowed`

HTTP 403 · Plan doesn't allow 5xx auto-rollback

See [Plans and pricing](plans.md#rollback-on-5xx).

### `plan_route_metrics_not_allowed`

HTTP 403 · Per-route metrics are not allowed on this plan

### `plan_source_bytes`

HTTP 413 · Invocation payload too large

See [Event-driven workloads](event-driven.md#payload-size).

### `plan_static_egress_ip_not_allowed`

HTTP 402 · Plan does not unlock static egress IP

See [Apps](apps.md#static-egress-ip).

### `plan_static_egress_ip_quota`

HTTP 403 · Static egress IP quota reached

See [Apps](apps.md#static-egress-ip).

### `plan_streaming_not_allowed`

HTTP 403 · Streaming responses are not allowed on this plan

### `plan_token_stale`

HTTP 409 · Plan_token does not match project binding; Plan_token does not match uploaded source; Project selection changed after review

### `plan_traffic_split_not_allowed`

HTTP 403 · Plan doesn't allow traffic splitting; Route enforcement unavailable; Route health enforcement unavailable

See [Plans and pricing](plans.md#traffic-split).

### `plan_trigger_quota`

HTTP 403 · Trigger limit reached

See [Plans and pricing](plans.md#triggers).

### `plan_triggers_not_allowed`

HTTP 402 · Triggers unavailable on this plan

See [Plans and pricing](plans.md#triggers).

### `plan_warm_pool_not_allowed`

HTTP 403 · Warm-pool capacity is not allowed on this plan

### `plan_warm_snapshot_not_allowed`

HTTP 403 · Warm-tier snapshots are not allowed on this plan

### `plan_webhook_quota`

HTTP 403 · Webhook limit reached

See [Plans and pricing](plans.md#webhooks).

### `plan_webhooks_not_allowed`

HTTP 402 · Outbound webhooks unavailable on this plan

See [Plans and pricing](plans.md#webhooks).

### `plan_websocket_not_allowed`

HTTP 403 · WebSocket / Upgrade traffic is not allowed on this plan

### `plan_workflows_not_allowed`

HTTP 402 · Workflows unavailable on this plan

See [Plans and pricing](plans.md#workflows).

### `plan_workflows_quota`

HTTP 403 · Stored workflow definition quota exceeded; Workflow run quota exceeded

See [Plans and pricing](plans.md#workflows).

### `platform_release_paused`

HTTP 503 · Releases are paused

See [Release approvals, freezes and schedules](release-controls.md).

### `postgres_custom_roles_limit`

HTTP 409 · Custom role limit reached

Delete a role you no longer need (gregale postgres roles delete).

### `postgres_custom_roles_unavailable`

HTTP 403 · Postgres custom roles unavailable

### `postgres_custom_roles_unsupported`

HTTP 422 · Custom roles unsupported

### `pr_preview_resource_unsupported`

HTTP 409

Give previews test credentials in the production app's pr-preview scope: gregale env set --app &lt;app> --env pr-preview KEY=value

See [PR preview environments](preview-environments.md).

### `preflight_busy`

Preflight is busy

### `preflight_check_failed`

Check failed

### `preflight_invalid_source`

Not a GitHub repository; Invalid ref

### `preflight_rate_limited`

Too many checks

### `preflight_repo_not_found`

Repository not found

### `preflight_source_too_large`

Repository too large

### `preflight_upstream_unavailable`

GitHub is rate limiting us

### `preview_environment_not_found`

HTTP 404 · Preview environment not found

### `preview_expired`

HTTP 410 · Review source again; Review this project again; Preview source expired

### `preview_full`

HTTP 409 · Preview is full

See [CLI guide](cli-setup.md#previews).

### `preview_not_found`

HTTP 404 · Preview not found

### `preview_not_offered`

HTTP 404 · Preview not offered

See [CLI guide](cli-setup.md#previews).

### `preview_org_unsupported`

HTTP 409 · Preview cannot be joined by an organization

See [CLI guide](cli-setup.md#previews).

### `preview_plan_not_eligible`

HTTP 403 · Plan not eligible for this preview

See [CLI guide](cli-setup.md#previews).

### `preview_production_dependency_denied`

HTTP 403 · Preview dependency denied

See [Internal services](internal-services.md), [PR preview environments](preview-environments.md).

### `private_invocation_disabled`

HTTP 503 · Private invocation unavailable

### `private_network_fabric_unavailable`

Private network fabric unavailable

### `private_network_invalid`

HTTP 400 · Invalid private network attachment

See [Networking](networking.md).

### `private_network_not_enabled`

HTTP 503 · Private network attachments are not enabled on this cluster

See [Networking](networking.md).

### `private_network_transport_unavailable`

Private network transport unavailable

### `private_network_unavailable`

Private network firewall updates unavailable; Private network policy updates unavailable; Private network attachment updates unavailable

### `private_publication_disabled`

HTTP 503 · Private publication unavailable

### `prod_branch_mismatch`

HTTP 409 · Production branch does not match the existing project

### `project_apply_stale`

HTTP 409 · Project changed during apply

### `project_configuration_failed`

HTTP 503 · Project configuration incomplete

### `project_environment_approval_invalid`

HTTP 409 · Many operations send this code; the response's `title` and `detail` name the failure.

### `project_environment_approval_required`

HTTP 409 · Protected environment; Protected environment approval required

Change it in the source environment and promote, or run `gregale project apply` and approve the plan. Deleting or rotating a secret stays open for incident response.

Run `gregale project apply` or `gregale project promote` and approve the plan, or deploy with no traffic to stage the release.

### `project_promotion_queue_full`

HTTP 429 · Promotion queue is full

See [Deploys](deploys.md#promotion-admission), [Environment promotion](environment-promotion.md).

### `project_release_baseline_expired`

HTTP 410 · Rollback baseline expired

See [Atomic project releases](project-releases.md).

### `project_release_control_conflict`

HTTP 409 · Deployment control conflicted

### `project_release_rollback_required`

HTTP 409 · Project release rollback required

Roll back the project environment release, then retry this app rollback if needed.

### `project_release_secrets_pinned`

HTTP 409 · Secret versions are release-bound

Bind the bucket in an environment whose release does not pin secrets, or release without pinned secrets first.

Bind the database in an environment whose release does not pin secrets, or release without pinned secrets first.

See [Delivered secrets](secrets.md).

### `project_required`

HTTP 409 · Project required

### `project_retry_changed`

HTTP 409 · Project retry stopped

### `project_selection_empty`

HTTP 409 · No workloads selected

See [Deploy a multi-service app](multi-service-apps.md).

### `project_services_call_each_other`

HTTP 409 · Project services call each other

Delete the services instead (gregale app delete), or pass detach_services=true (gregale project delete --detach-services) once they no longer call each other by name.

### `project_submission_exists`

HTTP 409 · Project submission already registered

### `project_submission_mismatch`

HTTP 409 · Project submission intent changed

### `promotion_rolled_back`

The response's `title` and `detail` describe the failure.

### `provision_capacity`

HTTP 429 · Provisioning capacity reached

### `public_auth_config_invalid`

HTTP 503 · Public authentication unavailable

### `public_auth_ip_allowlist_too_long`

HTTP 400 · Public-auth IP allowlist too long

See [Apps](apps.md#public-auth-ip-allowlist).

## Q

### `queue_binding_environment_unavailable`

HTTP 409 · Queue environment unavailable

### `queue_binding_keyed_work`

HTTP 409 · Keyed work still queued

### `queue_binding_retired`

HTTP 409 · Queue binding retired

### `queue_canary_changed`

HTTP 409 · Queue canary blocked

### `queue_environment_work_policy_unavailable`

HTTP 409 · Scoped work policy unavailable

### `queue_groups_unavailable`

HTTP 403 · Ordered message groups unavailable

### `queue_message_not_processed`

HTTP 409 · Queue message not processed

### `queue_receive_busy`

HTTP 429 · Too many queue receives in flight

See [Errors](errors.md).

### `queue_replay_requires_binding`

HTTP 409 · Queue replay requires its binding

### `quota_exhausted`

HTTP 429 · Custom domain quota reached; Domain verification rate limited; Profile capture limit reached

See [Gregale API specification](faas_openapi_spec.md).

## R

### `rate_limit_unavailable`

HTTP 503 · Request admission unavailable

### `rate_limited`

HTTP 429 · Rate limit exceeded; Pre-auth rate limit exceeded

See [Edge rules](edge-rules.md).

### `realtime_unavailable`

HTTP 503 · Managed realtime temporarily unavailable

Retry the connection in a few seconds.

See [Plans and pricing](plans.md#realtime), [Errors](errors.md).

### `recent_sign_in_required`

HTTP 403 · Sign in again

See [Authentication](auth.md), [Account and organization management](account.md).

### `registry_credential_not_found`

HTTP 400 · Registry credential not set

See [Registry credentials](registry-credentials.md).

### `regression_changed`

HTTP 409 · Regression changed

### `release_approval_canary_required`

HTTP 409 · Release approvals need a canary; Release approvals need an approved promotion

Deploy with --rollout safe or fast (or without --rollout immediate and --traffic-percent), or remove the policy with `gregale approvals off`.

See [Deploys](deploys.md#release-approvals), [Release approvals, freezes and schedules](release-controls.md).

### `release_approval_decided`

HTTP 409 · Decision not accepted

### `release_approval_policy_not_found`

HTTP 404 · No approval policy

### `release_approval_rejected`

HTTP 409 · Release rejected

Deploy a new release; a rejection is final.

See [Deploys](deploys.md#release-approvals).

### `release_approval_required`

HTTP 409 · Release waiting for approval

An eligible teammate approves it on its approval page in the dashboard; the rollout then continues on its next tick.

See [Deploys](deploys.md#release-approvals).

### `release_approval_self`

HTTP 403 · Approve someone else's promotion; Self-approval not allowed

Ask an eligible teammate to approve it on the release's approval page in the dashboard.

Ask another approver to approve this promotion, then promote with their approval.

See [Deploys](deploys.md#release-approvals).

### `release_approval_session_required`

HTTP 403 · Approve as a person; Approve in the dashboard

Approve the promotion signed in as yourself, not with an API key or token, then promote with that approval.

See [Deploys](deploys.md#release-approvals).

### `release_command_failed`

HTTP 422

See [Safe releases](releases.md).

### `release_freeze_limit`

HTTP 409 · Too many freezes

Lift freezes you no longer need with `gregale freeze lift`.

See [Deploys](deploys.md#deploy-freezes).

### `release_freeze_not_found`

HTTP 404 · Freeze not found

List freezes with `gregale freeze list`. A company's freezes are lifted with --org ORG, or --app APP for a freeze on one app.

### `release_freeze_override_not_allowed`

HTTP 403 · Freeze override not allowed

Ask an owner or admin to lift the freeze with `gregale freeze lift`, or to deploy with --override-freeze.

See [Deploys](deploys.md#deploy-freezes).

### `release_frozen`

HTTP 409 · Production releases are frozen

Wait for the freeze to end, lift it with `gregale freeze lift`, or release anyway with override_freeze and a reason (CLI: --override-freeze REASON); the override is audited.

See [Deploys](deploys.md#deploy-freezes), [Release approvals, freezes and schedules](release-controls.md).

### `release_gate_limit`

HTTP 409 · Too many release gates

Delete a gate you no longer need with `gregale release-gates rm`.

See [Deploys](deploys.md#release-gates).

### `release_gate_not_found`

HTTP 404 · Release gate not found

List gates with `gregale release-gates list`.

### `release_graph_changed`

HTTP 409 · Active graph required; History unavailable; Release graph changed

### `release_infrastructure_failed`

The response's `title` and `detail` describe the failure.

See [Deploys](deploys.md), [Safe releases](releases.md).

### `release_not_parked`

HTTP 409 · Release cannot be scheduled

### `release_outcome_uncertain`

HTTP 422

See [Safe releases](releases.md).

### `release_phase_unavailable`

HTTP 422 · Release commands unavailable

### `release_scheduled`

HTTP 409 · Release is scheduled

See [Release approvals, freezes and schedules](release-controls.md).

### `release_staging_changed`

HTTP 409 · Release staging changed

### `request_body_read_failed`

HTTP 400 · Request body could not be read

See [Functions and runtimes](functions.md#limits).

### `request_budget_exceeded`

HTTP 503 · Request budget exceeded; Request timed out

See [Edge rules](edge-rules.md), [Errors](errors.md).

### `request_too_large`

HTTP 413 · Event too large; MCP instance probe failed; Request body too large

For larger uploads, use a bucket signed URL (gregale storage ... signed-url).

See [Event-driven workloads](event-driven.md), [Plans and pricing](plans.md#workflows), [Storage](storage.md#signed-uploads).

### `request_upload_timeout`

HTTP 408 · Request upload timed out

Retry on a stable connection, or use a bucket signed URL for large uploads.

See [Functions and runtimes](functions.md#limits).

### `request_validation_failed`

HTTP 422 · Invalid request

See [Edge rules](edge-rules.md).

### `reset_token_expired`

HTTP 410 · Reset link expired

See [Authentication](auth.md#reset).

### `reset_token_invalid`

HTTP 410 · Reset link invalid

See [Authentication](auth.md#reset).

### `response_too_large`

HTTP 413 · Response exceeded plan cap

### `rollback_attempt_accepted`

HTTP 409 · Rollback already accepted

### `rollback_attempt_conflict`

HTTP 409 · Rollback attempt conflict

### `rollback_crosses_release`

HTTP 409 · Rollback crosses release commands

If the target is compatible with those changes, retry with acknowledge_releases (CLI: --acknowledge-releases). To undo database changes first, restore each listed database to its restore point.

See [Deploys](deploys.md#releases-that-change-the-database), [Safe releases](releases.md).

### `rollback_deployment_changed`

HTTP 409 · Rollback selection changed; Serving release changed

### `rollback_operation_required`

HTTP 409 · Checked rollback in progress

### `rollback_target_already_live`

HTTP 409 · Rollback target is already live

See [Deploys](deploys.md#rollback).

### `rollback_target_database_restored`

HTTP 409 · Rollback target's database changes were undone; Rollback target's migration was undone

Deploy that code again; its release command runs before it serves.

See [Deploys](deploys.md#rollbacks-that-restore-the-database), [Safe releases](releases.md).

### `rollback_target_ineligible`

HTTP 409 · Rollback target is not eligible

See [Deploys](deploys.md#rollback).

### `rollback_target_not_found`

HTTP 404 · Rollback target not found

See [Deploys](deploys.md#rollback).

### `rollback_target_unavailable`

HTTP 409 · Rollback target is unavailable

See [Deploys](deploys.md#rollback).

### `rollout_not_stuck`

HTTP 409 · Rollout is not stuck

See [Deploys](deploys.md#recover-rollout).

### `rollout_state_invalid`

HTTP 409 · Rollout state does not permit recovery; Rollouts are in progress in more than one environment

See [Deploys](deploys.md#recover-rollout).

### `route_health_blocked`

HTTP 409 · Route health gate blocked

## S

### `safe_release_unavailable`

HTTP 503 · Safe release unavailable

### `scaling_request_conflict`

HTTP 409 · Scaling request ID reused

### `scaling_revision_conflict`

HTTP 409 · Scaling settings changed

### `scaling_target_incompatible_with_workload_class`

HTTP 422 · Target metric is not compatible with this app's workload class

See [Apps](apps.md#scaling-policy).

### `scan_critical`

HTTP 503 · Many operations send this code; the response's `title` and `detail` name the failure.

### `scheduled_release_not_found`

HTTP 404 · No scheduled release

### `scope_exclusion_not_found`

HTTP 404 · No persisted exclusion matches

### `secret_entry_unavailable`

HTTP 410 · Secret entry unavailable

### `secret_invalid_key`

HTTP 400 · Invalid secret key

See [Delivered secrets](secrets.md#keys).

### `secret_not_found`

HTTP 400 · Secret not set; Secret unavailable

See [Delivered secrets](secrets.md).

### `secret_reference_conflict`

HTTP 409 · Reference conflict

### `secret_reference_suppression_limit`

HTTP 403 · Suppression limit reached

See [Environment variables](env.md#secret-references).

### `secret_scan_strict`

HTTP 422 · Secret-shaped values found in uploaded source

See [Delivered secrets](secrets.md).

### `secret_store_unavailable`

HTTP 503 · Secret storage unavailable

### `secret_value_too_large`

HTTP 413 · Secret value too large

See [Delivered secrets](secrets.md#limits).

### `security_posture_blocked`

HTTP 403 or 409 or 503 · App is security quarantined; Security posture blocks this deploy

See [Security](security.md#deploy-enforcement).

### `security_quarantine_recovery_blocked`

HTTP 409 · Security quarantine recovery is blocked

See [Security](security.md#quarantine-recovery).

### `security_scan_blocked`

HTTP 422

### `service_abort_attempt_accepted`

HTTP 409 · Service abort already accepted

### `service_abort_attempt_conflict`

HTTP 409 · Service abort attempt conflict

### `service_recovery_capacity_unavailable`

HTTP 503 · Service recovery capacity unavailable

### `session_expired`

HTTP 401 · Session expired; Sign in to verify

### `session_invalid`

HTTP 401 · Session invalid

### `sidecar_cap_exceeded`

HTTP 400 · Too many sidecars

See [Application companions](companions.md#cap).

### `sidecar_invalid_cpu_millicores`

HTTP 400 · Invalid sidecar cpu_millicores

### `sidecar_invalid_disk_io_profile`

HTTP 400 · Invalid sidecar disk_io_profile

### `sidecar_invalid_image`

HTTP 400 · Invalid sidecar image

### `sidecar_invalid_name`

HTTP 400 · Invalid sidecar name

### `sidecar_invalid_port`

HTTP 400 · Invalid sidecar port

### `sidecar_invalid_ram_mb`

HTTP 400 · Invalid sidecar ram_mb

### `sidecar_invalid_scratch_mb`

HTTP 400 · Invalid sidecar scratch_mb

### `sidecar_invalid_type`

HTTP 400 · Invalid sidecar type

### `sidecar_not_allowed_on_plan`

HTTP 403 · Plan doesn't allow sidecars

See [Plans and pricing](plans.md#sidecars).

### `sidecar_stateful_denied`

HTTP 403 · Stateful sidecar image is not allowed

See [Application companions](companions.md#stateless).

### `snapshot_backoff`

HTTP 503 · Snapshot temporarily unavailable

### `snapshot_unavailable`

HTTP 410 · Snapshot unavailable

### `source_invalid`

HTTP 400 or 409 · Many operations send this code; the response's `title` and `detail` name the failure.

### `source_ref_stale`

HTTP 409 · Repository changed; Source ref is stale

See [Deploy from a GitHub source ref](source-ref.md).

### `source_ref_unavailable`

HTTP 503 · Source-ref fetch unavailable

See [Deploy from a GitHub source ref](source-ref.md#errors).

### `source_too_large`

HTTP 413 · Source too large; Entry too large

See [Builds and framework detection](build.md#limits).

### `stage_dependency_restore_failed`

The response's `title` and `detail` describe the failure.

### `stage_image_build_oom`

The response's `title` and `detail` describe the failure.

### `stage_image_build_timeout`

The response's `title` and `detail` describe the failure.

### `stage_readiness_failed`

The response's `title` and `detail` describe the failure.

### `stage_security_scan_findings`

The response's `title` and `detail` describe the failure.

### `stage_snapshot_prepare_timeout`

HTTP 422 · Snapshot prime exceeded the scheduler deadline

### `stage_source_download_failed`

The response's `title` and `detail` describe the failure.

### `stateless_only_violation`

HTTP 422 · Stateless-only platform

See [Storage](storage.md).

### `statement_invoice_export_in_progress`

HTTP 409 · Statement invoice export in progress

### `static_egress_ip_not_enabled`

HTTP 402 · Static egress IP feature is not enabled on this cluster

See [Static egress IP](static-egress-ip.md).

### `static_egress_ip_not_provisioned`

HTTP 404 · Static egress IP not provisioned on this host

See [Apps](apps.md#static-egress-ip).

### `step_up_required`

HTTP 403 · Step-up required

### `streaming_not_available`

HTTP 413 · Streaming response exceeded plan cap

### `synthetic_check_invalid`

HTTP 400 or 409 · App is internal; Invalid synthetic check; Synthetic check name already exists

See [Synthetic checks](synthetic-checks.md).

### `synthetic_check_limit_reached`

HTTP 422 · Synthetic check limit reached

See [Synthetic checks](synthetic-checks.md).

## T

### `traffic_change_during_canary`

HTTP 409 · Canary rollout controls traffic

See [Deploys](deploys.md#canary).

### `traffic_deadline_invalid`

The response's `title` and `detail` describe the failure.

### `traffic_deadline_unavailable`

The response's `title` and `detail` describe the failure.

### `traffic_percent_sum_invalid`

HTTP 409 · No deployment can take the remaining traffic; Traffic_percent sum invariant violated

See [Deploys](deploys.md#traffic-percent).

### `traffic_policy_too_complex`

HTTP 422 · Traffic policy analysis limit reached

See [Plans and pricing](plans.md#edge-rules).

### `traffic_policy_too_large`

HTTP 422 · Traffic policy too large

Reduce policy values and replace the policy. Clear an environment overlay or disable and clear its scoped route contract to recover it.

Reduce rules or preset values that match the same host. Size-reducing repairs to an existing oversized host remain allowed.

See [Plans and pricing](plans.md#edge-rules).

### `traffic_policy_unavailable`

HTTP 503 · Traffic policy unavailable

See [Edge rules](edge-rules.md).

### `traffic_revocation_unavailable`

HTTP 503 · Traffic security verification unavailable

### `traffic_revoked`

HTTP 403 · Traffic revoked

### `traffic_serving_changed`

HTTP 409 · Production revision changed; Traffic split changed

See [Deploys](deploys.md#traffic-percent), [Progressive rollouts](rollouts.md).

### `traffic_split_release_limit`

HTTP 422 · Too many releases in the traffic split

See [Deploys](deploys.md#traffic-percent).

### `transport_error`

HTTP 503 · Could not reach Gregale

See [Errors](errors.md).

### `trigger_batch_window_too_large`

HTTP 403 · Batch_window too large for this plan

See [Plans and pricing](plans.md#triggers).

### `trigger_immutable`

HTTP 400 · Binding is immutable; Kind=cron not supported on POST /v1/triggers — use POST /v1/crons

### `trigger_inconsistent`

HTTP 500 · Trigger has no cron_id

### `trigger_invalid_config`

HTTP 422 · Many operations send this code; the response's `title` and `detail` name the failure.

### `trigger_invalid_retry_policy`

HTTP 422 · Invalid retry policy

### `trigger_kind_not_allowed`

HTTP 403 · Trigger kind not available

See [Plans and pricing](plans.md#triggers).

### `trigger_kind_unavailable`

HTTP 422 · Trigger kind unavailable

### `trigger_tls_skip_verify_not_allowed`

HTTP 403 · Tls.skip_verify unavailable on this plan

See [Plans and pricing](plans.md#triggers).

### `trusted_signer_invalid`

HTTP 400 · Trusted signer invalid

See [Deploys](deploys.md#trusted-signers).

### `trusted_signer_not_found`

HTTP 404 · Trusted signer not found

## U

### `udp_listener_limit`

HTTP 409 · UDP listener reservation limit reached

See [Deploys](deploys.md#containers).

### `unauthorized`

HTTP 401 · Many operations send this code; the response's `title` and `detail` name the failure.

### `unavailable`

Migration unavailable

See [Apps](apps.md), [Billing](billing.md), [Jobs from the CLI](cli-jobs.md).

### `undo_plan_changed`

HTTP 409 · Undo plan changed

### `undo_too_many_changes`

HTTP 422 · Too many changes

### `unsupported_by_cli`

HTTP 403 · Endpoint requires the dashboard session cookie

See [CLI guide](cli-setup.md#cookie-only-routes).

### `unsupported_media_type`

HTTP 415 · Unsupported media type

See [Edge rules](edge-rules.md).

### `upload_checksum_mismatch`

HTTP 410 · Uploaded archive does not match its checksum

Start the upload again; gregale deploy does this automatically.

See [Deploy from a GitHub source ref](source-ref.md#uploads).

### `upload_session_already_cancelled`

HTTP 409 · Upload session not open

See [Deploy from a GitHub source ref](source-ref.md#uploads).

### `upload_session_already_committed`

HTTP 409 · Upload session already committed

See [Deploy from a GitHub source ref](source-ref.md#uploads).

### `upload_session_expired`

HTTP 410 · Upload session expired

See [Deploy from a GitHub source ref](source-ref.md#uploads).

### `upload_session_not_found`

HTTP 404 · Upload session not found

See [Deploy from a GitHub source ref](source-ref.md#uploads).

### `upload_session_offset_conflict`

HTTP 409 · Upload session offset conflict

See [Deploy from a GitHub source ref](source-ref.md#uploads).

### `upload_session_too_many`

HTTP 429 · Too many upload sessions

See [Deploy from a GitHub source ref](source-ref.md#uploads).

## V

### `validation_failed`

HTTP status varies by operation · Many operations send this code; the response's `title` and `detail` name the failure.

### `verification_link_invalid`

HTTP 410 · Verification link no longer valid

Request a new verification email, then open the latest link.

See [Authentication](auth.md#email-verification), [Errors](errors.md).

## W

### `waf_blocked`

HTTP 403 · Request blocked

See [Edge rules](edge-rules.md), [Errors](errors.md).

### `wait_for_warm`

HTTP 503 · Scale-out cooldown in effect

See [Scaling policy](scaling-policy.md#cooldown).

### `wait_timed_out`

HTTP 408 · Wait timed out

### `wake_in_progress`

The response's `title` and `detail` describe the failure.

### `wake_queue_full`

The response's `title` and `detail` describe the failure.

### `webhook_outbox_full`

HTTP 429 · Webhook outbox full

See [Event-driven workloads](event-driven.md#outbox-backlog).

### `webhook_workflow_conflict`

HTTP 409 · Webhook workflow conflict

### `webhook_workflow_unavailable`

HTTP 503 · Webhook workflow unavailable

### `wildcard_domains_not_allowed`

HTTP 402 · Wildcard domains unavailable on this plan

See [Domains](domains.md#wildcard-domains).

### `work_policy_in_use`

HTTP 409 · Work policy in use

### `work_policy_reserved`

HTTP 409 · Reserved work policy

### `workflow_callback_binding_conflict`

HTTP 409 · Workflow callback binding conflict

### `workflow_callback_closed`

HTTP 409 · Workflow callback closed

### `workflow_callback_expired`

HTTP 410 · Workflow callback expired

### `workflow_callback_payload_conflict`

HTTP 409 · Workflow callback payload conflict

### `workflow_dag_cycle`

The response's `title` and `detail` describe the failure.

### `workflow_definition_not_found`

HTTP 404 · Workflow definition not found

### `workflow_deployment_unavailable`

HTTP 501 · Workflow deployment unavailable

### `workflow_event_not_found`

HTTP 404

### `workflow_event_replay_preview_read_timeout`

HTTP 503 · Workflow replay preview read timed out

### `workflow_not_running`

HTTP 409 · Workflow run not running

### `workflow_performance_cohort_changed`

HTTP 409 · Workflow performance changed

### `workflow_resume_conflict`

HTTP 409 · Workflow resume conflict

### `workflow_resume_limit`

HTTP 409 · Workflow resume limit reached

### `workflow_resume_unsafe`

HTTP 409 · Workflow cannot be resumed

### `workflow_run_not_found`

HTTP 404 · Workflow run not found

### `workflow_step_not_found`

HTTP 404 · Workflow step not found

### `workflow_step_retry_not_allowed`

HTTP 409 · Workflow step cannot be retried

### `workflow_tenant_identity_unavailable`

HTTP 409 · Tenant identity required

### `workflow_wait_interrupted`

HTTP 499 · Workflow wait canceled

See [CLI guide](cli-setup.md).

### `workflow_wait_timeout`

HTTP 408 · Workflow wait timed out

See [CLI guide](cli-setup.md).

### `workload_not_production`

HTTP 403 · Production workload required

### `workload_selector_ambiguous`

HTTP 400
