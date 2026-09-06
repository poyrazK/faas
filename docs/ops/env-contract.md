# Daemon environment contract

<!-- GENERATED from pkg/daemonunitspec/envcontract.go — do not edit. -->
<!-- Regenerate: UPDATE_ENV_CONTRACT=1 go test ./pkg/daemonunitspec -run DocInSync -->

Every `FAAS_*` variable a production daemon reads, and which deploy path
delivers it. Enforced by `pkg/daemonunitspec/envcontract_test.go` (ADR-143).

| Source | Meaning |
|---|---|
| `unit` | `Environment=` in the daemon's generated systemd unit |
| `dropin` | systemd drop-in rendered by an ansible role |
| `envfile` | `EnvironmentFile` staged by ansible, node_join or the manifest renderer |
| `secrets-env` | operator-provisioned secret file under `/etc/faas/secrets/` or `/etc/faas/sealed.env` |
| `runtime-config` | DB-backed runtime config; env is only the boot default |
| `default` | optional; the code default is production-correct |
| `script` | consumed by a `deploy/scripts/` script |
| `internal` | set by a daemon for its own subprocess |
| `client` | read by the CLI/SDK on the operator's machine |
| `dev-only` | tests / e2e / Lima only — never set in production |
| `guest` | read by guest-init inside the microVM |

