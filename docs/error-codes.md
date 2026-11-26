# Error code reference

Every error Gregale returns carries a stable `code`. [Errors](errors.md) explains how an error is shaped and what to do about the most common ones. This page lists each code with the HTTP status Gregale sends it with, the title and hint Gregale writes for it, and the docs page it points to. A real response's `detail` adds the specifics, such as the observed value, the limit or the field that failed.

The list is generated from the code that builds each error, so a new code appears here when it ships. Codes for features whose docs are not published yet are left out. Each code's name is its anchor: link to `error-codes#plan_limit_apps`, for example.

If you are fixing a failure rather than looking up one code, start with [the common codes](errors.md#common-codes), which group the frequent ones by what went wrong, or [Troubleshooting](troubleshooting.md), which goes by symptom.

Jump to: [A](#a) · [B](#b) · [C](#c) · [D](#d) · [E](#e) · [F](#f) · [G](#g) · [H](#h) · [I](#i) · [J](#j) · [L](#l) · [M](#m) · [N](#n) · [O](#o) · [P](#p) · [Q](#q) · [R](#r) · [S](#s) · [T](#t) · [U](#u) · [V](#v) · [W](#w)

## A

### `account_abuse_hold`

HTTP 403 Forbidden · Account on hold

See [Networking](networking.md#outbound-connections).

### `account_deletion_confirm_required`

HTTP 409 Conflict

### `account_deletion_managed_postgres_databases`

HTTP 409 Conflict · Managed PostgreSQL databases must be deleted first

Run `gregale postgres list`, then `gregale postgres delete <database> --purge` for each database

See [Managed PostgreSQL](postgres.md).

### `account_deletion_pending`

HTTP 409 Conflict

### `account_deploy_blocked`

HTTP 409 Conflict · Deployment unavailable

### `account_egress_allowlist_extra_out_of_range`

HTTP 400 Bad Request · Account egress allowlist extra out of range

See [Networking](networking.md#outbound-connections).

### `account_exists`

HTTP 400 Bad Request

### `account_not_restorable`

HTTP 409 Conflict · Grace expired; Not restorable

### `admin_required`

HTTP 403 Forbidden · Admin access required

### `admission_refused`

HTTP 402 Payment Required · Spend cap reached

See [Billing](billing.md#spend-cap), [Cost controls](cost-controls.md).

### `alert_preset_disabled`

HTTP 400 Bad Request · Alert preset not yet available

See [Alerts](alerts.md#configure-an-alert).

### `alert_preset_invalid`

HTTP 400 Bad Request · Invalid alert preset

See [Alerts](alerts.md#configure-an-alert).

### `alert_preset_test_rate_limited`

HTTP 429 Too Many Requests · Alert preset test rate limited

See [Alerts](alerts.md).

### `alert_rule_invalid`

HTTP 400 Bad Request · Invalid alert rule

See [Alerts](alerts.md).

### `analytics_cohort_limit`

HTTP 409 Conflict · Cohort limit reached

### `api_key_expired`

HTTP 401 Unauthorized · API key expired; Deploy token expired

See [Authentication](auth.md#expiry-and-rotation).

### `api_key_limit_exceeded`

HTTP 409 Conflict · API key limit reached

See [Authentication](auth.md#api-keys).

### `api_key_revoked`

HTTP 401 Unauthorized · API key revoked; Deploy token revoked

See [Authentication](auth.md#expiry-and-rotation).

### `api_rate_limited`

HTTP 429 Too Many Requests · API rate limit reached

See [Errors](errors.md), [Troubleshooting](troubleshooting.md).

### `app_admission_unavailable`

HTTP 503 Service Unavailable · App admission unavailable

### `app_arch_mismatch`

HTTP 422 Unprocessable Entity · Unsupported CPU architecture

Your binary won't run on this control plane

### `app_concurrency_reached`

HTTP 429 Too Many Requests · App concurrency reached

See [Plans and pricing](plans.md#at-a-glance).

### `app_deleted`

HTTP 410 Gone · App deleted; App was deleted while wake was in flight

See [Alerts](alerts.md), [Crons](crons.md).

### `app_has_buckets`

HTTP 409 Conflict · Conflict

### `app_has_dependents`

HTTP 409 Conflict · Conflict

Remove the service and its references from your Compose file, then run a reviewed deploy (gregale deploy), or delete the dependent apps first.

See [Deploy a multi-service app](multi-service-apps.md).

### `app_has_postgres_bindings`

HTTP 409 Conflict · Conflict

See [Managed PostgreSQL](postgres.md).

### `app_health_unavailable`

HTTP 503 Service Unavailable · App health is unavailable

See [Errors](errors.md), [Troubleshooting](troubleshooting.md).

### `app_healthz_unauthorized`

HTTP 422 Unprocessable Entity · App_healthz_unauthorized; Health endpoint returning 401

Your /healthz is gated behind auth — we treat that as 'down'

See [Troubleshooting](troubleshooting.md).

### `app_layer_too_large`

HTTP 403 Forbidden · App too large

The built app is larger than your plan's app filesystem

See [Builds and framework detection](build.md#limits), [Container compatibility](container-compatibility.md), [Deploy a Next.js app](deploy-nextjs.md), [Troubleshooting](troubleshooting.md).

### `app_log_drain_invalid`

HTTP 400 Bad Request · Bad request; Invalid log drain

See [Logs](logs.md#log-drains).

### `app_logs_unavailable`

HTTP 503 Service Unavailable · App logs unavailable; Live logs temporarily unavailable

Retry `gregale logs APP` in a few seconds.

See [CLI guide](cli-setup.md), [Logs](logs.md), [Errors](errors.md).

### `app_loopback_bound`

HTTP 422 Unprocessable Entity · App bound to loopback; Application bound to loopback

Your app is listening on 127.0.0.1 — gateway traffic can't reach it

See [Errors](errors.md), [Troubleshooting](troubleshooting.md).

### `app_maintenance_mode`

HTTP 503 Service Unavailable · App in maintenance

### `app_manifest_invalid`

HTTP 409 Conflict or 422 Unprocessable Entity · Many operations send this code; the response's `title` and `detail` name the failure.

### `app_name_unavailable`

HTTP 409 Conflict · App name unavailable

### `app_not_listening`

HTTP 422 Unprocessable Entity · No process listening on $PORT; App listens on a different port

Your app isn't accepting traffic on the port we expect

See [Deployment overrides](deploy-overrides.md), [Errors](errors.md), [Deploy a web framework](frameworks.md), [Troubleshooting](troubleshooting.md).

### `app_not_purgeable`

HTTP 409 Conflict · App not purgeable

See [Apps](apps.md#purge-a-deleted-app-to-reuse-its-name).

### `app_not_restorable`

HTTP 409 Conflict · App not restorable

See [Apps](apps.md#retained-deleted-apps).

### `app_protocol_invalid`

HTTP 400 Bad Request · Invalid app_protocol; Invalid app protocol

### `app_rename_failed`

HTTP 409 Conflict · Slug taken

See [Apps](apps.md).

### `app_runtime_oom`

HTTP 422 Unprocessable Entity or 503 Service Unavailable · Container out of memory; App is out of memory; App ran out of memory during startup

Your app used more than its RAM limit and was killed

See [Errors](errors.md), [Troubleshooting](troubleshooting.md).

### `app_startup_crash`

HTTP 422 Unprocessable Entity · App exited during startup

Your app crashed before it became ready

See [Errors](errors.md), [Troubleshooting](troubleshooting.md).

### `app_startup_timeout`

HTTP 422 Unprocessable Entity · Many operations send this code; the response's `title` and `detail` name the failure.

### `app_static_egress_ip_invalid`

HTTP 400 Bad Request · Invalid static egress IP

See [Static egress IP](static-egress-ip.md).

### `app_unavailable`

HTTP 503 Service Unavailable · App temporarily unavailable

Retry the request in a few seconds.

See [Errors](errors.md), [Troubleshooting](troubleshooting.md).

### `app_visibility_invalid`

HTTP 400 Bad Request · Invalid app visibility

See [Networking](networking.md).

### `app_webhook_invalid`

HTTP 400 Bad Request · Bad request; Invalid webhook

### `approval_denied`

HTTP 403 Forbidden · Approval denied

### `approval_expired`

HTTP 410 Gone · Approval expired

### `approval_required`

HTTP 428 Precondition Required or 429 Too Many Requests · Approval required; Too many requests awaiting approval

### `auth_rate_limited`

HTTP 429 Too Many Requests · Too many codes; Too many failed authentication attempts; Too many MFA disable emails

See [Authentication](auth.md), [Errors](errors.md).

## B

### `bad_gateway`

HTTP 503 Service Unavailable · App connection failed; Edge rule compile error

Retry the request; if it still fails, check the app logs.

See [Quickstart](quickstart.md), [Errors](errors.md), [Troubleshooting](troubleshooting.md).

### `bad_request`

HTTP 400 Bad Request · Many operations send this code; the response's `title` and `detail` name the failure.

### `before_checkpoint_failed`

HTTP 422 Unprocessable Entity · Before checkpoint callback failed

Your before_checkpoint callback did not complete successfully

See [How scaling to zero works](cold-wake.md), [Functions and runtimes](functions.md).

### `billing_already_cancelled`

HTTP 409 Conflict · No active subscription

### `billing_cancel_failed`

HTTP 503 Service Unavailable · Billing cancel failed

### `billing_contact_member`

HTTP 409 Conflict · Member is the billing contact

See [Billing](billing.md#billing-email).

### `billing_email_change_failed`

HTTP 503 Service Unavailable · Billing email change failed

### `billing_email_in_use`

HTTP 409 Conflict · Billing email in use

See [Billing](billing.md#billing-email).

### `billing_past_due`

HTTP 402 Payment Required · Account suspended; Deploys blocked; Payment required

See [Billing](billing.md).

### `billing_plan_change_failed`

HTTP 503 Service Unavailable · Billing plan change failed

### `billing_provider_unavailable`

HTTP 503 Service Unavailable · Payment provider unavailable

### `billing_subscription_ending`

HTTP 409 Conflict · Subscription is scheduled to end

See [Billing](billing.md#cancel-and-resume).

### `billing_unavailable`

HTTP 501 Not Implemented · Billing provider not initialised; State store not initialised

### `binding_not_granted`

HTTP 403 Forbidden · Binding not granted

### `binding_verification_budget_full`

HTTP 429 Too Many Requests · Verification budget full

See [Networking](networking.md), [Binding verification runs](binding-verification-runs.md).

### `binding_verification_run_changed`

HTTP 409 Conflict · Verification run blocked

### `binding_verification_run_limit`

HTTP 429 Too Many Requests · Verification run limit reached

See [Networking](networking.md).

### `bindings_check_changed`

HTTP 409 Conflict · Bindings check changed; Release policy changed; Service graph changed

See [Errors](errors.md).

### `bindings_check_failed`

HTTP 409 Conflict · Bindings check failed

See [Errors](errors.md).

### `bindings_gate_unavailable`

HTTP 503 Service Unavailable · Bindings gate unavailable

See [Errors](errors.md).

### `bindings_release_policy_changed`

HTTP 409 Conflict · Release policy changed

### `bindings_release_required`

HTTP 409 Conflict · Binding release policy blocked traffic; Exact rollback required

Verify the exact deployment receiving traffic, then promote, advance its canary, or abort with both deployment_id and expected_predecessor_deployment_id. Recovery without exact selectors requires explicitly setting the release policy to off with its current revision and a reason.

### `build_failed`

Build failed

A step of your build exited with an error

See [Troubleshooting](troubleshooting.md).

### `build_infrastructure_failed`

Build could not run

A platform problem stopped the build; your code did not cause it

See [Troubleshooting](troubleshooting.md).

### `build_not_found`

HTTP 404 Not Found · No such build

See [Builds and framework detection](build.md).

### `build_oom`

HTTP 503 Service Unavailable

See [Troubleshooting](troubleshooting.md).

### `build_provenance_not_found`

HTTP 404 Not Found · Build provenance not found

See [Builds and framework detection](build.md).

### `build_sbom_unavailable`

HTTP 503 Service Unavailable · Build SBOM unavailable

See [Builds and framework detection](build.md).

### `build_timeout`

HTTP 503 Service Unavailable

See [Troubleshooting](troubleshooting.md).

### `build_undetected`

HTTP 400 Bad Request

See [Troubleshooting](troubleshooting.md).

## C

### `cache_shrink_needs_evict`

HTTP 409 Conflict · Shrinking needs --evict

### `cache_waking`

HTTP 503 Service Unavailable · Redis cache is waking

### `canary_step_conflict`

HTTP 409 Conflict · Canary step changed

See [Progressive rollouts](rollouts.md#how-a-rollout-proceeds).

### `capability_not_released`

HTTP 403 Forbidden · Feature not available

See [Plans and pricing](plans.md), [Edge rules](edge-rules.md).

### `capacity_unavailable`

HTTP status varies by operation · Many operations send this code; the response's `title` and `detail` name the failure.

### `challenge_required`

HTTP 403 Forbidden · Challenge required

See [Edge rules](edge-rules.md), [Security](security.md).

### `cli_auth_code_pending`

HTTP 404 Not Found · Awaiting approval

### `cli_auth_code_unavailable`

HTTP 410 Gone · Code unavailable

### `cold_wake_rate_limited`

HTTP 429 Too Many Requests · Too many app wakes

### `companion_preset_unavailable`

HTTP 503 Service Unavailable · Companion preset unavailable

See [Application companions](companions.md).

### `concurrency_queue_full`

HTTP 429 Too Many Requests · Concurrency queue full

See [Scaling policy](scaling-policy.md).

### `concurrency_queue_timeout`

HTTP 503 Service Unavailable · Concurrency queue wait expired

See [Scaling policy](scaling-policy.md).

### `concurrency_throttled`

HTTP 429 Too Many Requests · App busy; Concurrency limit reached

### `config_guard_not_found`

HTTP 404 Not Found · No config guard

See [Environment variables](env.md#config-guard).

### `config_revision_not_found`

HTTP 404 Not Found · Config revision not found

See [Environment variables](env.md#config-history).

### `conflict`

HTTP 409 Conflict or 410 Gone · Many operations send this code; the response's `title` and `detail` name the failure.

### `control_plane_busy`

HTTP 503 Service Unavailable · Control plane busy

### `control_plane_unavailable`

HTTP 503 Service Unavailable · Control plane unavailable

### `cors_origin_not_allowed`

HTTP 403 Forbidden · CORS origin not allowed

### `cors_preset_too_large`

HTTP 422 Unprocessable Entity · CORS preset too large

See [Plans and pricing](plans.md).

### `cron_fire_now_busy`

HTTP 429 Too Many Requests · Cron run already pending

See [Crons](crons.md#run-a-cron-now).

### `cron_invalid`

HTTP 400 Bad Request · Invalid cron path; Cron environment requires a project; Invalid cron schedule

See [Crons](crons.md).

### `csrf_mismatch`

HTTP 400 Bad Request or 403 Forbidden · CSRF Error; CSRF mismatch

### `csrf_missing`

HTTP 403 Forbidden · Missing CSRF state

### `custom_metric_limit`

HTTP 422 Unprocessable Entity · Custom metric limit reached

## D

### `data_upstreams_disabled`

HTTP 503 Service Unavailable · Data placement is disabled

See [Plans and pricing](plans.md).

### `database_cutover_fenced`

HTTP 409 Conflict · Database cutover in progress

### `database_switch_in_progress`

HTTP 409 Conflict · Database switch in progress

### `database_switch_not_cancellable`

HTTP 409 Conflict · Database switch cannot be cancelled

### `database_switch_unavailable`

HTTP 409 Conflict · Database switch unavailable

See [Safe releases](releases.md#rollbacks-that-restore-the-database).

### `database_switches_active`

HTTP 429 Too Many Requests · Too many database switches in progress

See [Safe releases](releases.md).

### `debug_regressions_unavailable`

HTTP 503 Service Unavailable · Debugger data unavailable

### `debug_replay_unsupported`

HTTP 409 Conflict · Debug replay is unavailable; Mirror replay is unavailable

### `dep_install_failed`

HTTP 422 Unprocessable Entity · Dependency installation failure

The build VM couldn't install your dependencies

See [Errors](errors.md), [Troubleshooting](troubleshooting.md).

### `deploy_failed`

HTTP 422 Unprocessable Entity or 503 Service Unavailable · Deploy failed; Deployment artifact unavailable

See [Deploys](deploys.md).

### `deploy_rate_limited`

HTTP 429 Too Many Requests · Deploy rate limited

See [Deploys](deploys.md#limits).

### `deploy_settings_conflict`

HTTP 409 Conflict · Deploy settings changed

### `deploy_signature_invalid`

HTTP 403 Forbidden · Signed-image enforcement rejected the deploy

See [Security](security.md#app-deploy-guard).

### `deployment_attempt_accepted`

HTTP 409 Conflict · Deployment attempt already accepted

### `deployment_attempt_pending`

The response's `title` and `detail` describe the failure.

### `deployment_cancel_live_forbidden`

HTTP 409 Conflict · Cannot cancel a live deployment

Use 'gregale release rollback &lt;id>' to swap to a previous deployment.

See [Deploys](deploys.md#cancel).

### `deployment_cancel_not_cancellable`

HTTP 409 Conflict · Deployment is not in a cancellable state

See [Deploys](deploys.md#cancel).

### `deployment_check_target_changed`

HTTP 409 Conflict · Deployment target unavailable

### `deployment_cron_conflict`

HTTP 409 Conflict · Deployment cron staging changed

### `deployment_hide_in_flight`

HTTP 409 Conflict · Cannot hide a deployment that is still in progress

Cancel the deployment first, then hide it.

See [Deploys](deploys.md#cancel).

### `deployment_not_live`

HTTP 409 Conflict · Deployment is not live

See [Progressive rollouts](rollouts.md).

### `deployment_promotion_attempt_accepted`

HTTP 409 Conflict · Promotion already accepted

### `deployment_promotion_attempt_conflict`

HTTP 409 Conflict · Promotion attempt conflict

### `deployment_queue_timeout`

The response's `title` and `detail` describe the failure.

See [Builds and framework detection](build.md), [Deploys](deploys.md).

### `deployment_reorder_not_pending`

HTTP 409 Conflict · Reorder only valid for pending deployments

Cancel this deployment and re-deploy with --priority if you need queue control.

See [Deploys](deploys.md).

### `deployment_reorder_priority_invalid`

HTTP 422 Unprocessable Entity · Priority must be in [0, 1000]

See [Deploys](deploys.md).

### `deployment_scope_collision`

HTTP 409 Conflict · Scope already live

### `deployment_smoke_failed`

HTTP 422 Unprocessable Entity

Your new release started but did not answer its first request correctly

See [Container compatibility](container-compatibility.md).

### `deployment_stalled`

The response's `title` and `detail` describe the failure.

See [Deploys](deploys.md).

### `deployment_verification_unavailable`

HTTP 503 Service Unavailable

Gregale could not start your new release to check it; your current release keeps serving

See [Container compatibility](container-compatibility.md), [Troubleshooting](troubleshooting.md).

### `dev_debug_not_developer_environment`

HTTP 409 Conflict · Debugging unavailable

### `dev_debug_session_limit`

HTTP 429 Too Many Requests · Too many debugger sessions

### `dev_postgres_database_url_secret`

HTTP 409 Conflict · A database variable is already set in this developer environment

See [`gregale dev`](gregale-dev.md).

### `dev_session_tearing_down`

HTTP 409 Conflict · Developer environment is being removed

See [`gregale dev`](gregale-dev.md).

### `dev_source_base_missing`

HTTP 409 Conflict · Developer source base unavailable

See [`gregale dev`](gregale-dev.md).

### `dev_source_diverged`

HTTP 409 Conflict · Instance not snapshotted

### `dev_watch_unsupported`

HTTP 422 Unprocessable Entity · Watch mode is for apps; Watch mode needs an authenticated environment; Watch mode needs more memory

See [`gregale dev`](gregale-dev.md).

### `developer_seed_failed`

The response's `title` and `detail` describe the failure.

See [`gregale dev`](gregale-dev.md).

### `developer_sync_history_unavailable`

HTTP 503 Service Unavailable · Developer sync history unavailable

### `domain_cert_not_issued`

HTTP 422 Unprocessable Entity · Domain cert not issued

See [Domains](domains.md#verification).

### `domain_not_verified`

HTTP 409 Conflict · Domain not verified

See [Domains](domains.md#verification).

### `domain_verification_failed`

HTTP 422 Unprocessable Entity · Domain verification failed

See [Domains](domains.md#verification).

### `dry_run_requires_post`

HTTP 405 Method Not Allowed · Dry-run is POST-only; use POST /v1/apps/{slug}/openapi/dry-run

## E

### `edge_feature_unavailable`

HTTP 422 Unprocessable Entity · Edge feature not released

See [Security](security.md).

### `edge_overloaded`

HTTP 503 Service Unavailable · Edge overloaded

### `edge_rule_conflict`

HTTP 409 Conflict · Edge rule conflict

### `edge_rule_list_exists`

HTTP 409 Conflict · Edge rule list exists

### `edge_rule_list_in_use`

HTTP 409 Conflict · Edge rule list in use

### `edge_rule_list_not_found`

HTTP 404 Not Found · Edge rule list not found

### `edge_rule_maintenance`

HTTP 503 Service Unavailable · Endpoint in maintenance

See [Edge rules](edge-rules.md).

### `edge_rule_not_found`

HTTP 404 Not Found · Edge rule not found

### `edge_rules_version_mismatch`

HTTP 412 Precondition Failed · Edge rules changed

### `egress_allowlist_too_long`

HTTP 400 Bad Request · Egress allowlist too long

See [Networking](networking.md#outbound-connections).

### `egress_ports_too_many`

HTTP 400 Bad Request · Too many egress ports

See [Networking](networking.md#outbound-connections).

### `email_missing`

HTTP 400 Bad Request · Missing Email

### `email_not_verified`

HTTP 401 Unauthorized · Email not verified

See [Authentication](auth.md#email-verification).

### `email_verification_required`

HTTP 403 Forbidden · Email verification required

See [Authentication](auth.md#email-verification).

### `empty_body`

HTTP 400 Bad Request · Request body is empty

### `env_batch_request_conflict`

HTTP 409 Conflict · Request ID already used

### `env_batch_revision_conflict`

HTTP 409 Conflict · Configuration changed

### `env_scope_invalid`

HTTP 400 Bad Request · Invalid env scope

See [Environment variables](env.md).

### `env_scope_reserved`

HTTP 400 Bad Request · Env scope reserved

See [Environment variables](env.md).

### `env_value_too_large`

HTTP 413 Request Entity Too Large · Invalid sidecar env; Env var value too large

See [Environment variables](env.md).

### `env_var_invalid_key`

HTTP 400 Bad Request · Invalid env var key

See [Environment variables](env.md).

### `env_var_missing`

HTTP 422 Unprocessable Entity · Missing environment variable

Your code references an env var we haven't been given

### `env_var_not_found`

HTTP 400 Bad Request · Env var not set

See [Environment variables](env.md).

### `environment_cleanup_pending`

HTTP 409 Conflict · Environment cleanup in progress

### `environment_field_ownership_conflict`

HTTP 409 Conflict · Field ownership conflict

### `environment_full_clone_unavailable`

HTTP 409 Conflict · Full environment clone unavailable

### `environment_promotion_attempt_accepted`

HTTP 409 Conflict · Environment promotion already accepted

### `environment_promotion_attempt_conflict`

HTTP 409 Conflict · Environment promotion attempt conflict

### `environment_queue_collection_unavailable`

HTTP 409 Conflict · Stage queue collection unavailable

### `environment_rollback_attempt_accepted`

HTTP 409 Conflict · Environment rollback already accepted

### `environment_rollback_attempt_conflict`

HTTP 409 Conflict · Environment rollback attempt conflict

### `environment_work_busy`

HTTP 409 Conflict · Environment work is running

### `environment_work_ownership_conflict`

HTTP 409 Conflict · Environment work ownership conflict

### `environment_work_policy_collection_unavailable`

HTTP 409 Conflict · Stage policy collection unavailable

### `event_envelope_too_large`

HTTP 413 Request Entity Too Large · Event envelope too large

See [Build stored workflows in Gregale](stored-workflow-authoring.md).

### `event_publish_batch_too_large`

HTTP 413 Request Entity Too Large · Batch too large

See [Errors](errors.md).

### `event_publish_not_attempted`

HTTP 503 Service Unavailable · Event not attempted

See [Errors](errors.md).

### `event_recovery_changed`

HTTP 409 Conflict · Recovery plan changed

See [Recover stored workflow runs](stored-workflow-recovery.md).

### `event_storage_capacity_exhausted`

HTTP 429 Too Many Requests · Event storage capacity exhausted

See [Errors](errors.md).

### `event_stream_unavailable`

The response's `title` and `detail` describe the failure.

See [Errors](errors.md).

### `exclude_only_overlap`

HTTP 409 Conflict · Workload is in both --only and --exclude

### `exclude_unknown_slug`

HTTP 400 Bad Request · Exclude slug is not a workload in this commit

### `execution_mode_conflict`

HTTP 422 Unprocessable Entity · Execution mode conflicts with the app's behavior

Your app did not behave like its execution mode

See [Errors](errors.md).

### `export_rate_limited`

HTTP 429 Too Many Requests · Export rate limited

See [Account and organization management](account.md#export-and-deletion).

## F

### `financial_budget_activation_unavailable`

HTTP 422 Unprocessable Entity · Budget activation unavailable; Organization budgets unavailable; Strict budgets not available for organizations

See [Billing](billing.md).

### `financial_budget_held`

HTTP 402 Payment Required · Compute budget held; Preview compute budget reached

### `financial_compute_budget_too_low`

HTTP 422 Unprocessable Entity · Compute budget below committed funding

### `financial_compute_not_ready`

HTTP 422 Unprocessable Entity · Strict compute activation unavailable; Job compute authority unavailable

### `financial_compute_unavailable`

HTTP 503 Service Unavailable · Compute admission unavailable

### `forged`

HTTP 403 Forbidden · GitHub installation could not be verified

### `function_init_failed`

HTTP 422 Unprocessable Entity · Function init failed

Your function's init hook failed before it became ready

See [Errors](errors.md), [Functions and runtimes](functions.md).

### `function_input_invalid`

The response's `title` and `detail` describe the failure.

See [Errors](errors.md), [Functions and runtimes](functions.md).

### `function_send_rate_limited`

HTTP 429 Too Many Requests · Function send rate limited

See [Event-driven workloads](event-driven.md#async-backlog).

## G

### `gateway_overloaded`

HTTP 503 Service Unavailable · Gateway overloaded

### `geo_denied`

HTTP 403 Forbidden · Country not allowed; Country denied

See [Edge rules](edge-rules.md), [Errors](errors.md), [Troubleshooting](troubleshooting.md).

### `github_account_conflict`

HTTP 409 Conflict · GitHub deployment connection could not be saved

See [Deploys](deploys.md).

### `github_app_not_installed`

HTTP 404 Not Found · Gregale GitHub App not installed

Install the Gregale GitHub App on your account or organization, then connect again.

See [Deploys](deploys.md).

### `github_install_ambiguous`

HTTP 409 Conflict · GitHub installation is ambiguous

### `github_install_not_found`

HTTP 404 Not Found or 409 Conflict · GitHub connection not found; GitHub installation is no longer available; No GitHub installation

See [Deploy from a GitHub source ref](source-ref.md#prerequisites).

### `github_install_not_owned`

HTTP 403 Forbidden · GitHub installation is not connected to this account

### `github_installation_choice_required`

HTTP 409 Conflict · Choose a GitHub installation

See [Deploys](deploys.md).

### `github_installation_not_accessible`

HTTP 403 Forbidden · GitHub installation not accessible

Sign in to GitHub as a user with access to that installation, then connect again.

See [Deploys](deploys.md).

### `github_installation_owner_required`

HTTP 403 Forbidden · Only the owner can connect this installation

Sign in to GitHub as that user, or install the Gregale GitHub App on your own account.

See [Deploys](deploys.md).

### `github_installation_required`

HTTP 409 Conflict · Choose a GitHub installation

See [Deploys](deploys.md).

### `github_integration_unavailable`

HTTP 503 Service Unavailable · GitHub integration unavailable

### `github_not_bound`

HTTP 409 Conflict · GitHub is not connected to this app

### `github_org_admin_required`

HTTP 403 Forbidden · Organization admin required

Ask an organization admin to connect GitHub, or to make you an admin.

See [Deploys](deploys.md).

### `github_permission_pending`

HTTP 403 Forbidden · GitHub permission not accepted

Ask an organization owner to review the pending permission request in the organization's GitHub App settings.

See [Deploys](deploys.md).

### `github_repo_not_accessible`

HTTP 403 Forbidden · Repository is not accessible

See [Deploys](deploys.md).

### `github_unreachable`

HTTP 503 Service Unavailable · GitHub Unreachable; Could not reach GitHub

Gregale could not complete the GitHub connection. Try again in a minute; if it continues, contact support.

See [Deploys](deploys.md).

### `gitlab_company_project_unavailable`

HTTP 409 Conflict · GitLab unavailable for company projects

### `gitlab_connection_required`

HTTP 409 Conflict · GitLab connection required

### `google_id_token_invalid`

HTTP 400 Bad Request · OAuth Failed

### `google_id_token_missing`

HTTP 400 Bad Request · OAuth Failed

### `google_identity_mismatch`

HTTP 400 Bad Request · OAuth Failed

### `google_unreachable`

HTTP 503 Service Unavailable · Google Unreachable

## H

### `handler_missing`

HTTP 400 Bad Request · Handler required

See [Functions and runtimes](functions.md).

### `header_mutation_forbidden`

HTTP 422 Unprocessable Entity · Header mutation forbidden

### `http_admission_unavailable`

HTTP 503 Service Unavailable · Request admission unavailable

### `http_error`

The response's `title` and `detail` describe the failure.

## I

### `idempotency_conflict`

HTTP 409 Conflict · Idempotency key conflict

### `idempotency_key_reused`

HTTP 422 Unprocessable Entity · Idempotency key reused; Idempotency-Key reused

See [Errors](errors.md).

### `image_abuse_detected`

The response's `title` and `detail` describe the failure.

See [Security](security.md).

### `image_egress_denied`

HTTP 403 Forbidden · Egress denied

### `image_manifest_invalid`

HTTP 422 Unprocessable Entity

See [Container compatibility](container-compatibility.md).

### `image_not_found`

HTTP 422 Unprocessable Entity

The container image you asked for could not be pulled

### `image_required`

HTTP 400 Bad Request · Image digest required

### `image_secret_detected`

The response's `title` and `detail` describe the failure.

### `inbound_webhook_backlog_full`

HTTP 429 Too Many Requests · Inbound webhook backlog full

See [Event-driven workloads](event-driven.md#inbound-webhook-backlog).

### `inbound_webhook_bad_signature`

HTTP 400 Bad Request · Invalid webhook signature

### `inbound_webhook_invalid`

HTTP 400 Bad Request · Invalid inbound webhook

### `inbound_webhook_rate_limited`

HTTP 429 Too Many Requests · Inbound webhook rate limited

See [Event-driven workloads](event-driven.md#inbound-webhook-rate-limit).

### `inbound_webhook_routing_conflict`

HTTP 409 Conflict · Endpoint already routed

### `inbound_webhook_too_large`

HTTP 413 Request Entity Too Large · Webhook body too large

### `init_sidecar_failed`

The response's `title` and `detail` describe the failure.

### `insights_query_timeout`

HTTP 503 Service Unavailable · Analytics query timed out

### `instance_busy`

HTTP 409 Conflict or 503 Service Unavailable · Function still running; Instance busy

### `insufficient_scope`

HTTP 403 Forbidden · Many operations send this code; the response's `title` and `detail` name the failure.

### `internal`

HTTP 500 Internal Server Error · List failed; Delete failed; Lookup failed

See [Application companions](companions.md), [Networking](networking.md).

### `internal_error`

HTTP 500 Internal Server Error or 503 Service Unavailable · Many operations send this code; the response's `title` and `detail` name the failure.

### `invalid_autoscale_target_cpu_pct`

HTTP 422 Unprocessable Entity · Invalid autoscale target CPU%

### `invalid_autoscale_target_rps`

HTTP 422 Unprocessable Entity · Invalid autoscale target RPS

### `invalid_body`

HTTP 400 Bad Request · Invalid JSON body

### `invalid_canary_preset`

HTTP 422 Unprocessable Entity · Invalid canary preset; Invalid canary rollout

See [Progressive rollouts](rollouts.md#canary-ladders).

### `invalid_cooldown`

HTTP 422 Unprocessable Entity · Invalid cooldown

See [Scaling policy](scaling-policy.md).

### `invalid_cpu_millicores`

HTTP 422 Unprocessable Entity · Invalid CPU

See [Plans and pricing](plans.md#what-each-limit-means).

### `invalid_cpu_ram_pair`

HTTP 422 Unprocessable Entity · Invalid CPU/RAM pair

See [Plans and pricing](plans.md#what-each-limit-means).

### `invalid_credentials`

HTTP 401 Unauthorized · Sign in failed

See [Account and organization management](account.md#profile-sign-in-and-sessions).

### `invalid_cursor`

HTTP 400 Bad Request · Invalid cursor

### `invalid_egress_allowlist`

HTTP 400 Bad Request · Invalid egress allowlist entry

See [Networking](networking.md#outbound-connections).

### `invalid_egress_port`

HTTP 400 Bad Request · Invalid egress port

See [Networking](networking.md#outbound-connections).

### `invalid_max_instances`

HTTP 422 Unprocessable Entity · Invalid max_instances

See [Scaling policy](scaling-policy.md#manifest-form).

### `invalid_min_instances`

HTTP 422 Unprocessable Entity · Invalid min_instances; Min_instances must be >= 0

See [How scaling to zero works](cold-wake.md#opting-out-keep-n-instances-warm-pro-scale).

### `invalid_mirror_percent`

HTTP 422 Unprocessable Entity · Invalid mirror percent

See [Progressive rollouts](rollouts.md#traffic-mirroring).

### `invalid_mirror_window`

HTTP 422 Unprocessable Entity · Invalid mirror window

See [Progressive rollouts](rollouts.md#traffic-mirroring).

### `invalid_nonce`

HTTP 400 Bad Request · Invalid OAuth Request

### `invalid_openapi_shape`

HTTP 400 Bad Request · Doc must be a JSON object with a top-level 'openapi' or 'swagger' key

### `invalid_pkce`

HTTP 400 Bad Request · Invalid OAuth Request

### `invalid_public_auth_ip_allowlist`

HTTP 400 Bad Request · Invalid public-auth IP allowlist entry

See [Authentication](auth.md#app-access-control).

### `invalid_ram_mb`

HTTP 422 Unprocessable Entity · Invalid RAM

See [Plans and pricing](plans.md#what-each-limit-means).

### `invalid_recover_action`

HTTP 422 Unprocessable Entity · Invalid recover_rollout action

See [Progressive rollouts](rollouts.md#why-a-rollout-advanced-waits-or-stopped).

### `invalid_ref`

HTTP 400 Bad Request · Invalid ref

See [Deploy from a GitHub source ref](source-ref.md).

### `invalid_registry_host`

HTTP 400 Bad Request · Invalid registry host

See [Registry credentials](registry-credentials.md).

### `invalid_request`

HTTP 400 Bad Request · Many operations send this code; the response's `title` and `detail` name the failure.

### `invalid_resource_profile`

HTTP 422 Unprocessable Entity · Conflicting resource profile; Invalid resource profile

### `invalid_scheduled_at`

HTTP 400 Bad Request · Invalid scheduled_at

See [Event-driven workloads](event-driven.md#delayed-tasks).

### `invalid_source`

HTTP 400 Bad Request · Source must be one of: cold_boot, manual_upload; Source must be one of: manual_import, auto

### `invalid_state`

HTTP 400 Bad Request · Invalid State

### `invalid_suspend_timeout`

HTTP 422 Unprocessable Entity · Invalid suspend timeout

See [Managed PostgreSQL](postgres.md#plans).

### `invalid_traffic_percent`

HTTP 422 Unprocessable Entity · Invalid traffic_percent

See [Progressive rollouts](rollouts.md).

### `invalid_wake_with_app`

HTTP 422 Unprocessable Entity · Invalid wake-with-app setting

See [Managed PostgreSQL](postgres.md#plans).

### `invalid_warm_pool_size`

HTTP 422 Unprocessable Entity · Invalid warm_pool_size; Invalid max_concurrency for warm_pool_size

### `invalid_warm_snapshot_min_ms`

HTTP 422 Unprocessable Entity · Invalid warm_snapshot_min_ms

See [How scaling to zero works](cold-wake.md#warm-snapshots-opt-in-pro-and-scale).

### `invalid_warm_snapshot_min_requests`

HTTP 422 Unprocessable Entity · Invalid warm_snapshot_min_requests

See [How scaling to zero works](cold-wake.md#warm-snapshots-opt-in-pro-and-scale).

### `invocation_environment_work_isolation_unavailable`

HTTP 409 Conflict · Stage work unavailable

### `invocation_not_found`

HTTP 404 Not Found · Invocation not found

See [Event-driven workloads](event-driven.md#invocation-history-retention).

### `invocation_not_replayable`

HTTP 409 Conflict · Invocation not replayable; Invocation is not in a replayable state

See [Event-driven workloads](event-driven.md#invocation-history-retention).

### `invocation_replay_unavailable`

HTTP 409 Conflict · Invocation replay unavailable

### `invocation_version_conflict`

HTTP 409 Conflict · Invocation version conflict

### `invocation_version_unavailable`

HTTP 410 Gone · Invocation version unavailable

### `invocation_wait_interrupted`

Invocation wait interrupted

See [CLI guide](cli-setup.md).

### `invocation_wait_timeout`

HTTP 408 Request Timeout · Invocation wait timed out

See [CLI guide](cli-setup.md).

### `ip_denied`

HTTP 403 Forbidden · IP address not allowed; IP denied; IP not allowed

See [Edge rules](edge-rules.md), [Errors](errors.md), [Troubleshooting](troubleshooting.md).

## J

### `job_command_invalid`

HTTP 422 Unprocessable Entity · Job command invalid

See [Jobs](jobs.md).

### `job_deadline_exceeded`

HTTP 410 Gone · Job run deadline exceeded

See [Jobs](jobs.md).

### `job_has_live_instances`

HTTP 409 Conflict · Job has active work; Job has live instances

See [Jobs](jobs.md).

### `job_image_missing`

HTTP 422 Unprocessable Entity · Job image not found

See [Jobs](jobs.md).

### `job_quota_exceeded`

HTTP 403 Forbidden · Job limit reached

See [Plans and pricing](plans.md).

### `job_registry_credential_not_found`

HTTP 400 Bad Request · Job registry credential not set

See [Registry credentials](registry-credentials.md).

### `job_run_cancelled`

HTTP 409 Conflict · Job run is already terminal

See [Jobs](jobs.md).

### `job_task_max_retries_reached`

HTTP 409 Conflict · Job task retry budget exhausted

See [Jobs](jobs.md).

### `job_task_not_found`

HTTP 404 Not Found · Job task not found; Task not found

See [Jobs](jobs.md).

### `job_task_not_retriable`

HTTP 409 Conflict · Job task cannot be retried

See [Jobs](jobs.md).

### `jobs_not_allowed`

HTTP 402 Payment Required · Jobs unavailable on this plan

See [Plans and pricing](plans.md).

### `json_error`

HTTP 500 Internal Server Error · Internal Error

### `jwt_audience_mismatch`

HTTP 401 Unauthorized · JWT audience mismatch

### `jwt_missing_issuer`

HTTP 401 Unauthorized · JWT issuer mismatch

### `jwt_missing_token`

HTTP 401 Unauthorized · JWT bearer token required

### `jwt_signature_invalid`

HTTP 401 Unauthorized · JWT signature invalid

## L

### `log_archive_degraded`

HTTP 503 Service Unavailable · Archived logs unavailable

See [CLI guide](cli-setup.md).

### `log_archive_invalid_query`

HTTP 400 Bad Request · Invalid archived log request

Choose an instance and a date formatted as YYYY-MM-DD, then retry.

See [Logs](logs.md), [Errors](errors.md).

### `log_archive_missing`

HTTP 404 Not Found · Archived logs not found

See [CLI guide](cli-setup.md).

### `log_archive_retention_exceeded`

HTTP 403 Forbidden · Archived log date is outside the retention window

Choose a more recent date, or review plans with longer log retention.

See [Logs](logs.md#retention), [Errors](errors.md).

### `log_archive_unconfigured`

HTTP 503 Service Unavailable · Archived logs temporarily unavailable

Retry shortly, or use `gregale logs APP` for live logs.

See [Logs](logs.md), [Errors](errors.md).

### `log_reads_busy`

HTTP 429 Too Many Requests · Too many log reads

### `log_streams_limit`

HTTP 429 Too Many Requests · Too many log streams

See [Logs](logs.md#limits).

### `long_poll_timeout`

HTTP 503 Service Unavailable · Long-poll wait budget ran out

See [Event-driven workloads](event-driven.md).

## M

### `managed_postgres_ambiguous`

HTTP 409 Conflict · Managed PostgreSQL database reference is ambiguous

### `managed_postgres_binding_changed`

HTTP 409 Conflict · Gregale.yaml changes an existing binding

Bind the new database or access under another env key and switch the app to it, or delete the old binding (gregale postgres bindings) and deploy again.

See [Managed PostgreSQL](postgres.md).

### `managed_postgres_binding_limit`

HTTP 403 Forbidden · Managed PostgreSQL binding limit reached

Delete a binding the app no longer uses (gregale postgres bindings delete) or upgrade the plan.

See [Plans and pricing](plans.md).

### `managed_postgres_binding_not_ready`

HTTP 409 Conflict · Managed PostgreSQL binding is not ready

### `managed_postgres_binding_target_missing`

HTTP 409 Conflict · Database binding target missing

### `managed_postgres_forbidden`

HTTP 403 Forbidden · Cannot create managed PostgreSQL database

### `managed_postgres_not_found`

HTTP 404 Not Found · Managed PostgreSQL database not found; Managed PostgreSQL resource not found

### `managed_postgres_not_in_plan`

HTTP 403 Forbidden · Managed PostgreSQL is not included in this plan

### `managed_postgres_not_ready`

HTTP 409 Conflict · Managed PostgreSQL database is not ready

### `managed_postgres_plan_unknown`

HTTP 422 Unprocessable Entity · Managed PostgreSQL plan unknown

### `managed_postgres_quota_exceeded`

HTTP 403 Forbidden · Managed PostgreSQL quota exceeded

Delete a database you no longer need or upgrade the plan, then try again; or restore the database with gregale postgres restore and attach the copy yourself.

See [Plans and pricing](plans.md), [Managed PostgreSQL](postgres.md#usage-and-limits), [Safe releases](releases.md).

### `managed_postgres_unavailable`

HTTP 503 Service Unavailable · Managed PostgreSQL unavailable

See [Managed PostgreSQL](postgres.md).

### `managed_redis_binding_target_missing`

HTTP 409 Conflict · Cache binding target missing

### `managed_redis_cache_missing`

HTTP 409 Conflict · Cache missing

### `managed_redis_limit`

HTTP 403 Forbidden · Managed Redis cache limit reached

### `managed_redis_not_in_plan`

HTTP 403 Forbidden · Managed Redis is not included in this plan

### `max_min_instances_exceeded`

HTTP 422 Unprocessable Entity · Min_instances exceeds plan cap

See [How scaling to zero works](cold-wake.md#opting-out-keep-n-instances-warm-pro-scale).

### `method_not_allowed`

HTTP 405 Method Not Allowed · MCP instance probe failed; Method not allowed; Routes read failed

### `mfa_disable_cooldown`

HTTP 425 Too Early · MFA disable cooldown active

### `mfa_invalid_code`

HTTP 401 Unauthorized · Invalid code; Invalid credentials; Invalid token

### `mfa_last_recovery_code`

HTTP 409 Conflict · Last recovery code

### `mfa_required`

HTTP 403 Forbidden or 409 Conflict · MFA required; MFA setup incomplete; Session required

### `migration_recovery_blocked`

HTTP 409 Conflict · Migration recovery blocked

### `mirror_cross_app_mismatch`

HTTP 422 Unprocessable Entity · Source and mirror deployments must belong to the same app

See [Progressive rollouts](rollouts.md#traffic-mirroring).

### `mirror_deployment_not_live`

HTTP 409 Conflict · Referenced deployment is not live

See [Progressive rollouts](rollouts.md#traffic-mirroring).

### `mirror_rule_not_found`

HTTP 404 Not Found · Mirror rule not found

See [Progressive rollouts](rollouts.md#traffic-mirroring).

### `mirror_rule_quota_exceeded`

HTTP 422 Unprocessable Entity · Mirror rule quota exceeded

See [Progressive rollouts](rollouts.md#traffic-mirroring).

### `mirror_slot_at_capacity`

HTTP 503 Service Unavailable

### `mirror_source_target_same`

HTTP 422 Unprocessable Entity · Source and mirror deployments must differ

See [Progressive rollouts](rollouts.md#traffic-mirroring).

### `misdirected_request`

HTTP 421 Misdirected Request · Misdirected request

### `missing_code`

HTTP 400 Bad Request · Authorization Error

### `missing_doc`

HTTP 400 Bad Request · Doc field is empty; Set_doc=true with a non-empty doc is required

## N

### `no_live_deployment`

HTTP 409 Conflict · App has no live deployment; No live deployment

See [CLI guide](cli-setup.md).

### `no_rollback_target`

HTTP 409 Conflict · A release is still rolling out; Choose a rollback target; More than one environment serves this app

See [Deploys](deploys.md#revisions).

### `not_found`

HTTP 404 Not Found or 410 Gone · Many operations send this code; the response's `title` and `detail` name the failure.

### `notification_channel_in_use`

HTTP 409 Conflict · Channel has open incidents; Notification channel in use

See [Alerts](alerts.md#notification-channels).

### `notification_channel_invalid`

HTTP 400 Bad Request or 409 Conflict · Channel name already exists; Invalid notification channel

See [Alerts](alerts.md#notification-channels).

### `notification_channel_limit_reached`

HTTP 422 Unprocessable Entity · Notification channel limit reached

See [Alerts](alerts.md#notification-channels).

### `notification_channel_test_rate_limited`

HTTP 429 Too Many Requests · Notification channel test rate limited

See [Alerts](alerts.md#notification-channels).

## O

### `oauth_exchange_failed`

HTTP 400 Bad Request or 503 Service Unavailable · OAuth Failed

### `oauth_provider_unavailable`

HTTP 503 Service Unavailable · GitHub connection unavailable; OAuth Provider Unavailable

Try again later, or contact support if you need to connect GitHub.

Use another sign-in method, or contact support if you need this provider.

See [Authentication](auth.md), [Errors](errors.md).

### `obs_runtime_busy`

HTTP 429 Too Many Requests · Runtime collection busy

See [Errors](errors.md).

### `openapi_docs_not_allowed`

HTTP 402 Payment Required · Endpoint discovery unavailable on this plan; Stored route checks unavailable

See [Plans and pricing](plans.md).

### `openapi_import_invalid`

HTTP 422 Unprocessable Entity or 500 Internal Server Error · Persisted OpenAPI document could not be analyzed; Failed to walk imported doc for dry-run; Imported OpenAPI doc failed validation

### `openapi_import_quota_reached`

HTTP 403 Forbidden · Per-account OpenAPI import quota reached

### `openapi_import_too_large`

HTTP 413 Request Entity Too Large · Imported doc exceeds size cap

### `openapi_import_too_many_endpoints`

HTTP 422 Unprocessable Entity · Imported doc declares too many endpoints

### `openapi_policy_confirmation_required`

HTTP 400 Bad Request · OpenAPI policy confirmation is required

### `openapi_policy_stale`

HTTP 409 Conflict · OpenAPI policy plan is stale

### `org_already_member`

HTTP 409 Conflict · Already a member of this organization

See [Account and organization management](account.md#invitations).

### `org_api_key_requires_org`

HTTP 409 Conflict · Organization key required; API key must be bound to an organization; Organization project path required

See [Account and organization management](account.md#organization-api-keys).

### `org_free_limit_exceeded`

HTTP 403 Forbidden · Free organization limit reached

See [Account and organization management](account.md#member-limits).

### `org_invitation_cap_exceeded`

HTTP 403 Forbidden · Organization invitation limit reached

See [Account and organization management](account.md#member-limits).

### `org_invitation_expired`

HTTP 410 Gone · Invitation expired

See [Account and organization management](account.md#invitations).

### `org_invitation_invalid`

HTTP 410 Gone · Invitation invalid

See [Account and organization management](account.md#invitations).

### `org_last_owner`

HTTP 409 Conflict · Cannot remove the last owner; Owner cannot leave

See [Account and organization management](account.md#changing-roles-removing-members-and-ownership).

### `org_member_cap_exceeded`

HTTP 403 Forbidden · Organization member limit reached

See [Account and organization management](account.md#member-limits).

### `org_mfa_required`

HTTP 403 Forbidden or 409 Conflict · Turn on two-factor authentication first; Two-factor authentication required

See [Account and organization management](account.md#require-two-factor-authentication).

### `org_not_found`

HTTP 404 Not Found · Organization not found

See [Account and organization management](account.md).

### `org_personal_immutable`

HTTP 409 Conflict · Personal organizations are immutable

See [Account and organization management](account.md#members-roles-and-invitations).

### `org_role_forbidden`

HTTP 403 Forbidden · Application standards mutation forbidden; Approver not allowed; Insufficient role for this action

Ask a teammate the policy lets approve to approve this promotion, then promote with their approval.

See [Account and organization management](account.md#roles), [Release approvals, freezes and schedules](release-controls.md#release-approvals).

### `org_slug_invalid`

HTTP 422 Unprocessable Entity · Invalid org plan; Invalid organization slug

See [Account and organization management](account.md).

### `org_slug_taken`

HTTP 409 Conflict · Organization slug in use

See [Account and organization management](account.md).

### `outbound_account_suspended`

The account is suspended

### `outbound_admission_unavailable`

Outbound admission is temporarily unavailable

### `outbound_app_not_attached`

The app is not attached to this outbound integration

See [Outbound integrations](outbound-integrations.md).

### `outbound_binding_unavailable`

The response's `title` and `detail` describe the failure.

### `outbound_budget_exhausted`

Outbound integration budget is exhausted

### `outbound_budget_unavailable`

The response's `title` and `detail` describe the failure.

### `outbound_circuit_breaker_open`

Outbound provider is temporarily unavailable

### `outbound_circuit_breaker_unavailable`

Outbound circuit breaker is unavailable

### `outbound_credential_key_limit`

The response's `title` and `detail` describe the failure.

### `outbound_credential_seal_unavailable`

The response's `title` and `detail` describe the failure.

### `outbound_credential_unavailable`

The response's `title` and `detail` describe the failure.

### `outbound_execution_authorization_unavailable`

Runs outbound authorization is unavailable

### `outbound_execution_not_authorized`

This Run is not authorized for the outbound integration; Runs access is unavailable for this outbound integration

### `outbound_identity_unavailable`

Outbound workload identity is unavailable; Runs workload identity is unavailable

### `outbound_integration_disabled`

Outbound integration is disabled

See [Outbound integrations](outbound-integrations.md).

### `outbound_integration_limit`

The response's `title` and `detail` describe the failure.

See [Outbound integrations](outbound-integrations.md).

### `outbound_integration_not_found`

Outbound integration not found

See [Outbound integrations](outbound-integrations.md).

### `outbound_integration_unavailable`

Outbound integration is unavailable

### `outbound_internal_not_authorized`

The gateway's own call cannot use this outbound integration

### `outbound_method_not_allowed`

AI prompts take POST requests; CONNECT is not supported; Feedback takes POST

### `outbound_policy_unavailable`

The response's `title` and `detail` describe the failure.

### `outbound_probe_unavailable`

The response's `title` and `detail` describe the failure.

See [Errors](errors.md).

### `outbound_provider_cooldown`

Outbound provider requested a cooldown

### `outbound_provider_cooldown_unavailable`

Outbound provider cooldown state is unavailable

### `outbound_provider_credential_unavailable`

Outbound provider credential is unavailable

See [Outbound integrations](outbound-integrations.md).

### `outbound_qualification_not_authorized`

The qualification probe cannot use this outbound integration

### `outbound_relay_not_supported`

This outbound integration is not available through the app relay

### `outbound_replay_not_authorized`

The replay cannot use this outbound integration

### `outbound_request_invalid`

Outbound request could not be constructed

### `outbound_request_too_large`

Outbound request body exceeds the gateway limit; Workflow outbound body exceeds the limit

### `outbound_request_unreadable`

Outbound request body could not be read

### `outbound_response_headers_too_large`

Outbound provider response headers exceed the gateway limit

### `outbound_response_sensitive`

Workflow outbound response contains sensitive authorization data

### `outbound_response_too_large`

Outbound provider response exceeds the gateway limit; Workflow outbound response is incomplete or exceeds the limit

### `outbound_retry_budget_exhausted`

Outbound retry budget is exhausted

### `outbound_route_not_allowed`

Outbound method or path is not allowed

See [Outbound integrations](outbound-integrations.md).

### `outbound_route_not_found`

AI route not found

### `outbound_route_requires_relay`

AI prompts are available only through GREGALE_OUTBOUND_URL or an access token; AI routes are available only through GREGALE_OUTBOUND_URL or an access token

### `outbound_route_unavailable`

AI route is temporarily unavailable

### `outbound_runs_binding_ineligible`

The response's `title` and `detail` describe the failure.

See [Outbound integrations](outbound-integrations.md).

### `outbound_stage_not_allowed`

Outbound integrations serve production deployments; a stage calls the provider with its own credentials

### `outbound_target_invalid`

Outbound integration target is invalid

### `outbound_unauthorized`

Outbound identity is ambiguous; Outbound token is invalid; Outbound workload identity is invalid

### `outbound_upstream_unavailable`

Outbound provider could not be reached

### `outbound_usage_unavailable`

The response's `title` and `detail` describe the failure.

### `outbound_workflow_not_authorized`

Workflow access is unavailable for this integration

### `outbound_workflow_unavailable`

Workflow outbound authorization is unavailable

### `overage_cap_above_max`

HTTP 422 Unprocessable Entity · Spend cap above plan maximum

See [Billing](billing.md#spend-cap).

### `overage_cap_required`

HTTP 422 Unprocessable Entity · Spend cap required

See [Billing](billing.md#spend-cap).

## P

### `password_too_weak`

HTTP 400 Bad Request · Password too weak

See [Account and organization management](account.md#profile-sign-in-and-sessions).

### `payment_authentication_required`

HTTP 402 Payment Required · Bank confirmation required

See [Billing](billing.md#upgrades).

### `payment_required`

HTTP 402 Payment Required · Billing confirmation required; Upgrade not charged; Billing subscription required

### `penalty_box`

HTTP 403 Forbidden · Temporarily blocked

See [Edge rules](edge-rules.md), [Security](security.md).

### `plan_alert_presets_not_allowed`

HTTP 402 Payment Required · Alert preset not available on this plan

See [Plans and pricing](plans.md).

### `plan_alert_rule_quota`

HTTP 403 Forbidden · Alert rule limit reached

See [Plans and pricing](plans.md).

### `plan_alert_rules_not_allowed`

HTTP 402 Payment Required · Alert rules unavailable on this plan

See [Plans and pricing](plans.md).

### `plan_app_errors_not_allowed`

HTTP 402 Payment Required · App error grouping unavailable on this plan

See [Plans and pricing](plans.md).

### `plan_app_protocol_grpc_not_allowed`

HTTP 403 Forbidden · Per-app gRPC wire protocol is not allowed on this plan

### `plan_app_usage_summary_not_allowed`

HTTP 402 Payment Required · App usage summary unavailable on this plan

See [Plans and pricing](plans.md).

### `plan_async_backlog`

HTTP 429 Too Many Requests · Per-app async backlog full

See [Event-driven workloads](event-driven.md#async-backlog).

### `plan_autoscale_not_allowed`

HTTP 403 Forbidden · Autoscale target CPU% is not allowed on this plan; Autoscale target RPS is not allowed on this plan

See [Plans and pricing](plans.md).

### `plan_cors_preset_quota_reached`

HTTP 403 Forbidden · CORS preset limit reached

See [Plans and pricing](plans.md).

### `plan_cors_presets_not_allowed`

HTTP 402 Payment Required · CORS presets unavailable on this plan

See [Plans and pricing](plans.md).

### `plan_cron_quota`

HTTP 403 Forbidden · Cron limit reached; Cron over plan limit

See [Crons](crons.md).

### `plan_crons_not_allowed`

HTTP 402 Payment Required · Crons unavailable on this plan

See [Crons](crons.md).

### `plan_custom_metrics_not_allowed`

HTTP 402 Payment Required · Custom metrics not available on this plan

See [Plans and pricing](plans.md).

### `plan_delayed_tasks_cap`

HTTP 403 Forbidden · Per-app delayed-task cap exceeded

See [Event-driven workloads](event-driven.md#delayed-tasks).

### `plan_edge_rule_kind_not_allowed`

HTTP 402 Payment Required · Edge rule kind unavailable on this plan

See [Edge rules](edge-rules.md#limits).

### `plan_edge_rule_kind_quota_reached`

HTTP 403 Forbidden · Edge rule kind quota reached

See [Edge rules](edge-rules.md#limits).

### `plan_egress_allowlist_not_allowed`

HTTP 403 Forbidden · Plan doesn't allow an egress allowlist

See [Networking](networking.md#outbound-connections).

### `plan_egress_overage_not_allowed`

HTTP 402 Payment Required · Plan cannot elect egress overage

See [Billing](billing.md).

### `plan_egress_ports_not_allowed`

HTTP 403 Forbidden · Plan doesn't allow extra egress ports

See [Networking](networking.md#outbound-connections).

### `plan_empty`

HTTP 422 Unprocessable Entity · Project plan is empty

### `plan_eviction_priority_reserved_not_allowed`

HTTP 402 Payment Required · Reserved eviction priority is not available on this plan

See [Plans and pricing](plans.md).

### `plan_eviction_priority_reserved_quota`

HTTP 422 Unprocessable Entity · Reserved eviction priority quota reached

See [Plans and pricing](plans.md).

### `plan_feature_gated`

HTTP 402 Payment Required · Many operations send this code; the response's `title` and `detail` name the failure.

### `plan_feature_unavailable`

HTTP 403 Forbidden · Bucket triggers unavailable

### `plan_gate_blocked`

HTTP 403 Forbidden · Plan gate blocked

### `plan_health_path_wakes_not_allowed`

HTTP 403 Forbidden · Health-path wakes are not allowed on this plan

See [Plans and pricing](plans.md).

### `plan_inbound_webhook_quota`

HTTP 403 Forbidden · Inbound webhook endpoint limit reached

See [Event-driven workloads](event-driven.md#inbound-webhooks).

### `plan_inbound_webhooks_not_allowed`

HTTP 402 Payment Required · Inbound webhooks unavailable on this plan

See [Event-driven workloads](event-driven.md#inbound-webhooks).

### `plan_internal_ingress_not_allowed`

HTTP 402 Payment Required · Plan does not unlock internal-only ingress

See [Networking](networking.md).

### `plan_job_registry_credential_quota`

HTTP 413 Request Entity Too Large · Per-job registry credential quota reached

See [Registry credentials](registry-credentials.md).

### `plan_job_registry_credentials_not_allowed`

HTTP 403 Forbidden · Plan doesn't allow private-registry credentials for jobs

See [Registry credentials](registry-credentials.md).

### `plan_limit_apps`

HTTP 403 Forbidden · App limit reached; Environment branch limit reached; PR preview environment too large

See [Plans and pricing](plans.md#at-a-glance).

### `plan_limit_build_minutes`

HTTP 403 Forbidden · Build minutes used up

See [Plans and pricing](plans.md#builds).

### `plan_limit_concurrency`

HTTP 403 Forbidden or 429 Too Many Requests · Concurrency limit reached; Concurrency over plan limit; Execution concurrency limit reached

See [Plans and pricing](plans.md#at-a-glance), [Gregale API specification](faas_openapi_spec.md).

### `plan_limit_deployment_aliases`

HTTP 403 Forbidden · Deployment alias limit reached

See [Deploys](deploys.md#deployment-aliases).

### `plan_limit_developer_apps`

HTTP 403 Forbidden · Developer environment limit reached

See [Plans and pricing](plans.md#developer-environments).

### `plan_limit_developer_lease`

HTTP 403 Forbidden · Developer environment lease over plan limit

See [Plans and pricing](plans.md#developer-environments), [`gregale dev`](gregale-dev.md).

### `plan_limit_edge_rule_list_items`

HTTP 403 Forbidden · Edge rule list too large

See [Edge rules](edge-rules.md#limits).

### `plan_limit_edge_rule_lists`

HTTP 403 Forbidden · Edge rule list limit reached

See [Edge rules](edge-rules.md#limits).

### `plan_limit_edge_rules`

HTTP 403 Forbidden · Edge rule limit reached

See [Edge rules](edge-rules.md#limits).

### `plan_limit_env_vars`

HTTP 403 Forbidden · Env var count limit reached

See [Environment variables](env.md).

### `plan_limit_project_secrets`

HTTP 403 Forbidden · Project secret limit reached

See [Secrets](secrets.md#project-environment-secrets).

### `plan_limit_ram`

HTTP 403 Forbidden · RAM over plan limit

See [Plans and pricing](plans.md#at-a-glance).

### `plan_limit_secrets`

HTTP 403 Forbidden · Secret count limit reached

See [Secrets](secrets.md).

### `plan_limit_trusted_signers`

HTTP 403 Forbidden · Trusted signer count limit reached; Trusted signers are not allowed on this plan

See [Security](security.md#app-deploy-guard).

### `plan_liveness_probe_not_allowed`

HTTP 403 Forbidden · GRPC liveness probes are not allowed on this plan; Liveness probes are not allowed on this plan; Plan doesn't allow a liveness probe

See [Deployment overrides](deploy-overrides.md).

### `plan_log_archive_not_allowed`

HTTP 402 Payment Required · Log archive unavailable on this plan

See [Logs](logs.md#retention).

### `plan_log_drain_quota`

HTTP 403 Forbidden · Log drain limit reached

See [Logs](logs.md#log-drains).

### `plan_log_drains_not_allowed`

HTTP 402 Payment Required · Log drains unavailable on this plan

See [Logs](logs.md#log-drains).

### `plan_max_instances_not_allowed`

HTTP 403 Forbidden · Plan doesn't allow a max_instances ceiling

See [Scaling policy](scaling-policy.md#manifest-form).

### `plan_min_instances_not_allowed`

HTTP 403 Forbidden · Plan doesn't allow a min-instances floor

See [How scaling to zero works](cold-wake.md#opting-out-keep-n-instances-warm-pro-scale).

### `plan_mirror_not_allowed`

HTTP 403 Forbidden · Plan doesn't allow traffic mirroring

See [Progressive rollouts](rollouts.md#traffic-mirroring).

### `plan_openapi_doc_quota_reached`

HTTP 403 Forbidden · OpenAPI document limit reached

See [Plans and pricing](plans.md).

### `plan_openapi_doc_too_large`

HTTP 413 Request Entity Too Large · OpenAPI document exceeds the per-plan byte cap

See [Plans and pricing](plans.md).

### `plan_per_app_metrics_not_allowed`

HTTP 402 Payment Required · Per-app metrics unavailable on this plan

See [Plans and pricing](plans.md).

### `plan_private_network_not_allowed`

HTTP 402 Payment Required · Plan does not unlock private network attachments

See [Networking](networking.md).

### `plan_public_auth_basic_not_allowed`

HTTP 402 Payment Required · Basic public-URL auth is not available on this plan

See [Authentication](auth.md#app-access-control).

### `plan_public_auth_bearer_not_allowed`

HTTP 402 Payment Required · Bearer public-URL auth is not available on this plan

See [Authentication](auth.md#app-access-control).

### `plan_public_auth_ip_allowlist_not_allowed`

HTTP 403 Forbidden · Plan doesn't allow a public-auth IP allowlist

See [Authentication](auth.md#app-access-control).

### `plan_queue_backlog_bytes`

HTTP 403 Forbidden · Account queue backlog full

See [Event-driven workloads](event-driven.md#queue-backlog).

### `plan_queue_depth`

HTTP 403 Forbidden · Per-app queue depth exceeded

See [Event-driven workloads](event-driven.md#queue-depth).

### `plan_registry_credential_quota`

HTTP 413 Request Entity Too Large · Per-app registry credential quota reached

See [Registry credentials](registry-credentials.md).

### `plan_registry_credentials_not_allowed`

HTTP 403 Forbidden · Plan doesn't allow private-registry credentials

See [Registry credentials](registry-credentials.md).

### `plan_reorder_disabled`

HTTP 402 Payment Required · Plan doesn't allow reorder or deploy-immediately

Cancel + clear-obsolete remain available on Free; upgrade to unlock reorder/deploy-immediately.

See [Plans and pricing](plans.md).

### `plan_require_authn_not_allowed`

HTTP 403 Forbidden · Per-app authentication is not allowed on this plan; Per-deployment authentication is not allowed on this plan

See [Plans and pricing](plans.md).

### `plan_rollback_on_5xx_not_allowed`

HTTP 403 Forbidden · Plan doesn't allow 5xx auto-rollback

See [Plans and pricing](plans.md).

### `plan_route_metrics_not_allowed`

HTTP 403 Forbidden · Per-route metrics are not allowed on this plan

See [Plans and pricing](plans.md).

### `plan_source_bytes`

HTTP 413 Request Entity Too Large · Invocation payload too large

See [Event-driven workloads](event-driven.md).

### `plan_static_egress_ip_not_allowed`

HTTP 402 Payment Required · Plan does not unlock static egress IP

See [Static egress IP](static-egress-ip.md).

### `plan_static_egress_ip_quota`

HTTP 403 Forbidden · Static egress IP quota reached

See [Static egress IP](static-egress-ip.md).

### `plan_streaming_not_allowed`

HTTP 403 Forbidden · Streaming responses are not allowed on this plan

See [Plans and pricing](plans.md).

### `plan_token_stale`

HTTP 409 Conflict · Plan_token does not match project binding; Plan_token does not match uploaded source; Project selection changed after review

### `plan_traffic_split_not_allowed`

HTTP 403 Forbidden · Plan doesn't allow traffic splitting; Route enforcement unavailable; Route health enforcement unavailable

See [Plans and pricing](plans.md).

### `plan_trigger_quota`

HTTP 403 Forbidden · Trigger limit reached

See [Plans and pricing](plans.md).

### `plan_triggers_not_allowed`

HTTP 402 Payment Required · Triggers unavailable on this plan

See [Plans and pricing](plans.md).

### `plan_warm_pool_not_allowed`

HTTP 403 Forbidden · Warm-pool capacity is not allowed on this plan

See [Plans and pricing](plans.md).

### `plan_warm_snapshot_not_allowed`

HTTP 403 Forbidden · Warm-tier snapshots are not allowed on this plan

See [Plans and pricing](plans.md).

### `plan_webhook_quota`

HTTP 403 Forbidden · Webhook limit reached

See [Plans and pricing](plans.md).

### `plan_webhooks_not_allowed`

HTTP 402 Payment Required · Outbound webhooks unavailable on this plan

See [Plans and pricing](plans.md).

### `plan_websocket_not_allowed`

HTTP 403 Forbidden · WebSocket / Upgrade traffic is not allowed on this plan

See [Plans and pricing](plans.md).

### `plan_workflows_not_allowed`

HTTP 402 Payment Required · Workflows unavailable on this plan

See [Plans and pricing](plans.md).

### `plan_workflows_quota`

HTTP 403 Forbidden · Stored workflow definition quota exceeded; Workflow run quota exceeded

See [Plans and pricing](plans.md).

### `platform_release_paused`

HTTP 503 Service Unavailable · Releases are paused

See [Release approvals, freezes and schedules](release-controls.md).

### `postgres_bypass_rls_conflict`

HTTP 409 Conflict · BYPASSRLS conflicts with Data API or Auth

Use a separate database for the tool that needs BYPASSRLS, or declare the role without --bypass-rls.

See [Custom Postgres roles](postgres-custom-roles.md).

### `postgres_bypass_rls_unavailable`

HTTP 422 Unprocessable Entity · BYPASSRLS unavailable

See [Custom Postgres roles](postgres-custom-roles.md).

### `postgres_custom_role_default_taken`

HTTP 409 Conflict · Default role already set

Remove it there first (gregale postgres roles create &lt;database> &lt;role> --default-for '' on that role).

See [Custom Postgres roles](postgres-custom-roles.md).

### `postgres_custom_roles_limit`

HTTP 409 Conflict · Custom role limit reached

Delete a role you no longer need (gregale postgres roles delete).

### `postgres_custom_roles_unavailable`

HTTP 403 Forbidden · Postgres custom roles unavailable

### `postgres_custom_roles_unsupported`

HTTP 422 Unprocessable Entity · Custom roles unsupported

### `postgres_query_failed`

HTTP 422 Unprocessable Entity · Query failed

The statement runs read-only as a login that can read the public schema, for at most 5 seconds. Fix the statement and try again.

### `postgres_query_unsupported`

HTTP 501 Not Implemented · Queries unsupported

### `pr_preview_resource_unsupported`

HTTP 409 Conflict

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

HTTP 404 Not Found · Preview environment not found

### `preview_expired`

HTTP 410 Gone · Review source again; Review this project again; Preview source expired

### `preview_full`

HTTP 409 Conflict · Preview is full

See [CLI guide](cli-setup.md).

### `preview_not_found`

HTTP 404 Not Found · Preview not found

### `preview_not_offered`

HTTP 404 Not Found · Preview not offered

See [CLI guide](cli-setup.md).

### `preview_org_unsupported`

HTTP 409 Conflict · Preview cannot be joined by an organization

See [CLI guide](cli-setup.md).

### `preview_plan_not_eligible`

HTTP 403 Forbidden · Plan not eligible for this preview

See [CLI guide](cli-setup.md).

### `preview_production_dependency_denied`

HTTP 403 Forbidden · Preview dependency denied

See [Internal services](internal-services.md), [PR preview environments](preview-environments.md).

### `private_invocation_disabled`

HTTP 503 Service Unavailable · Private invocation unavailable

### `private_network_fabric_unavailable`

Private network fabric unavailable

### `private_network_invalid`

HTTP 400 Bad Request · Invalid private network attachment

See [Networking](networking.md).

### `private_network_not_enabled`

HTTP 503 Service Unavailable · Private network attachments are not enabled on this cluster

See [Networking](networking.md).

### `private_network_transport_unavailable`

Private network transport unavailable

### `private_network_unavailable`

Private network firewall updates unavailable; Private network policy updates unavailable; Private network attachment updates unavailable

### `private_publication_disabled`

HTTP 503 Service Unavailable · Private publication unavailable

### `prod_branch_mismatch`

HTTP 409 Conflict · Production branch does not match the existing project

### `project_apply_stale`

HTTP 409 Conflict · Project changed during apply

### `project_configuration_failed`

HTTP 503 Service Unavailable · Project configuration incomplete

### `project_environment_approval_invalid`

HTTP 409 Conflict · Many operations send this code; the response's `title` and `detail` name the failure.

### `project_environment_approval_required`

HTTP 409 Conflict · Protected environment; Protected environment approval required

Change it in the source environment and promote, or run `gregale project apply` and approve the plan. Deleting or rotating a secret stays open for incident response.

Run `gregale project apply` or `gregale project promote` and approve the plan, or deploy with no traffic to stage the release.

### `project_promotion_queue_full`

HTTP 429 Too Many Requests · Promotion queue is full

See [Environment promotion](environment-promotion.md#promotion-admission).

### `project_release_baseline_expired`

HTTP 410 Gone · Rollback baseline expired

See [Atomic project releases](project-releases.md).

### `project_release_control_conflict`

HTTP 409 Conflict · Deployment control conflicted

### `project_release_rollback_required`

HTTP 409 Conflict · Project release owns traffic; Project release rollback required

Roll back the project environment release, then retry this app rollback if needed.

### `project_release_secrets_pinned`

HTTP 409 Conflict · Secret versions are release-bound

Bind the bucket in an environment whose release does not pin secrets, or release without pinned secrets first.

Bind the database in an environment whose release does not pin secrets, or release without pinned secrets first.

See [Secrets](secrets.md).

### `project_required`

HTTP 409 Conflict · Project required

### `project_retry_changed`

HTTP 409 Conflict · Project retry stopped

### `project_rollback_already_complete`

HTTP 409 Conflict · Rollback already complete

### `project_rollback_auto_undo_blocked`

HTTP 409 Conflict · Automatic rollback undo paused

### `project_rollback_conflict`

HTTP 409 Conflict · Project rollback already running; Project rollback conflict; Rollback resume unavailable

### `project_rollback_health_failed`

HTTP 409 Conflict · Rollback health verification paused

### `project_rollback_health_probe_required`

HTTP 409 Conflict · Health probe required

### `project_rollback_health_timeout`

HTTP 409 Conflict · Rollback health verification paused

### `project_rollback_live_traffic_failed`

HTTP 409 Conflict · Rollback live-traffic verification paused

### `project_rollback_live_traffic_required`

HTTP 409 Conflict · Production error budget required; Request telemetry required

### `project_rollback_live_traffic_timeout`

HTTP 409 Conflict · Rollback live-traffic verification paused

### `project_rollback_paused`

HTTP 409 Conflict · Project rollback paused

### `project_rollback_plan_changed`

HTTP 409 Conflict · Reviewed resume plan changed; Reviewed rollback plan changed; Reviewed undo plan changed

### `project_rollback_pre_cutover_health_failed`

HTTP 409 Conflict · Pre-cutover health verification failed

### `project_rollback_pre_cutover_health_timeout`

HTTP 409 Conflict · Pre-cutover health verification failed

### `project_rollback_preparation_expired`

HTTP 409 Conflict · Rollback preparation expired

### `project_rollback_preparation_released`

The response's `title` and `detail` describe the failure.

### `project_rollback_prepared`

The response's `title` and `detail` describe the failure.

### `project_rollback_request_reused`

HTTP 409 Conflict · Rollback request ID already used

### `project_rollback_undo_artifacts_unavailable`

The response's `title` and `detail` describe the failure.

### `project_rollback_undo_started`

HTTP 409 Conflict · Rollback undo already accepted

### `project_rollback_undo_unavailable`

HTTP 409 Conflict · Rollback undo unavailable

### `project_selection_empty`

HTTP 409 Conflict · No workloads selected

See [Deploy a multi-service app](multi-service-apps.md).

### `project_services_call_each_other`

HTTP 409 Conflict · Project services call each other

Delete the services instead (gregale app delete), or pass detach_services=true (gregale project delete --detach-services) once they no longer call each other by name.

### `project_submission_exists`

HTTP 409 Conflict · Project submission already registered

### `project_submission_mismatch`

HTTP 409 Conflict · Project submission intent changed

### `promotion_rolled_back`

The response's `title` and `detail` describe the failure.

### `provision_capacity`

HTTP 429 Too Many Requests · Provisioning capacity reached

### `public_auth_config_invalid`

HTTP 503 Service Unavailable · Public authentication unavailable

### `public_auth_ip_allowlist_too_long`

HTTP 400 Bad Request · Public-auth IP allowlist too long

See [Authentication](auth.md#app-access-control).

## Q

### `queue_binding_environment_unavailable`

HTTP 409 Conflict · Queue environment unavailable

### `queue_binding_keyed_work`

HTTP 409 Conflict · Keyed work still queued

### `queue_binding_retired`

HTTP 409 Conflict · Queue binding retired

### `queue_canary_changed`

HTTP 409 Conflict · Queue canary blocked

### `queue_environment_work_policy_unavailable`

HTTP 409 Conflict · Scoped work policy unavailable

### `queue_groups_unavailable`

HTTP 403 Forbidden · Ordered message groups unavailable

### `queue_message_not_processed`

HTTP 409 Conflict · Queue message not processed

### `queue_receive_busy`

HTTP 429 Too Many Requests · Too many queue receives in flight

See [Errors](errors.md).

### `queue_replay_requires_binding`

HTTP 409 Conflict · Queue replay requires its binding

### `quota_exhausted`

HTTP 429 Too Many Requests · Custom domain quota reached; Domain verification rate limited; Profile capture limit reached

See [Gregale API specification](faas_openapi_spec.md).

## R

### `rate_limit_unavailable`

HTTP 503 Service Unavailable · Request admission unavailable

### `rate_limited`

HTTP 429 Too Many Requests · Rate limit exceeded; Pre-auth rate limit exceeded

See [Edge rules](edge-rules.md).

### `realtime_connection_limit`

HTTP 429 Too Many Requests · Realtime connection limit reached

See [Managed realtime](realtime.md#fair-use-limits).

### `realtime_group_bound_to_trigger`

HTTP 409 Conflict · Consumer group already bound; Consumer group is bound to a trigger

See [Managed realtime](realtime.md#function-triggers).

### `realtime_unavailable`

HTTP 503 Service Unavailable · Managed realtime temporarily unavailable

Retry the connection in a few seconds.

See [Plans and pricing](plans.md#managed-realtime), [Errors](errors.md).

### `realtime_write_rate_limited`

HTTP 429 Too Many Requests · Realtime write rate limited

See [Managed realtime](realtime.md#fair-use-limits).

### `recent_sign_in_required`

HTTP 403 Forbidden · Sign in again

See [Authentication](auth.md), [Account and organization management](account.md).

### `registry_credential_not_found`

HTTP 400 Bad Request · Registry credential not set

See [Registry credentials](registry-credentials.md).

### `regression_changed`

HTTP 409 Conflict · Regression changed

### `release_approval_canary_required`

HTTP 409 Conflict · Release approvals need a canary; Release approvals need an approved promotion

Deploy with --rollout safe or fast (or without --rollout immediate and --traffic-percent), or remove the policy with `gregale release approval disable`.

See [Release approvals, freezes and schedules](release-controls.md#release-approvals).

### `release_approval_decided`

HTTP 409 Conflict · Decision not accepted

### `release_approval_policy_not_found`

HTTP 404 Not Found · No approval policy

### `release_approval_rejected`

HTTP 409 Conflict · Release rejected

Deploy a new release; a rejection is final.

See [Release approvals, freezes and schedules](release-controls.md#release-approvals).

### `release_approval_required`

HTTP 409 Conflict · Release waiting for approval

An eligible teammate approves it on its approval page in the dashboard; the rollout then continues on its next tick.

See [Release approvals, freezes and schedules](release-controls.md#release-approvals).

### `release_approval_self`

HTTP 403 Forbidden · Approve someone else's promotion; Self-approval not allowed

Ask an eligible teammate to approve it on the release's approval page in the dashboard.

Ask another approver to approve this promotion, then promote with their approval.

See [Release approvals, freezes and schedules](release-controls.md#release-approvals).

### `release_approval_session_required`

HTTP 403 Forbidden · Approve as a person; Approve in the dashboard

Approve the promotion signed in as yourself, not with an API key or token, then promote with that approval.

See [Release approvals, freezes and schedules](release-controls.md#release-approvals).

### `release_command_failed`

HTTP 422 Unprocessable Entity

Your release command exited non-zero or timed out; the previous release keeps serving

See [Safe releases](releases.md), [Troubleshooting](troubleshooting.md).

### `release_freeze_limit`

HTTP 409 Conflict · Too many freezes

Lift freezes you no longer need with `gregale release freeze lift`.

See [Release approvals, freezes and schedules](release-controls.md#deploy-freezes).

### `release_freeze_not_found`

HTTP 404 Not Found · Freeze not found

List freezes with `gregale release freeze list`. A company's freezes are lifted with --org ORG, or --app APP for a freeze on one app.

### `release_freeze_override_not_allowed`

HTTP 403 Forbidden · Freeze override not allowed

Ask an owner or admin to lift the freeze with `gregale release freeze lift`, or to deploy with --override-freeze.

See [Release approvals, freezes and schedules](release-controls.md#deploy-freezes).

### `release_frozen`

HTTP 409 Conflict · Production releases are frozen

Wait for the freeze to end, lift it with `gregale release freeze lift`, or release anyway with override_freeze and a reason (CLI: --override-freeze REASON); the override is audited.

See [Release approvals, freezes and schedules](release-controls.md#deploy-freezes).

### `release_gate_limit`

HTTP 409 Conflict · Too many release gates

Delete a gate you no longer need with `gregale release gate rm`.

See [Safe releases](releases.md#release-gates).

### `release_gate_not_found`

HTTP 404 Not Found · Release gate not found

List gates with `gregale release gate list`.

### `release_graph_changed`

HTTP 409 Conflict · Active graph required; History unavailable; Release graph changed

### `release_infrastructure_failed`

Release command could not run

A platform problem stopped the release task; your release command did not cause it

See [Deploys](deploys.md), [Safe releases](releases.md), [Troubleshooting](troubleshooting.md).

### `release_not_parked`

HTTP 409 Conflict · Release cannot be scheduled

### `release_outcome_uncertain`

HTTP 422 Unprocessable Entity

See [Safe releases](releases.md).

### `release_phase_unavailable`

HTTP 422 Unprocessable Entity · Release commands unavailable

### `release_scheduled`

HTTP 409 Conflict · Release is scheduled

See [Release approvals, freezes and schedules](release-controls.md).

### `release_staging_changed`

HTTP 409 Conflict · Release staging changed

### `request_body_read_failed`

HTTP 400 Bad Request · Request body could not be read

See [Functions and runtimes](functions.md).

### `request_budget_exceeded`

HTTP 503 Service Unavailable · Request budget exceeded; Request timed out

See [Edge rules](edge-rules.md), [Errors](errors.md), [Host an MCP server](mcp-hosting.md), [Troubleshooting](troubleshooting.md).

### `request_too_large`

HTTP 413 Request Entity Too Large · Event too large; MCP instance probe failed; Request body too large

For larger uploads, use a bucket signed URL (gregale storage ... signed-url).

See [Event-driven workloads](event-driven.md), [Plans and pricing](plans.md), [Storage](storage.md), [Edge rules](edge-rules.md).

### `request_upload_timeout`

HTTP 408 Request Timeout · Request upload timed out

Retry on a stable connection, or use a bucket signed URL for large uploads.

See [Functions and runtimes](functions.md).

### `request_validation_failed`

HTTP 422 Unprocessable Entity · Invalid request

See [Edge rules](edge-rules.md).

### `reset_token_expired`

HTTP 410 Gone · Reset link expired

See [Account and organization management](account.md#profile-sign-in-and-sessions).

### `reset_token_invalid`

HTTP 410 Gone · Reset link invalid

See [Account and organization management](account.md#profile-sign-in-and-sessions).

### `response_too_large`

HTTP 413 Request Entity Too Large · Response exceeded plan cap

### `rollback_attempt_accepted`

HTTP 409 Conflict · Rollback already accepted

### `rollback_attempt_conflict`

HTTP 409 Conflict · Rollback attempt conflict

### `rollback_crosses_release`

HTTP 409 Conflict · Rollback crosses release commands

If the target is compatible with those changes, retry with acknowledge_releases (CLI: --acknowledge-releases). To undo database changes first, restore each listed database to its restore point.

See [Safe releases](releases.md#releases-that-change-the-database), [Troubleshooting](troubleshooting.md).

### `rollback_deployment_changed`

HTTP 409 Conflict · Rollback selection changed; Serving release changed

### `rollback_operation_required`

HTTP 409 Conflict · Checked rollback in progress

### `rollback_target_already_live`

HTTP 409 Conflict · Rollback target is already live

See [Deploys](deploys.md#revisions).

### `rollback_target_database_restored`

HTTP 409 Conflict · Rollback target's database changes were undone; Rollback target's migration was undone

Deploy that code again; its release command runs before it serves.

See [Safe releases](releases.md#rollbacks-that-restore-the-database).

### `rollback_target_ineligible`

HTTP 409 Conflict · Rollback target is not eligible

See [Deploys](deploys.md#revisions).

### `rollback_target_not_found`

HTTP 404 Not Found · Rollback target not found

See [Deploys](deploys.md#revisions).

### `rollback_target_unavailable`

HTTP 409 Conflict · Rollback target is unavailable

See [Deploys](deploys.md#revisions).

### `rollout_not_stuck`

HTTP 409 Conflict · Rollout is not stuck

See [Progressive rollouts](rollouts.md#why-a-rollout-advanced-waits-or-stopped).

### `rollout_state_invalid`

HTTP 409 Conflict · Rollout state does not permit recovery; Rollouts are in progress in more than one environment

See [Progressive rollouts](rollouts.md#why-a-rollout-advanced-waits-or-stopped).

### `route_health_blocked`

HTTP 409 Conflict · Route health gate blocked

### `run_artifact_destination_limit`

HTTP 413 Request Entity Too Large · Artifact exceeds destination limit

### `run_artifact_integrity`

HTTP 422 Unprocessable Entity · Artifact could not be verified

### `run_attribution_denied`

HTTP 403 Forbidden · Run attribution refused

### `run_control_revision_conflict`

HTTP 409 Conflict · Run control changed

## S

### `safe_release_unavailable`

HTTP 503 Service Unavailable · Safe release unavailable

### `saved_insight_limit`

HTTP 409 Conflict · Saved insight limit reached

### `scaling_request_conflict`

HTTP 409 Conflict · Scaling request ID reused

### `scaling_revision_conflict`

HTTP 409 Conflict · Scaling settings changed

### `scaling_target_incompatible_with_workload_class`

HTTP 422 Unprocessable Entity · Target metric is not compatible with this app's workload class

See [Scaling policy](scaling-policy.md).

### `scan_critical`

HTTP 503 Service Unavailable · Many operations send this code; the response's `title` and `detail` name the failure.

### `scheduled_release_not_found`

HTTP 404 Not Found · No scheduled release

### `scope_exclusion_not_found`

HTTP 404 Not Found · No persisted exclusion matches

### `secret_entry_unavailable`

HTTP 410 Gone · Secret entry unavailable

### `secret_invalid_key`

HTTP 400 Bad Request · Invalid secret key

See [Secrets](secrets.md).

### `secret_not_found`

HTTP 400 Bad Request · Secret not set; Secret unavailable

See [Secrets](secrets.md).

### `secret_reference_conflict`

HTTP 409 Conflict · Reference conflict

### `secret_reference_suppression_limit`

HTTP 403 Forbidden · Suppression limit reached

See [Environment variables](env.md).

### `secret_scan_strict`

HTTP 422 Unprocessable Entity · Secret-shaped values found in uploaded source

See [Secrets](secrets.md).

### `secret_store_unavailable`

HTTP 503 Service Unavailable · Secret storage unavailable

### `secret_value_too_large`

HTTP 413 Request Entity Too Large · Secret value too large

See [Secrets](secrets.md).

### `security_posture_blocked`

HTTP 403 Forbidden or 409 Conflict or 503 Service Unavailable · App is security quarantined; Security posture blocks this deploy

See [Security](security.md#app-deploy-guard).

### `security_quarantine_recovery_blocked`

HTTP 409 Conflict · Security quarantine recovery is blocked

See [Security](security.md#quarantine-recovery).

### `security_scan_blocked`

HTTP 422 Unprocessable Entity

### `service_abort_attempt_accepted`

HTTP 409 Conflict · Service abort already accepted

### `service_abort_attempt_conflict`

HTTP 409 Conflict · Service abort attempt conflict

### `service_recovery_capacity_unavailable`

HTTP 503 Service Unavailable · Service recovery capacity unavailable

### `session_expired`

HTTP 401 Unauthorized · Session expired; Sign in to verify

### `session_invalid`

HTTP 401 Unauthorized · Session invalid

### `sidecar_cap_exceeded`

HTTP 400 Bad Request · Too many sidecars

See [Application companions](companions.md#limits-and-lifecycle).

### `sidecar_invalid_cpu_millicores`

HTTP 400 Bad Request · Invalid sidecar cpu_millicores

### `sidecar_invalid_disk_io_profile`

HTTP 400 Bad Request · Invalid sidecar disk_io_profile

### `sidecar_invalid_image`

HTTP 400 Bad Request · Invalid sidecar image

### `sidecar_invalid_name`

HTTP 400 Bad Request · Invalid sidecar name

### `sidecar_invalid_port`

HTTP 400 Bad Request · Invalid sidecar port

### `sidecar_invalid_ram_mb`

HTTP 400 Bad Request · Invalid sidecar ram_mb

### `sidecar_invalid_scratch_mb`

HTTP 400 Bad Request · Invalid sidecar scratch_mb

### `sidecar_invalid_type`

HTTP 400 Bad Request · Invalid sidecar type

### `sidecar_not_allowed_on_plan`

HTTP 403 Forbidden · Plan doesn't allow sidecars

See [Plans and pricing](plans.md).

### `sidecar_stateful_denied`

HTTP 403 Forbidden · Stateful sidecar image is not allowed

See [Application companions](companions.md#writable-paths).

### `snapshot_backoff`

HTTP 503 Service Unavailable · Snapshot temporarily unavailable

### `snapshot_unavailable`

HTTP 410 Gone · Snapshot unavailable

### `source_invalid`

HTTP 400 Bad Request or 409 Conflict · Many operations send this code; the response's `title` and `detail` name the failure.

### `source_ref_stale`

HTTP 409 Conflict · Repository changed; Source ref is stale

See [Deploy from a GitHub source ref](source-ref.md).

### `source_ref_unavailable`

HTTP 503 Service Unavailable · Source-ref fetch unavailable

See [Deploy from a GitHub source ref](source-ref.md#failure-modes).

### `source_too_large`

HTTP 413 Request Entity Too Large · Source too large; Entry too large

See [Builds and framework detection](build.md#limits).

### `stage_dependency_restore_failed`

Dependencies restore failed

Your package manager install step exited non-zero inside the build VM

### `stage_image_build_oom`

Image build ran out of memory

The build VM hit its 2 GB memory ceiling during image_build

### `stage_image_build_timeout`

Image build timed out

The build VM exceeded the 15-minute build-time ceiling

### `stage_readiness_failed`

Readiness probe failed

The deployed VM's /healthz never returned 200

### `stage_security_scan_findings`

Security scan flagged findings

Grype found CVEs at or above the plan's severity threshold

### `stage_snapshot_prepare_timeout`

HTTP 422 Unprocessable Entity · Snapshot prime exceeded the scheduler deadline

The snapshot_prepare stage exceeded the scheduler's wall-clock ceiling

### `stage_source_download_failed`

Source download failed

The build VM could not fetch your repository tarball

### `stateless_only_violation`

HTTP 422 Unprocessable Entity · Stateless-only platform

This app shape needs persistent storage we don't provide

See [Storage](storage.md).

### `statement_invoice_export_in_progress`

HTTP 409 Conflict · Statement invoice export in progress

### `static_egress_ip_not_enabled`

HTTP 402 Payment Required · Static egress IP feature is not enabled on this cluster

See [Static egress IP](static-egress-ip.md).

### `static_egress_ip_not_provisioned`

HTTP 404 Not Found · Static egress IP not provisioned on this host

See [Static egress IP](static-egress-ip.md).

### `step_up_required`

HTTP 403 Forbidden · Step-up required

### `stored_workflow_events_unavailable`

HTTP 409 Conflict · Custom events are not enabled

See [Build stored workflows in Gregale](stored-workflow-authoring.md#start-on-an-event).

### `stored_workflow_failure_policy_conflict`

HTTP 409 Conflict · Stored workflow failure policy changed

### `stored_workflow_invalid`

HTTP 422 Unprocessable Entity · Invalid stored workflow

### `stored_workflow_ownership_conflict`

HTTP 409 Conflict · Confirm YAML ownership change

### `stored_workflow_publish_check_required`

HTTP 409 Conflict · Publishing checks required

### `stored_workflow_version_conflict`

HTTP 409 Conflict · Stored workflow changed; Suite target changed; Verification target changed

### `streaming_not_available`

HTTP 413 Request Entity Too Large · Streaming response exceeded plan cap

### `suspend_timeout_always_on`

HTTP 409 Conflict · Database is always-on

### `synthetic_check_invalid`

HTTP 400 Bad Request or 409 Conflict · App is internal; Invalid synthetic check; Synthetic check name already exists

See [Synthetic checks](synthetic-checks.md).

### `synthetic_check_limit_reached`

HTTP 422 Unprocessable Entity · Synthetic check limit reached

See [Synthetic checks](synthetic-checks.md).

## T

### `traffic_change_during_canary`

HTTP 409 Conflict · Canary rollout controls traffic

See [Progressive rollouts](rollouts.md#how-a-rollout-proceeds).

### `traffic_deadline_invalid`

The response's `title` and `detail` describe the failure.

### `traffic_deadline_unavailable`

The response's `title` and `detail` describe the failure.

### `traffic_percent_sum_invalid`

HTTP 409 Conflict · No deployment can take the remaining traffic; Traffic_percent sum invariant violated

See [Progressive rollouts](rollouts.md), [Deploys](deploys.md).

### `traffic_policy_too_complex`

HTTP 422 Unprocessable Entity · Traffic policy analysis limit reached

See [Edge rules](edge-rules.md#limits).

### `traffic_policy_too_large`

HTTP 422 Unprocessable Entity · Traffic policy too large

Reduce policy values and replace the policy. Clear an environment overlay or disable and clear its scoped route contract to recover it.

Reduce rules or preset values that match the same host. Size-reducing repairs to an existing oversized host remain allowed.

See [Edge rules](edge-rules.md#limits).

### `traffic_policy_unavailable`

HTTP 503 Service Unavailable · Traffic policy unavailable

See [Edge rules](edge-rules.md).

### `traffic_revocation_unavailable`

HTTP 503 Service Unavailable · Traffic security verification unavailable

### `traffic_revoked`

HTTP 403 Forbidden · Traffic revoked

### `traffic_serving_changed`

HTTP 409 Conflict · Production revision changed; Traffic split changed

See [Progressive rollouts](rollouts.md).

### `traffic_split_release_limit`

HTTP 422 Unprocessable Entity · Too many releases in the traffic split

See [Progressive rollouts](rollouts.md), [Deploys](deploys.md).

### `transport_error`

HTTP 503 Service Unavailable · Could not reach Gregale

See [Errors](errors.md).

### `trigger_batch_window_too_large`

HTTP 403 Forbidden · Batch_window too large for this plan

See [Plans and pricing](plans.md).

### `trigger_immutable`

HTTP 400 Bad Request · Binding is immutable; Kind=cron not supported on POST /v1/triggers — use POST /v1/crons

### `trigger_inconsistent`

HTTP 500 Internal Server Error · Trigger has no cron_id

### `trigger_invalid_config`

HTTP 422 Unprocessable Entity · Many operations send this code; the response's `title` and `detail` name the failure.

### `trigger_invalid_retry_policy`

HTTP 422 Unprocessable Entity · Invalid retry policy

### `trigger_kind_not_allowed`

HTTP 403 Forbidden · Trigger kind not available

See [Plans and pricing](plans.md).

### `trigger_kind_unavailable`

HTTP 422 Unprocessable Entity · Trigger kind unavailable

### `trigger_tls_skip_verify_not_allowed`

HTTP 403 Forbidden · Tls.skip_verify unavailable on this plan

See [Plans and pricing](plans.md).

### `trusted_signer_invalid`

HTTP 400 Bad Request · Trusted signer invalid

See [Security](security.md#app-deploy-guard).

### `trusted_signer_not_found`

HTTP 404 Not Found · Trusted signer not found

## U

### `udp_listener_limit`

HTTP 409 Conflict · UDP listener reservation limit reached

See [Networking](networking.md#private-tcp-between-services).

### `unauthorized`

HTTP 401 Unauthorized · Many operations send this code; the response's `title` and `detail` name the failure.

### `unavailable`

Migration unavailable

See [Apps](apps.md), [Billing](billing.md), [Jobs from the CLI](cli-jobs.md), [Secrets and required configuration](cli-secrets.md).

### `undo_plan_changed`

HTTP 409 Conflict · Undo plan changed

### `undo_too_many_changes`

HTTP 422 Unprocessable Entity · Too many changes

### `unsupported_by_cli`

HTTP 403 Forbidden · Endpoint requires the dashboard session cookie

See [CLI guide](cli-setup.md).

### `unsupported_media_type`

HTTP 415 Unsupported Media Type · Unsupported media type

See [Edge rules](edge-rules.md).

### `upload_checksum_mismatch`

HTTP 410 Gone · Uploaded archive does not match its checksum

Start the upload again; gregale deploy does this automatically.

See [Deploys](deploys.md#what-gets-uploaded).

### `upload_session_already_cancelled`

HTTP 409 Conflict · Upload session not open

See [Deploys](deploys.md#what-gets-uploaded).

### `upload_session_already_committed`

HTTP 409 Conflict · Upload session already committed

See [Deploys](deploys.md#what-gets-uploaded).

### `upload_session_expired`

HTTP 410 Gone · Upload session expired

See [Deploys](deploys.md#what-gets-uploaded).

### `upload_session_not_found`

HTTP 404 Not Found · Upload session not found

See [Deploys](deploys.md#what-gets-uploaded).

### `upload_session_offset_conflict`

HTTP 409 Conflict · Upload session offset conflict

See [Deploys](deploys.md#what-gets-uploaded).

### `upload_session_too_many`

HTTP 429 Too Many Requests · Too many upload sessions

See [Deploys](deploys.md#what-gets-uploaded).

## V

### `validation_failed`

HTTP status varies by operation · Many operations send this code; the response's `title` and `detail` name the failure.

### `verification_link_invalid`

HTTP 410 Gone · Verification link no longer valid

Request a new verification email, then open the latest link.

See [Authentication](auth.md#email-verification), [Errors](errors.md).

## W

### `waf_blocked`

HTTP 403 Forbidden · Request blocked

See [Edge rules](edge-rules.md), [Errors](errors.md), [Troubleshooting](troubleshooting.md).

### `wait_for_warm`

HTTP 503 Service Unavailable · Scale-out cooldown in effect

See [Scaling policy](scaling-policy.md).

### `wait_timed_out`

HTTP 408 Request Timeout · Wait timed out

### `wake_in_progress`

The response's `title` and `detail` describe the failure.

### `wake_queue_full`

The response's `title` and `detail` describe the failure.

### `wake_with_app_unsupported`

HTTP 409 Conflict · Database cannot start with its app

See [Managed PostgreSQL](postgres.md#plans).

### `webhook_outbox_full`

HTTP 429 Too Many Requests · Webhook outbox full

See [Event-driven workloads](event-driven.md#outbox-backlog).

### `webhook_workflow_conflict`

HTTP 409 Conflict · Webhook workflow conflict

### `webhook_workflow_unavailable`

HTTP 503 Service Unavailable · Webhook workflow unavailable

### `wildcard_domains_not_allowed`

HTTP 402 Payment Required · Wildcard domains unavailable on this plan

See [Domains](domains.md#serve-several-apps-on-one-domain).

### `work_policy_in_use`

HTTP 409 Conflict · Work policy in use

### `work_policy_reserved`

HTTP 409 Conflict · Reserved work policy

### `workflow_callback_binding_conflict`

HTTP 409 Conflict · Workflow callback binding conflict

### `workflow_callback_closed`

HTTP 409 Conflict · Workflow callback closed

### `workflow_callback_expired`

HTTP 410 Gone · Workflow callback expired

### `workflow_callback_payload_conflict`

HTTP 409 Conflict · Workflow callback payload conflict

### `workflow_dag_cycle`

The response's `title` and `detail` describe the failure.

### `workflow_definition_not_found`

HTTP 404 Not Found · Workflow definition not found

### `workflow_deployment_unavailable`

HTTP 501 Not Implemented · Workflow deployment unavailable

### `workflow_event_not_found`

HTTP 404 Not Found

### `workflow_event_replay_preview_read_timeout`

HTTP 503 Service Unavailable · Workflow replay preview read timed out

### `workflow_function_not_invocable`

HTTP 404 Not Found

See [Functions and runtimes](functions.md).

### `workflow_not_running`

HTTP 409 Conflict · Workflow run not running

### `workflow_performance_cohort_changed`

HTTP 409 Conflict · Workflow performance changed

### `workflow_resume_conflict`

HTTP 409 Conflict · Workflow resume conflict

### `workflow_resume_limit`

HTTP 409 Conflict · Workflow resume limit reached

### `workflow_resume_unsafe`

HTTP 409 Conflict · Workflow cannot be resumed

### `workflow_run_not_found`

HTTP 404 Not Found · Workflow run not found

### `workflow_sdk_missing`

The response's `title` and `detail` describe the failure.

### `workflow_step_not_found`

HTTP 404 Not Found · Workflow step not found

### `workflow_step_retry_not_allowed`

HTTP 409 Conflict · Workflow step cannot be retried

### `workflow_tenant_identity_unavailable`

HTTP 409 Conflict · Tenant identity required

### `workflow_wait_interrupted`

HTTP 499 · Workflow wait canceled

See [CLI guide](cli-setup.md).

### `workflow_wait_timeout`

HTTP 408 Request Timeout · Workflow wait timed out

See [CLI guide](cli-setup.md).

### `workload_bucket_not_found`

HTTP 404 Not Found · Granted bucket not found

### `workload_bucket_required`

HTTP 400 Bad Request · Bucket required

### `workload_credential_limit`

HTTP 429 Too Many Requests · Too many workload credentials

### `workload_not_production`

HTTP 403 Forbidden · Production workload required

### `workload_selector_ambiguous`

HTTP 400 Bad Request