| Variable | Owners | Source | Note |
|---|---|---|---|
| `FAAS_ACCOUNT_SPEND_AGGREGATOR_INTERVAL` | meterd | `default` |  |
| `FAAS_ADMIN_EMAILS` | apid | `secrets-env` | delivered by /etc/faas/sealed.env (apid, operator-provisioned via `gregalectl secrets init`) |
| `FAAS_ALERT_EVAL_INTERVAL` | meterd | `default` |  |
| `FAAS_APID_ADVISORY_SOCK` | apid, vmmd, shared | `unit` |  |
| `FAAS_APID_APP_ERRORS_SOCKET` | apid, gatewayd-internal | `default` |  |
| `FAAS_APID_APP_ERRORS_TARGET` | apid, gatewayd-internal | `dropin` |  |
| `FAAS_APID_APP_ERRORS_TLS_CA_PATH` | apid, gatewayd-internal | `dropin` |  |
| `FAAS_APID_APP_ERRORS_TLS_CERT_PATH` | apid, gatewayd-internal | `dropin` |  |
| `FAAS_APID_APP_ERRORS_TLS_KEY_PATH` | apid, gatewayd-internal | `dropin` |  |
| `FAAS_APID_AUTH_SOCKET` | gatewayd-public | `default` |  |
| `FAAS_APID_BASE_URL` | meterd | `default` |  |
| `FAAS_APID_GITHUBD_BRIDGE_SOCK` | apid, githubd | `default` |  |
| `FAAS_APID_LISTEN` | apid | `default` |  |
| `FAAS_APID_LOOPBACK` | gatewayd-internal | `default` |  |
| `FAAS_APID_METRICS_ADDR` | apid | `default` |  |
| `FAAS_APID_OTEL_SPANS_WRITER_SOCKET` | gatewayd-public | `default` |  |
| `FAAS_APID_REQUEST_IDLE_TIMEOUT` | apid | `default` |  |
| `FAAS_APID_REQUEST_MAX_HEADER_BYTES` | apid | `default` |  |
| `FAAS_APID_REQUEST_READ_TIMEOUT` | apid | `default` |  |
| `FAAS_APID_REQUEST_TELEMETRY_SOCKET` | gatewayd-internal | `default` |  |
| `FAAS_APID_REQUEST_TELEMETRY_TARGET` | gatewayd-internal | `dropin` |  |
| `FAAS_APID_REQUEST_WRITE_TIMEOUT` | apid | `default` |  |
| `FAAS_APID_ROLE` | apid, shared | `dropin` |  |
| `FAAS_API_CONTRACT_DIFF_ENABLED` | shared | `default` |  |
| `FAAS_API_HOSTING_SMOKE_URL` | imaged | `default` | optional public origin for post-readiness API hosting smoke verification |
| `FAAS_APPS_DOMAIN` | apid, gatewayd-internal, imaged, shared | `envfile` |  |
| `FAAS_APPS_ROOT` | imaged, shared | `default` |  |
| `FAAS_APP_ERRORS_ENABLED` | apid, gatewayd-internal | `runtime-config` |  |
| `FAAS_ARTIFACT_REPLICATOR` | imaged | `envfile` |  |
| `FAAS_ARTIFACT_SYNC_TARGET` | imaged | `script` | consumed by deploy/scripts/faas-artifact-replicator.sh via /etc/faas/artifact-sync.env |
| `FAAS_ARTIFACT_SYNC_USER` | imaged | `script` | consumed by deploy/scripts/faas-artifact-replicator.sh via /etc/faas/artifact-sync.env |
| `FAAS_AUDIT_HMAC_KEY` | apid | `secrets-env` | delivered by /etc/faas/sealed.env (apid, operator-provisioned via `gregalectl secrets init`) |
| `FAAS_AUDIT_HMAC_KEY_FILE` | apid | `default` |  |
| `FAAS_AUTH_RPC_ENABLED` | apid | `default` |  |
| `FAAS_BASE_EXTRACT_ROOT` | shared | `unit` |  |
| `FAAS_BASE_STAGING_ROOT` | shared | `unit` |  |
| `FAAS_BASE_TMP_ROOT` | shared | `unit` |  |
| `FAAS_BILLING_PORTAL_URL` | apid | `secrets-env` | delivered by /etc/faas/secrets/meterd/billing.env (meterd) and /etc/faas/sealed.env (apid) |
| `FAAS_BILLING_PROVIDER` | shared | `secrets-env` | delivered by /etc/faas/secrets/meterd/billing.env (meterd) and /etc/faas/sealed.env (apid) |
| `FAAS_BRIDGE_HEADERS` | vmmd-stream-bridge | `internal` | set by vmmd for the per-request stream-bridge subprocess |
| `FAAS_BRIDGE_HOST` | vmmd-stream-bridge | `internal` | set by vmmd for the per-request stream-bridge subprocess |
| `FAAS_BRIDGE_METHOD` | vmmd-stream-bridge | `internal` | set by vmmd for the per-request stream-bridge subprocess |
| `FAAS_BRIDGE_PROTOCOL` | vmmd-stream-bridge, shared | `default` | rollback lever, see docs/ops/h2c-rollback.md |
| `FAAS_BRIDGE_URL` | vmmd-stream-bridge | `internal` | set by vmmd for the per-request stream-bridge subprocess |
| `FAAS_BROKER_EGRESS_IFNAME` | schedd | `default` |  |
| `FAAS_BROKER_EGRESS_MBIT` | schedd | `default` |  |
| `FAAS_BUILDERD_CONFIG` | builderd, shared | `unit` |  |
| `FAAS_BUILDERD_ROLE` | builderd, shared | `dropin` |  |
| `FAAS_BUILDER_BASE_PATH` | imaged, shared | `default` |  |
| `FAAS_BUILDER_BASE_REF` | imaged | `dropin` |  |
| `FAAS_CANARY_PROGRESSION_TOKEN` | meterd | `secrets-env` | delivered by /etc/faas/secrets/meterd/billing.env (meterd) and /etc/faas/sealed.env (apid); Safe Deploy activation requires this and FAAS_SAFEDEPLOY_TOKEN together |
| `FAAS_CERT_EXPIRY_REFRESHER_INTERVAL` | meterd | `default` |  |
| `FAAS_CLI_AUTH_URL_BASE` | apid | `default` |  |
| `FAAS_COMPLETION_CACHE_PATH` | shared | `client` | read by the CLI/SDK on the operator's machine, never by a daemon |
| `FAAS_COMPUTE_GATEWAY_DISCOVERY` | gatewayd-public, shared | `unit` |  |
| `FAAS_CONTROL_PLANE_API_TARGET` | gatewayd-public, shared | `unit` |  |
| `FAAS_DATABASE_URL` | shared | `default` | DATABASE_URL from compute-db.env is the production DSN; this is the legacy alias |
| `FAAS_DATA_PLACEMENT` | apid | `runtime-config` |  |
| `FAAS_DEAD_NODE_RECONCILER_INTERVAL_SECONDS` | schedd | `default` |  |
| `FAAS_DEAD_NODE_RECONCILER_STALENESS_SECONDS` | schedd | `default` |  |
| `FAAS_DEPLOY_BASE_REF` | imaged, shared | `envfile` |  |
| `FAAS_DEPLOY_BASE_REF_DEBIAN_PARENT` | shared | `default` |  |
| `FAAS_DEPLOY_BASE_REF_GO124` | shared | `envfile` |  |
| `FAAS_DEPLOY_BASE_REF_GO124_ALPINE` | shared | `envfile` |  |
| `FAAS_DEPLOY_BASE_REF_MINIMAL` | shared | `envfile` |  |
| `FAAS_DEPLOY_BASE_REF_NODE22` | shared | `envfile` |  |
| `FAAS_DEPLOY_BASE_REF_NODE24` | shared | `envfile` |  |
| `FAAS_DEPLOY_BASE_REF_PYTHON312` | shared | `envfile` |  |
| `FAAS_DEPLOY_BASE_REF_PYTHON313` | shared | `envfile` |  |
| `FAAS_DEV` | shared | `dev-only` | must never be set on a production host |
| `FAAS_DEV_TOKEN` | apid | `dev-only` | must never be set on a production host |
| `FAAS_DNS_API_URL` | gatewayd-public | `default` |  |
| `FAAS_DNS_PROVIDER` | gatewayd-public, shared | `default` |  |
| `FAAS_DNS_PROVIDER_SEALED` | gatewayd-public | `default` |  |
| `FAAS_DNS_ZONE` | gatewayd-public | `default` |  |
| `FAAS_DOMAIN_DOCTOR_ENABLED` | apid, shared | `runtime-config` |  |
| `FAAS_DOMAIN_DOCTOR_TTL_SECONDS` | apid | `runtime-config` |  |
| `FAAS_DPA_PATH` | apid | `default` |  |
| `FAAS_DUNNING_INTERVAL` | meterd | `default` |  |
| `FAAS_EGRESS_ALLOW_LOOPBACK` | shared | `dev-only` | must never be set on a production host |
| `FAAS_EGRESS_SOCKET` | shared | `dropin` |  |
| `FAAS_FLOOR_INTERVAL_SECONDS` | schedd | `default` |  |
| `FAAS_FUNCTION_RUNNER_GO124` | imaged, shared | `unit` |  |
| `FAAS_FUNCTION_RUNNER_GO124_ALPINE` | imaged, shared | `unit` |  |
| `FAAS_FUNCTION_RUNNER_NODE22` | imaged, shared | `unit` |  |
| `FAAS_FUNCTION_RUNNER_NODE24` | imaged, shared | `unit` |  |
| `FAAS_FUNCTION_RUNNER_PYTHON312` | imaged, shared | `unit` |  |
| `FAAS_FUNCTION_RUNNER_PYTHON313` | imaged, shared | `unit` |  |
| `FAAS_GATEWAYD_CONFIG` | gatewayd-internal | `dropin` |  |
| `FAAS_GATEWAYD_CONTROL_URL` | apid | `default` |  |
| `FAAS_GATEWAYD_PUBLIC_ROLE` | gatewayd-public, shared | `dropin` |  |
| `FAAS_GATEWAYD_ROLE` | gatewayd-internal, shared | `dropin` |  |
| `FAAS_GATEWAY_CONTROL_LISTEN` | gatewayd-internal | `default` |  |
| `FAAS_GATEWAY_EGRESS_SOCKET` | shared | `default` |  |
| `FAAS_GATEWAY_LISTEN` | gatewayd-internal, shared | `unit` |  |
| `FAAS_GATEWAY_METRICS_URL` | schedd | `dropin` |  |
| `FAAS_GATEWAY_RAW_STREAM_ENABLED` | gatewayd-internal | `default` |  |
| `FAAS_GATEWAY_ROUTE_METRICS` | gatewayd-internal | `default` |  |
| `FAAS_GATEWAY_STREAMING` | gatewayd-internal | `default` | emergency override only; production enables streaming via streaming_enabled=true in gatewayd-internal.toml (ADR-143) |
| `FAAS_GATEWAY_SYNTH_SOCKET` | gatewayd-internal | `default` |  |
| `FAAS_GATEWAY_SYNTH_TARGET` | schedd | `dropin` |  |
| `FAAS_GC_INTERVAL` | imaged | `default` |  |
| `FAAS_GEOIP_AUTO_REFRESH` | gatewayd-internal | `default` | 0; the geoip role owns refresh through re-bootstrap |
| `FAAS_GEOIP_DB_PATH` | gatewayd-internal | `default` | the geoip role stages the DB-IP database at the code default (ADR-143); geo edge rules are no-ops without it |
| `FAAS_GITHUBD_LOOPBACK` | gatewayd-internal | `default` |  |
| `FAAS_GITHUBD_ROLE` | githubd, shared | `dropin` |  |
| `FAAS_GITHUBD_SOCKET` | apid | `default` |  |
| `FAAS_GITHUBD_WORK_DIR` | apid, githubd | `default` |  |
| `FAAS_GITHUB_APP_CLIENT_ID` | apid, githubd | `secrets-env` | delivered by /etc/faas/secrets/githubd/githubd.env (githubd) and /etc/faas/sealed.env (apid) |
| `FAAS_GITHUB_APP_CLIENT_SECRET` | githubd | `secrets-env` | delivered by /etc/faas/secrets/githubd/githubd.env (githubd) and /etc/faas/sealed.env (apid) |
| `FAAS_GITHUB_APP_ID` | githubd | `secrets-env` | delivered by /etc/faas/secrets/githubd/githubd.env (githubd) and /etc/faas/sealed.env (apid) |
| `FAAS_GITHUB_APP_INSTALL_URL` | apid | `secrets-env` | delivered by /etc/faas/secrets/githubd/githubd.env (githubd) and /etc/faas/sealed.env (apid) |
| `FAAS_GITHUB_APP_KEY_PATH` | githubd, shared | `unit` |  |
| `FAAS_GITHUB_APP_REDIRECT_URI` | apid | `secrets-env` | delivered by /etc/faas/secrets/githubd/githubd.env (githubd) and /etc/faas/sealed.env (apid) |
| `FAAS_GITHUB_WEBHOOK_SECRET` | gatewayd-internal, githubd | `secrets-env` | the same GitHub App webhook secret is delivered by /etc/faas/secrets/gatewayd-internal/gatewayd-internal.env and /etc/faas/secrets/githubd/githubd.env |
| `FAAS_GRACE_INTERVAL` | apid | `default` |  |
| `FAAS_GRYPE_BIN` | imaged | `default` |  |
| `FAAS_GUEST_INIT` | imaged, shared | `dropin` |  |
| `FAAS_HOST_AGE_IDENTITY_PATH` | apid, githubd, imaged, meterd, shared | `unit` |  |
| `FAAS_HOST_AGE_KEY` | githubd | `default` |  |
| `FAAS_HOST_AGE_PUB` | githubd | `default` |  |
| `FAAS_HOST_AGE_RECIPIENT_PATH` | apid, vmmd, shared | `unit` |  |
| `FAAS_HOST_BRIDGE_CIDR` | vmmd | `default` |  |
| `FAAS_HOST_HMAC_KEY_PATH` | apid, shared | `unit` |  |
| `FAAS_HOST_KEY_PATH` | gatewayd-internal, gatewayd-public, vmmd | `default` |  |
| `FAAS_HSTS_ENABLED` | apid, shared | `runtime-config` |  |
| `FAAS_IMAGED_METRICS_ADDR` | imaged | `default` |  |
| `FAAS_IMAGED_NODE_NAME` | shared | `default` |  |
| `FAAS_IMAGED_ROLE` | imaged, shared | `dropin` |  |
| `FAAS_INTERNAL_H2C` | gatewayd-public | `default` |  |
| `FAAS_INTERNAL_SOCKET` | gatewayd-public | `default` |  |
| `FAAS_INTERNAL_SVC_KEY_PATH` | schedd | `default` |  |
| `FAAS_INTERNAL_SVC_KEY_SEALED_BLOB` | schedd | `secrets-env` | delivered by /etc/faas/secrets/schedd/schedd.env (schedd) |
| `FAAS_INTERNAL_SVC_KEY_SEALED_NAMESPACE` | schedd | `default` |  |
| `FAAS_INTERNAL_SVC_PUBKEYS` | gatewayd-internal | `default` |  |
| `FAAS_INTERNAL_TARGET` | gatewayd-public, shared | `unit` |  |
| `FAAS_JOB` | guest | `guest` |  |
| `FAAS_JOBS_DISPATCH` | schedd | `unit` | explicit 0 in faas-schedd.service until the vmmd job RPC ships (Mega-1.5); jobs would otherwise sit pending silently |
| `FAAS_LEADER_REDIRECT_TLS_CA` | gatewayd-internal | `default` |  |
| `FAAS_LEADER_REDIRECT_TLS_CERT` | gatewayd-internal | `default` |  |
| `FAAS_LEADER_REDIRECT_TLS_KEY` | gatewayd-internal | `default` |  |
| `FAAS_LOG_ARCHIVE_BUCKET` | shared | `default` |  |
| `FAAS_LOG_ARCHIVE_CREDS_PATH` | shared | `unit` |  |
| `FAAS_LOG_ARCHIVE_ENDPOINT` | shared | `default` |  |
| `FAAS_LOG_ARCHIVE_INTERVAL` | shared | `default` |  |
| `FAAS_LOG_ARCHIVE_KEY_ID` | shared | `default` |  |
| `FAAS_LOG_ARCHIVE_LOCAL_BYTES_MAX` | shared | `default` |  |
| `FAAS_LOG_ARCHIVE_REGION` | shared | `default` |  |
| `FAAS_LOG_ARCHIVE_RETENTION_DAYS` | shared | `default` |  |
| `FAAS_LOG_ARCHIVE_SECRET` | shared | `default` |  |
| `FAAS_LOG_ARCHIVE_SPOOL_ROOT` | shared | `default` |  |
| `FAAS_LOG_LEVEL` | shared | `default` | info; re-read on SIGHUP |
| `FAAS_MAIL_FROM` | shared | `secrets-env` | delivered by /etc/faas/secrets/meterd/billing.env (meterd) and /etc/faas/sealed.env (apid) |
| `FAAS_MAIL_POSTMARK_TOKEN` | shared | `secrets-env` | delivered by /etc/faas/secrets/meterd/billing.env (meterd) and /etc/faas/sealed.env (apid) |
| `FAAS_MAIL_RESEND_API_KEY` | shared | `secrets-env` | delivered by /etc/faas/secrets/meterd/billing.env (meterd) and /etc/faas/sealed.env (apid) |
| `FAAS_MAIL_RESEND_WEBHOOK_SECRET` | apid | `secrets-env` | delivered by /etc/faas/secrets/meterd/billing.env (meterd) and /etc/faas/sealed.env (apid) |
| `FAAS_MAIL_TRANSPORT` | apid, meterd, shared | `secrets-env` | delivered by /etc/faas/secrets/meterd/billing.env (meterd) and /etc/faas/sealed.env (apid) |
| `FAAS_MANAGED_POSTGRES_CONFIG` | shared | `default` | optional provider-registry JSON path; apid loads the dark-wired Neon adapter and reconciler, while the file's provisioning_enabled flag defaults false (ADR-155) |
| `FAAS_MANIFEST_PATH` | imaged | `dropin` |  |
| `FAAS_METERD_ROLE` | meterd, shared | `dropin` |  |
| `FAAS_MFA_RECOVERY_HMAC_KEY` | apid | `secrets-env` | delivered by /etc/faas/sealed.env (apid, operator-provisioned via `gregalectl secrets init`) |
| `FAAS_MIGRATE_LIVE_LEASE_SECONDS` | schedd | `default` |  |
| `FAAS_MIGRATE_LIVE_MAX_PER_TICK` | schedd | `default` |  |
| `FAAS_MIGRATING_WATCHDOG_INTERVAL_SECONDS` | schedd | `default` |  |
| `FAAS_MIGRATING_WATCHDOG_TICK_LIMIT` | schedd | `default` |  |
| `FAAS_NODE_NAME` | apid, builderd, gatewayd-internal, gatewayd-public, githubd, imaged, meterd, schedd, vmmd, shared | `dropin` |  |
| `FAAS_NODE_PUBLIC_IP` | gatewayd-public | `default` |  |
| `FAAS_NOTIFICATIONS_UNSUBSCRIBE_URL` | meterd | `default` |  |
| `FAAS_OBJECT_STORAGE_CONFIG` | apid, shared | `default` | optional provider-registry JSON path; s3_enabled runtime config separately defaults off (docs/object-storage.md); no production activation is promised |
| `FAAS_OCI_BLOB_CACHE_DIR` | imaged | `default` | defaults to <FAAS_STORAGE_CACHE_DIR>/oci-blobs for OCI-backed deployments; local-storage deployments may opt in explicitly |
| `FAAS_OCI_BLOB_CACHE_MAX_BYTES` | imaged | `default` | 8 GiB byte budget for the node-local OCI blob cache; override when sizing compute-node disks |
| `FAAS_OCI_INSECURE` | imaged | `dev-only` | must never be set on a production host |
| `FAAS_OCI_PASSWORD` | shared | `envfile` |  |
| `FAAS_OCI_PULL_TIMEOUT_SECONDS` | imaged | `default` |  |
| `FAAS_OCI_REGISTRY` | imaged, vmmd, shared | `envfile` |  |
| `FAAS_OCI_REPO_PREFIX` | shared | `envfile` |  |
| `FAAS_OCI_TIMEOUT_SECONDS` | shared | `envfile` |  |
| `FAAS_OCI_USERNAME` | shared | `envfile` |  |
| `FAAS_OFF_HOST_BACKUP_RCLONE_CONFIG` | postgres | `script` | LoadCredential= path on the postgresql@.service drop-in; consumed by the archive_command shell in the postgres role |
| `FAAS_OTEL_FLUSH_INTERVAL` | gatewayd-public | `default` |  |
| `FAAS_OTEL_SPANS_WRITER_ENABLED` | apid, gatewayd-public | `default` |  |
| `FAAS_OVERLAY_INTERFACE` | vmmd | `default` |  |
| `FAAS_PADDLE_API_KEY` | shared | `secrets-env` | delivered by /etc/faas/secrets/meterd/billing.env (meterd) and /etc/faas/sealed.env (apid) |
| `FAAS_PADDLE_SANDBOX` | shared | `secrets-env` | delivered by /etc/faas/secrets/meterd/billing.env (meterd) and /etc/faas/sealed.env (apid) |
| `FAAS_PADDLE_WEBHOOK_SECRET` | shared | `secrets-env` | delivered by /etc/faas/secrets/meterd/billing.env (meterd) and /etc/faas/sealed.env (apid) |
| `FAAS_PADDLE_WEBHOOK_TOLERANCE_SECONDS` | shared | `default` |  |
| `FAAS_PERSISTENT_PROTOCOL_V1` | guest | `guest` |  |
| `FAAS_PERSISTENT_WORKER` | shared | `internal` | set by the builder path for its own worker process |
| `FAAS_PGTEST_TEMPLATE_DATABASE` | shared | `dev-only` | must never be set on a production host |
| `FAAS_PLAN_CACHE_ROOT` | apid | `default` |  |
| `FAAS_POLAR_ACCESS_TOKEN` | shared | `secrets-env` | delivered by /etc/faas/secrets/meterd/billing.env (meterd) and /etc/faas/sealed.env (apid) |
| `FAAS_POLAR_API_KEY` | shared | `secrets-env` | delivered by /etc/faas/secrets/meterd/billing.env (meterd) and /etc/faas/sealed.env (apid) |
| `FAAS_POLAR_BASE_URL` | shared | `secrets-env` | delivered by /etc/faas/secrets/meterd/billing.env (meterd) and /etc/faas/sealed.env (apid) |
| `FAAS_POLAR_HOBBY_PRODUCT_ID` | shared | `secrets-env` | delivered by /etc/faas/secrets/meterd/billing.env (meterd) and /etc/faas/sealed.env (apid) |
| `FAAS_POLAR_METER_ID` | shared | `secrets-env` | delivered by /etc/faas/secrets/meterd/billing.env (meterd) and /etc/faas/sealed.env (apid) |
| `FAAS_POLAR_PRO_PRODUCT_ID` | shared | `secrets-env` | delivered by /etc/faas/secrets/meterd/billing.env (meterd) and /etc/faas/sealed.env (apid) |
| `FAAS_POLAR_RETURN_URL` | shared | `secrets-env` | delivered by /etc/faas/secrets/meterd/billing.env (meterd) and /etc/faas/sealed.env (apid) |
| `FAAS_POLAR_SANDBOX` | shared | `secrets-env` | delivered by /etc/faas/secrets/meterd/billing.env (meterd) and /etc/faas/sealed.env (apid) |
| `FAAS_POLAR_SCALE_PRODUCT_ID` | shared | `secrets-env` | delivered by /etc/faas/secrets/meterd/billing.env (meterd) and /etc/faas/sealed.env (apid) |
| `FAAS_POLAR_SUCCESS_URL` | shared | `secrets-env` | delivered by /etc/faas/secrets/meterd/billing.env (meterd) and /etc/faas/sealed.env (apid) |
| `FAAS_POLAR_USAGE_EVENT_NAME` | shared | `secrets-env` | delivered by /etc/faas/secrets/meterd/billing.env (meterd) and /etc/faas/sealed.env (apid) |
| `FAAS_POLAR_WEBHOOK_SECRET` | shared | `secrets-env` | delivered by /etc/faas/secrets/meterd/billing.env (meterd) and /etc/faas/sealed.env (apid) |
| `FAAS_POLAR_WEBHOOK_TOLERANCE_SECONDS` | shared | `default` |  |
| `FAAS_PREPARED_NETWORKS` | vmmd | `default` | opt-in unused-network cache size (0–16), disabled by default; ADR-149 |
| `FAAS_PRESSURE_MIGRATION_POLICY` | schedd | `default` |  |
| `FAAS_PRESSURE_REASSESSMENT_SECONDS` | schedd | `default` |  |
| `FAAS_PRESSURE_THRESHOLD_PER_MIN` | schedd | `default` |  |
| `FAAS_PREVIEW_JANITOR_INTERVAL_SECONDS` | apid | `default` |  |
| `FAAS_PREVIEW_JANITOR_STARTUP_DELAY_SECONDS` | apid | `default` |  |
| `FAAS_PROMETHEUS_URL` | apid, meterd | `default` |  |
| `FAAS_PUBLIC_CONTROL_ADDR` | gatewayd-public, shared | `unit` |  |
| `FAAS_PUBLIC_LISTEN_ADDR` | gatewayd-public | `envfile` |  |
| `FAAS_QUOTA_INTERVAL` | meterd | `default` |  |
| `FAAS_REBALANCE_COOLDOWN_SECONDS` | schedd | `default` |  |
| `FAAS_REBALANCE_MAX_PER_TICK` | schedd | `default` |  |
| `FAAS_RECONCILE_INTERVAL` | meterd | `default` |  |
| `FAAS_RECOVERY_HMAC_KEY_FILE` | apid | `default` |  |
| `FAAS_REGION` | meterd | `default` |  |
| `FAAS_REKEY_ENABLED` | apid | `runtime-config` |  |
| `FAAS_REKEY_PROGRESS_FILE` | apid | `default` |  |
| `FAAS_REQUEST_TELEMETRY_ENABLED` | apid, gatewayd-internal | `default` |  |
| `FAAS_REQUIRE_SHARED_ARTIFACTS` | shared | `envfile` |  |
| `FAAS_RESIDENCY_INTERVAL` | meterd | `default` |  |
| `FAAS_RETENTION_INTERVAL` | meterd | `default` |  |
| `FAAS_ROLLUP_INTERVAL` | meterd | `default` |  |
| `FAAS_RUNTIME_KIND` | guest | `guest` |  |
| `FAAS_SAFEDEPLOY_STUCK_AFTER` | apid, meterd | `default` |  |
| `FAAS_SAFEDEPLOY_TOKEN` | meterd | `secrets-env` | delivered by /etc/faas/secrets/meterd/billing.env (meterd) and /etc/faas/sealed.env (apid); Safe Deploy activation requires this and FAAS_CANARY_PROGRESSION_TOKEN together |
| `FAAS_SAMPLE_INTERVAL` | meterd | `default` |  |
| `FAAS_SBOM_ROOT` | apid | `default` |  |
| `FAAS_SCAN_SPOOL_ROOT` | apid | `default` |  |
| `FAAS_SCHEDD_ADDR` | meterd | `default` |  |
| `FAAS_SCHEDD_CONFIG` | schedd | `default` |  |
| `FAAS_SCHEDD_INVOCATION_DISPATCH_CONCURRENCY` | schedd | `default` |  |
| `FAAS_SCHEDD_ROLE` | schedd, shared | `dropin` |  |
| `FAAS_SCHEDD_SOCKET` | gatewayd-internal | `dropin` |  |
| `FAAS_SESSION_KEY` | apid, gatewayd-internal, shared | `unit` | LoadCredential= path form in faas-apid.service and faas-gatewayd-internal.service |
| `FAAS_SIGN_KEY` | imaged | `default` |  |
| `FAAS_SIGN_PUB` | schedd | `default` |  |
| `FAAS_SKIP_PG_TESTS` | shared | `dev-only` | must never be set on a production host |
| `FAAS_SKIP_SOCKET_GROUP` | shared | `dev-only` | must never be set on a production host |
| `FAAS_SNAPSHOT_FANOUT_INTERVAL` | vmmd | `default` |  |
| `FAAS_SPOOL_ROOT` | apid | `default` |  |
| `FAAS_STANDBY_WARMUP_ENABLED` | gatewayd-public | `default` |  |
| `FAAS_STANDBY_WARMUP_INTERVAL_MS` | gatewayd-public | `default` |  |
| `FAAS_STANDBY_WARMUP_SLUGS_PATH` | gatewayd-public | `default` |  |
| `FAAS_STATIC_EGRESS_IP_ENABLED` | shared | `default` |  |
| `FAAS_STATUSPAGE_PATH` | apid, shared | `unit` |  |
| `FAAS_STORAGE_BACKEND` | builderd, imaged, vmmd, shared | `envfile` |  |
| `FAAS_STORAGE_CACHE_DIR` | imaged, shared | `envfile` |  |
| `FAAS_STORAGE_CACHE_MAX_BYTES` | shared | `envfile` |  |
| `FAAS_STORAGE_CACHE_REFRESH` | shared | `default` |  |
| `FAAS_STORAGE_CACHE_SERVE_STALE` | shared | `envfile` |  |
| `FAAS_STORAGE_LOCAL_PREFIXES` | shared | `envfile` |  |
| `FAAS_STORAGE_ROLLUP_INTERVAL` | meterd | `default` |  |
| `FAAS_STORAGE_ROOT` | imaged, vmmd, shared | `default` |  |
| `FAAS_STREAM_BRIDGE_PERSISTENT` | shared | `default` |  |
| `FAAS_STREAM_BRIDGE_VERSION` | shared | `default` | rollback lever, see docs/ops/h2c-rollback.md |
| `FAAS_STRIPE_INTERVAL` | meterd | `default` |  |
| `FAAS_SYFT_BIN` | imaged | `default` |  |
| `FAAS_TAIL_PIPE_PATH` | guest | `guest` |  |
| `FAAS_TAIL_WAIT_SEC` | guest | `guest` |  |
| `FAAS_TENANT_SURFACES_ENABLED` | apid, shared | `runtime-config` |  |
| `FAAS_TEST_BUILDER_BASE_PATH` | shared | `dev-only` | must never be set on a production host |
| `FAAS_TEST_BUILDER_BASE_REF` | shared | `dev-only` | must never be set on a production host |
| `FAAS_TEST_DEPLOY_BASE_REF` | imaged, shared | `dev-only` | must never be set on a production host |
| `FAAS_TEST_KERNEL` | shared | `dev-only` | must never be set on a production host |
| `FAAS_TLS_CONTACT_EMAIL` | shared | `default` |  |
| `FAAS_TLS_CUTOVER_STATE_FILE` | apid, shared | `default` | optional operator override for the durable issue #252 TLS cutover state path |
| `FAAS_TLS_DIR` | vmmd | `default` |  |
| `FAAS_TLS_DNS_PROVIDER` | shared | `default` |  |
| `FAAS_TLS_DNS_TOKEN` | gatewayd-internal | `secrets-env` | delivered by /etc/faas/secrets/gatewayd-internal/gatewayd-internal.env (gatewayd-internal) |
| `FAAS_TLS_STAGING` | shared | `dev-only` | must never be set on a production host |
| `FAAS_TLS_STORAGE_DIR` | shared | `default` |  |
| `FAAS_TOKEN` | shared | `client` | read by the CLI/SDK on the operator's machine, never by a daemon |
| `FAAS_TRACE_OBSERVER_TOKEN` | shared | `default` |  |
| `FAAS_TRACE_RING_CAP` | shared | `default` |  |
| `FAAS_TRUSTED_PUBLISHERS_DIR` | apid, imaged | `default` |  |
| `FAAS_UPSTREAM_AFFINITY` | schedd | `default` | off by design until the §9.A rollout gate (spec) |
| `FAAS_UPSTREAM_AFFINITY_TTL` | schedd | `default` |  |
| `FAAS_UPSTREAM_PROBE` | meterd | `default` | off by design until the §9.A rollout gate (spec) |
| `FAAS_UPSTREAM_PROBE_INTERVAL` | meterd | `default` |  |
| `FAAS_UPSTREAM_PROBE_PARTITION_INTERVAL` | meterd | `default` |  |
| `FAAS_VCPU_BUDGET` | vmmd | `default` |  |
| `FAAS_VMMD_CONFIG` | vmmd | `default` |  |
| `FAAS_VMMD_DBURL` | vmmd | `envfile` |  |
| `FAAS_VMMD_LISTEN_ADDR` | vmmd | `dropin` |  |
| `FAAS_VMMD_LOG_ARCHIVE_SPOOL_ROOT` | vmmd, shared | `default` | optional compute-side spool override; defaults to /var/log/faas/vmmd-archive so vmmd and apid do not share active files on single-box hosts |
| `FAAS_VMMD_NODE_KEY_PATH` | vmmd | `default` |  |
| `FAAS_VMMD_RAW_BRIDGE_PATH` | shared | `default` |  |
| `FAAS_VMMD_ROLE` | vmmd, shared | `dropin` |  |
| `FAAS_VMMD_SCHEDD_TARGET` | vmmd | `dropin` |  |
| `FAAS_VMMD_STREAM_BRIDGE_PATH` | shared | `default` |  |
| `FAAS_VMMD_TARGET_URL` | vmmd | `dropin` |  |
| `FAAS_VMM_SOCK` | imaged | `dropin` |  |
| `FAAS_VMM_TLS_CA_PATH` | imaged | `dropin` |  |
| `FAAS_VMM_TLS_CERT_PATH` | imaged | `dropin` |  |
| `FAAS_VMM_TLS_KEY_PATH` | imaged | `dropin` |  |
| `FAAS_WEBHOOK_SECRET` | gatewayd-internal, githubd | `secrets-env` | deprecated fallback delivered by /etc/faas/secrets/gatewayd-internal/gatewayd-internal.env and /etc/faas/secrets/githubd/githubd.env |
| `FAAS_WORKFLOWS_ENABLED` | schedd | `unit` | explicit 0 in faas-schedd.service; set to 1 to activate durable workflow dispatch |
