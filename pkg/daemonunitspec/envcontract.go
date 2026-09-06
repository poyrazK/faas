package daemonunitspec

// EnvContract is the single registry of every FAAS_* environment variable
// a platform daemon (or a deploy-side script) reads, together with HOW a
// production host is expected to deliver it. It exists because three
// separate production outages in 2026-09 (function runner paths, the
// apid AppErrors listener, and the gateway streaming/jobs/geo gates)
// shared one root cause: code read a variable that no deploy path set,
// and nothing could notice. See ADR-143.
//
// The tests in envcontract_test.go enforce the contract in both
// directions:
//
//   - every `"FAAS_..."` literal read by daemon code must be declared here;
//   - every entry whose Source promises delivery (unit / dropin / envfile /
//     secrets-env / runtime-config) must actually be wired by that path;
//   - every FAAS_* the deploy tree sets must be read by something (no
//     dead config);
//   - docs/ops/env-contract.md is generated from this table.
//
// Adding a variable to code without adding it here fails CI with the
// exact instruction to add. Choose the Source honestly: "default" means
// the code default is production-correct and NO deploy path needs to set
// it. If a feature is meant to be on in production, it must not be
// "default" with an off default.

// EnvSource describes who is responsible for delivering a variable.
type EnvSource string

const (
	// EnvSourceUnit — Environment= in the daemon's pkg/daemonunitspec unit.
	EnvSourceUnit EnvSource = "unit"
	// EnvSourceDropin — a systemd drop-in rendered by an ansible role
	// (deploy/ansible/roles/*/templates/*.conf.j2) or a task.
	EnvSourceDropin EnvSource = "dropin"
	// EnvSourceEnvFile — an EnvironmentFile staged by ansible, node_join,
	// or the manifest renderer (compute-db.env, storage.env,
	// runtime-bases.env, ...).
	EnvSourceEnvFile EnvSource = "envfile"
	// EnvSourceSecretsEnv — an operator-provisioned secret EnvironmentFile
	// under /etc/faas/secrets/<daemon>/ or /etc/faas/sealed.env. The Note
	// MUST name the file.
	EnvSourceSecretsEnv EnvSource = "secrets-env"
	// EnvSourceRuntimeConfig — a DB-backed runtime-config key; the env is
	// only the boot-time default (cmd/apid/runtime_config.go).
	EnvSourceRuntimeConfig EnvSource = "runtime-config"
	// EnvSourceDefault — optional; the code default is production-correct
	// and no deploy path sets it.
	EnvSourceDefault EnvSource = "default"
	// EnvSourceScript — consumed by a deploy/ script, not a daemon.
	EnvSourceScript EnvSource = "script"
	// EnvSourceInternal — set by a daemon for its own subprocess.
	EnvSourceInternal EnvSource = "internal"
	// EnvSourceClient — read by the CLI/SDK on the operator's machine.
	EnvSourceClient EnvSource = "client"
	// EnvSourceDevOnly — tests, e2e, Lima. Must never be set in production.
	EnvSourceDevOnly EnvSource = "dev-only"
	// EnvSourceGuest — read by guest-init inside the microVM, delivered by
	// vmmd through the boot manifest, never by host deploy.
	EnvSourceGuest EnvSource = "guest"
)

// EnvVar is one row of the contract.
type EnvVar struct {
	// Name is the exact environment variable name. A trailing "_" marks a
	// prefix the code completes at runtime (e.g. FAAS_DEPLOY_BASE_REF_<RT>).
	Name string
	// Owners are the Registry daemon names that read it directly, plus
	// "shared" for pkg/ readers and "guest" for guest-init.
	Owners []string
	Source EnvSource
	// Note is free text shown in docs/ops/env-contract.md. Mandatory for
	// EnvSourceSecretsEnv (must name the delivering file).
	Note string
}

// EnvContract is sorted by Name; TestEnvContract_Sorted pins it.
var EnvContract = []EnvVar{
	{Name: "FAAS_ACCOUNT_SPEND_AGGREGATOR_INTERVAL", Owners: []string{"meterd"}, Source: EnvSourceDefault},
	{Name: "FAAS_ADMIN_EMAILS", Owners: []string{"apid"}, Source: EnvSourceSecretsEnv, Note: "delivered by /etc/faas/sealed.env (apid, operator-provisioned via `gregalectl secrets init`)"},
	{Name: "FAAS_ALERT_EVAL_INTERVAL", Owners: []string{"meterd"}, Source: EnvSourceDefault},
	{Name: "FAAS_APID_ADVISORY_SOCK", Owners: []string{"apid", "vmmd", "shared"}, Source: EnvSourceUnit},
	{Name: "FAAS_APID_APP_ERRORS_SOCKET", Owners: []string{"apid", "gatewayd-internal"}, Source: EnvSourceDefault},
	{Name: "FAAS_APID_APP_ERRORS_TARGET", Owners: []string{"apid", "gatewayd-internal"}, Source: EnvSourceDropin},
	{Name: "FAAS_APID_APP_ERRORS_TLS_CA_PATH", Owners: []string{"apid", "gatewayd-internal"}, Source: EnvSourceDropin},
	{Name: "FAAS_APID_APP_ERRORS_TLS_CERT_PATH", Owners: []string{"apid", "gatewayd-internal"}, Source: EnvSourceDropin},
	{Name: "FAAS_APID_APP_ERRORS_TLS_KEY_PATH", Owners: []string{"apid", "gatewayd-internal"}, Source: EnvSourceDropin},
	{Name: "FAAS_APID_AUTH_SOCKET", Owners: []string{"gatewayd-public"}, Source: EnvSourceDefault},
	{Name: "FAAS_APID_BASE_URL", Owners: []string{"meterd"}, Source: EnvSourceDefault},
	{Name: "FAAS_APID_GITHUBD_BRIDGE_SOCK", Owners: []string{"apid", "githubd"}, Source: EnvSourceDefault},
	{Name: "FAAS_APID_LISTEN", Owners: []string{"apid"}, Source: EnvSourceDefault},
	{Name: "FAAS_APID_LOOPBACK", Owners: []string{"gatewayd-internal"}, Source: EnvSourceDefault},
	{Name: "FAAS_APID_METRICS_ADDR", Owners: []string{"apid"}, Source: EnvSourceDefault},
	{Name: "FAAS_APID_OTEL_SPANS_WRITER_SOCKET", Owners: []string{"gatewayd-public"}, Source: EnvSourceDefault},
	{Name: "FAAS_APID_REQUEST_IDLE_TIMEOUT", Owners: []string{"apid"}, Source: EnvSourceDefault},
	{Name: "FAAS_APID_REQUEST_MAX_HEADER_BYTES", Owners: []string{"apid"}, Source: EnvSourceDefault},
	{Name: "FAAS_APID_REQUEST_READ_TIMEOUT", Owners: []string{"apid"}, Source: EnvSourceDefault},
	{Name: "FAAS_APID_REQUEST_TELEMETRY_SOCKET", Owners: []string{"gatewayd-internal"}, Source: EnvSourceDefault},
	{Name: "FAAS_APID_REQUEST_TELEMETRY_TARGET", Owners: []string{"gatewayd-internal"}, Source: EnvSourceDropin},
	{Name: "FAAS_APID_REQUEST_WRITE_TIMEOUT", Owners: []string{"apid"}, Source: EnvSourceDefault},
	{Name: "FAAS_APID_ROLE", Owners: []string{"apid", "shared"}, Source: EnvSourceDropin},
	{Name: "FAAS_API_CONTRACT_DIFF_ENABLED", Owners: []string{"shared"}, Source: EnvSourceDefault},
	{Name: "FAAS_API_HOSTING_SMOKE_URL", Owners: []string{"imaged"}, Source: EnvSourceDefault, Note: "optional public origin for post-readiness API hosting smoke verification"},
	{Name: "FAAS_APPS_DOMAIN", Owners: []string{"apid", "gatewayd-internal", "imaged", "shared"}, Source: EnvSourceEnvFile},
	{Name: "FAAS_APPS_ROOT", Owners: []string{"imaged", "shared"}, Source: EnvSourceDefault},
	{Name: "FAAS_APP_ERRORS_ENABLED", Owners: []string{"apid", "gatewayd-internal"}, Source: EnvSourceRuntimeConfig},
	{Name: "FAAS_ARTIFACT_REPLICATOR", Owners: []string{"imaged"}, Source: EnvSourceEnvFile},
	{Name: "FAAS_ARTIFACT_SYNC_TARGET", Owners: []string{"imaged"}, Source: EnvSourceScript, Note: "consumed by deploy/scripts/faas-artifact-replicator.sh via /etc/faas/artifact-sync.env"},
	{Name: "FAAS_ARTIFACT_SYNC_USER", Owners: []string{"imaged"}, Source: EnvSourceScript, Note: "consumed by deploy/scripts/faas-artifact-replicator.sh via /etc/faas/artifact-sync.env"},
	{Name: "FAAS_AUDIT_HMAC_KEY", Owners: []string{"apid"}, Source: EnvSourceSecretsEnv, Note: "delivered by /etc/faas/sealed.env (apid, operator-provisioned via `gregalectl secrets init`)"},
	{Name: "FAAS_AUDIT_HMAC_KEY_FILE", Owners: []string{"apid"}, Source: EnvSourceDefault},
	{Name: "FAAS_AUTH_RPC_ENABLED", Owners: []string{"apid"}, Source: EnvSourceDefault},
	{Name: "FAAS_BASE_EXTRACT_ROOT", Owners: []string{"shared"}, Source: EnvSourceUnit},
	{Name: "FAAS_BASE_STAGING_ROOT", Owners: []string{"shared"}, Source: EnvSourceUnit},
	{Name: "FAAS_BASE_TMP_ROOT", Owners: []string{"shared"}, Source: EnvSourceUnit},
	{Name: "FAAS_BILLING_PORTAL_URL", Owners: []string{"apid"}, Source: EnvSourceSecretsEnv, Note: "delivered by /etc/faas/secrets/meterd/billing.env (meterd) and /etc/faas/sealed.env (apid)"},
	{Name: "FAAS_BILLING_PROVIDER", Owners: []string{"shared"}, Source: EnvSourceSecretsEnv, Note: "delivered by /etc/faas/secrets/meterd/billing.env (meterd) and /etc/faas/sealed.env (apid)"},
	{Name: "FAAS_BRIDGE_HEADERS", Owners: []string{"vmmd-stream-bridge"}, Source: EnvSourceInternal, Note: "set by vmmd for the per-request stream-bridge subprocess"},
	{Name: "FAAS_BRIDGE_HOST", Owners: []string{"vmmd-stream-bridge"}, Source: EnvSourceInternal, Note: "set by vmmd for the per-request stream-bridge subprocess"},
	{Name: "FAAS_BRIDGE_METHOD", Owners: []string{"vmmd-stream-bridge"}, Source: EnvSourceInternal, Note: "set by vmmd for the per-request stream-bridge subprocess"},
	{Name: "FAAS_BRIDGE_PROTOCOL", Owners: []string{"vmmd-stream-bridge", "shared"}, Source: EnvSourceDefault, Note: "rollback lever, see docs/ops/h2c-rollback.md"},
	{Name: "FAAS_BRIDGE_URL", Owners: []string{"vmmd-stream-bridge"}, Source: EnvSourceInternal, Note: "set by vmmd for the per-request stream-bridge subprocess"},
	{Name: "FAAS_BROKER_EGRESS_IFNAME", Owners: []string{"schedd"}, Source: EnvSourceDefault},
	{Name: "FAAS_BROKER_EGRESS_MBIT", Owners: []string{"schedd"}, Source: EnvSourceDefault},
	{Name: "FAAS_BUILDERD_CONFIG", Owners: []string{"builderd", "shared"}, Source: EnvSourceUnit},
	{Name: "FAAS_BUILDERD_ROLE", Owners: []string{"builderd", "shared"}, Source: EnvSourceDropin},
	{Name: "FAAS_BUILDER_BASE_PATH", Owners: []string{"imaged", "shared"}, Source: EnvSourceDefault},
	{Name: "FAAS_BUILDER_BASE_REF", Owners: []string{"imaged"}, Source: EnvSourceDropin},
	{Name: "FAAS_CANARY_PROGRESSION_TOKEN", Owners: []string{"meterd"}, Source: EnvSourceSecretsEnv, Note: "delivered by /etc/faas/secrets/meterd/billing.env (meterd) and /etc/faas/sealed.env (apid); Safe Deploy activation requires this and FAAS_SAFEDEPLOY_TOKEN together"},
	{Name: "FAAS_CERT_EXPIRY_REFRESHER_INTERVAL", Owners: []string{"meterd"}, Source: EnvSourceDefault},
	{Name: "FAAS_CLI_AUTH_URL_BASE", Owners: []string{"apid"}, Source: EnvSourceDefault},
	{Name: "FAAS_COMPLETION_CACHE_PATH", Owners: []string{"shared"}, Source: EnvSourceClient, Note: "read by the CLI/SDK on the operator's machine, never by a daemon"},
	{Name: "FAAS_COMPUTE_GATEWAY_DISCOVERY", Owners: []string{"gatewayd-public", "shared"}, Source: EnvSourceUnit},
	{Name: "FAAS_CONTROL_PLANE_API_TARGET", Owners: []string{"gatewayd-public", "shared"}, Source: EnvSourceUnit},
	{Name: "FAAS_DATABASE_URL", Owners: []string{"shared"}, Source: EnvSourceDefault, Note: "DATABASE_URL from compute-db.env is the production DSN; this is the legacy alias"},
	{Name: "FAAS_DATA_PLACEMENT", Owners: []string{"apid"}, Source: EnvSourceRuntimeConfig},
	{Name: "FAAS_DEAD_NODE_RECONCILER_INTERVAL_SECONDS", Owners: []string{"schedd"}, Source: EnvSourceDefault},
	{Name: "FAAS_DEAD_NODE_RECONCILER_STALENESS_SECONDS", Owners: []string{"schedd"}, Source: EnvSourceDefault},
	{Name: "FAAS_DEPLOY_BASE_REF", Owners: []string{"imaged", "shared"}, Source: EnvSourceEnvFile},
	{Name: "FAAS_DEPLOY_BASE_REF_DEBIAN_PARENT", Owners: []string{"shared"}, Source: EnvSourceDefault},
	{Name: "FAAS_DEPLOY_BASE_REF_GO124", Owners: []string{"shared"}, Source: EnvSourceEnvFile},
	{Name: "FAAS_DEPLOY_BASE_REF_GO124_ALPINE", Owners: []string{"shared"}, Source: EnvSourceEnvFile},
	{Name: "FAAS_DEPLOY_BASE_REF_MINIMAL", Owners: []string{"shared"}, Source: EnvSourceEnvFile},
	{Name: "FAAS_DEPLOY_BASE_REF_NODE22", Owners: []string{"shared"}, Source: EnvSourceEnvFile},
	{Name: "FAAS_DEPLOY_BASE_REF_NODE24", Owners: []string{"shared"}, Source: EnvSourceEnvFile},
	{Name: "FAAS_DEPLOY_BASE_REF_PYTHON312", Owners: []string{"shared"}, Source: EnvSourceEnvFile},
	{Name: "FAAS_DEPLOY_BASE_REF_PYTHON313", Owners: []string{"shared"}, Source: EnvSourceEnvFile},
	{Name: "FAAS_DEV", Owners: []string{"shared"}, Source: EnvSourceDevOnly, Note: "must never be set on a production host"},
	{Name: "FAAS_DEV_TOKEN", Owners: []string{"apid"}, Source: EnvSourceDevOnly, Note: "must never be set on a production host"},
	{Name: "FAAS_DNS_API_URL", Owners: []string{"gatewayd-public"}, Source: EnvSourceDefault},
	{Name: "FAAS_DNS_PROVIDER", Owners: []string{"gatewayd-public", "shared"}, Source: EnvSourceDefault},
	{Name: "FAAS_DNS_PROVIDER_SEALED", Owners: []string{"gatewayd-public"}, Source: EnvSourceDefault},
	{Name: "FAAS_DNS_ZONE", Owners: []string{"gatewayd-public"}, Source: EnvSourceDefault},
	{Name: "FAAS_DOMAIN_DOCTOR_ENABLED", Owners: []string{"apid", "shared"}, Source: EnvSourceRuntimeConfig},
	{Name: "FAAS_DOMAIN_DOCTOR_TTL_SECONDS", Owners: []string{"apid"}, Source: EnvSourceRuntimeConfig},
	{Name: "FAAS_DPA_PATH", Owners: []string{"apid"}, Source: EnvSourceDefault},
	{Name: "FAAS_DUNNING_INTERVAL", Owners: []string{"meterd"}, Source: EnvSourceDefault},
	{Name: "FAAS_EGRESS_ALLOW_LOOPBACK", Owners: []string{"shared"}, Source: EnvSourceDevOnly, Note: "must never be set on a production host"},
	{Name: "FAAS_EGRESS_SOCKET", Owners: []string{"shared"}, Source: EnvSourceDropin},
	{Name: "FAAS_FLOOR_INTERVAL_SECONDS", Owners: []string{"schedd"}, Source: EnvSourceDefault},
	{Name: "FAAS_FUNCTION_RUNNER_GO124", Owners: []string{"imaged", "shared"}, Source: EnvSourceUnit},
	{Name: "FAAS_FUNCTION_RUNNER_GO124_ALPINE", Owners: []string{"imaged", "shared"}, Source: EnvSourceUnit},
	{Name: "FAAS_FUNCTION_RUNNER_NODE22", Owners: []string{"imaged", "shared"}, Source: EnvSourceUnit},
	{Name: "FAAS_FUNCTION_RUNNER_NODE24", Owners: []string{"imaged", "shared"}, Source: EnvSourceUnit},
	{Name: "FAAS_FUNCTION_RUNNER_PYTHON312", Owners: []string{"imaged", "shared"}, Source: EnvSourceUnit},
	{Name: "FAAS_FUNCTION_RUNNER_PYTHON313", Owners: []string{"imaged", "shared"}, Source: EnvSourceUnit},
	{Name: "FAAS_GATEWAYD_CONFIG", Owners: []string{"gatewayd-internal"}, Source: EnvSourceDropin},
	{Name: "FAAS_GATEWAYD_CONTROL_URL", Owners: []string{"apid"}, Source: EnvSourceDefault},
	{Name: "FAAS_GATEWAYD_PUBLIC_ROLE", Owners: []string{"gatewayd-public", "shared"}, Source: EnvSourceDropin},
	{Name: "FAAS_GATEWAYD_ROLE", Owners: []string{"gatewayd-internal", "shared"}, Source: EnvSourceDropin},
	{Name: "FAAS_GATEWAY_CONTROL_LISTEN", Owners: []string{"gatewayd-internal"}, Source: EnvSourceDefault},
	{Name: "FAAS_GATEWAY_EGRESS_SOCKET", Owners: []string{"shared"}, Source: EnvSourceDefault},
	{Name: "FAAS_GATEWAY_LISTEN", Owners: []string{"gatewayd-internal", "shared"}, Source: EnvSourceUnit},
	{Name: "FAAS_GATEWAY_METRICS_URL", Owners: []string{"schedd"}, Source: EnvSourceDropin},
	{Name: "FAAS_GATEWAY_RAW_STREAM_ENABLED", Owners: []string{"gatewayd-internal"}, Source: EnvSourceDefault},
	{Name: "FAAS_GATEWAY_ROUTE_METRICS", Owners: []string{"gatewayd-internal"}, Source: EnvSourceDefault},
	{Name: "FAAS_GATEWAY_STREAMING", Owners: []string{"gatewayd-internal"}, Source: EnvSourceDefault, Note: "emergency override only; production enables streaming via streaming_enabled=true in gatewayd-internal.toml (ADR-143)"},
	{Name: "FAAS_GATEWAY_SYNTH_SOCKET", Owners: []string{"gatewayd-internal"}, Source: EnvSourceDefault},
	{Name: "FAAS_GATEWAY_SYNTH_TARGET", Owners: []string{"schedd"}, Source: EnvSourceDropin},
	{Name: "FAAS_GC_INTERVAL", Owners: []string{"imaged"}, Source: EnvSourceDefault},
	{Name: "FAAS_GEOIP_AUTO_REFRESH", Owners: []string{"gatewayd-internal"}, Source: EnvSourceDefault, Note: "0; the geoip role owns refresh through re-bootstrap"},
	{Name: "FAAS_GEOIP_DB_PATH", Owners: []string{"gatewayd-internal"}, Source: EnvSourceDefault, Note: "the geoip role stages the DB-IP database at the code default (ADR-143); geo edge rules are no-ops without it"},
	{Name: "FAAS_GITHUBD_LOOPBACK", Owners: []string{"gatewayd-internal"}, Source: EnvSourceDefault},
	{Name: "FAAS_GITHUBD_ROLE", Owners: []string{"githubd", "shared"}, Source: EnvSourceDropin},
	{Name: "FAAS_GITHUBD_SOCKET", Owners: []string{"apid"}, Source: EnvSourceDefault},
	{Name: "FAAS_GITHUBD_WORK_DIR", Owners: []string{"apid", "githubd"}, Source: EnvSourceDefault},
	{Name: "FAAS_GITHUB_APP_CLIENT_ID", Owners: []string{"apid", "githubd"}, Source: EnvSourceSecretsEnv, Note: "delivered by /etc/faas/secrets/githubd/githubd.env (githubd) and /etc/faas/sealed.env (apid)"},
	{Name: "FAAS_GITHUB_APP_CLIENT_SECRET", Owners: []string{"githubd"}, Source: EnvSourceSecretsEnv, Note: "delivered by /etc/faas/secrets/githubd/githubd.env (githubd) and /etc/faas/sealed.env (apid)"},
	{Name: "FAAS_GITHUB_APP_ID", Owners: []string{"githubd"}, Source: EnvSourceSecretsEnv, Note: "delivered by /etc/faas/secrets/githubd/githubd.env (githubd) and /etc/faas/sealed.env (apid)"},
	{Name: "FAAS_GITHUB_APP_INSTALL_URL", Owners: []string{"apid"}, Source: EnvSourceSecretsEnv, Note: "delivered by /etc/faas/secrets/githubd/githubd.env (githubd) and /etc/faas/sealed.env (apid)"},
	{Name: "FAAS_GITHUB_APP_KEY_PATH", Owners: []string{"githubd", "shared"}, Source: EnvSourceUnit},
	{Name: "FAAS_GITHUB_APP_REDIRECT_URI", Owners: []string{"apid"}, Source: EnvSourceSecretsEnv, Note: "delivered by /etc/faas/secrets/githubd/githubd.env (githubd) and /etc/faas/sealed.env (apid)"},
	{Name: "FAAS_GITHUB_WEBHOOK_SECRET", Owners: []string{"gatewayd-internal", "githubd"}, Source: EnvSourceSecretsEnv, Note: "the same GitHub App webhook secret is delivered by /etc/faas/secrets/gatewayd-internal/gatewayd-internal.env and /etc/faas/secrets/githubd/githubd.env"},
	{Name: "FAAS_GRACE_INTERVAL", Owners: []string{"apid"}, Source: EnvSourceDefault},
	{Name: "FAAS_GRYPE_BIN", Owners: []string{"imaged"}, Source: EnvSourceDefault},
	{Name: "FAAS_GUEST_INIT", Owners: []string{"imaged", "shared"}, Source: EnvSourceDropin},
	{Name: "FAAS_HOST_AGE_IDENTITY_PATH", Owners: []string{"apid", "githubd", "imaged", "meterd", "shared"}, Source: EnvSourceUnit},
	{Name: "FAAS_HOST_AGE_KEY", Owners: []string{"githubd"}, Source: EnvSourceDefault},
	{Name: "FAAS_HOST_AGE_PUB", Owners: []string{"githubd"}, Source: EnvSourceDefault},
	{Name: "FAAS_HOST_AGE_RECIPIENT_PATH", Owners: []string{"apid", "vmmd", "shared"}, Source: EnvSourceUnit},
	{Name: "FAAS_HOST_BRIDGE_CIDR", Owners: []string{"vmmd"}, Source: EnvSourceDefault},
	{Name: "FAAS_HOST_HMAC_KEY_PATH", Owners: []string{"apid", "shared"}, Source: EnvSourceUnit},
	{Name: "FAAS_HOST_KEY_PATH", Owners: []string{"gatewayd-internal", "gatewayd-public", "vmmd"}, Source: EnvSourceDefault},
	{Name: "FAAS_HSTS_ENABLED", Owners: []string{"apid", "shared"}, Source: EnvSourceRuntimeConfig},
	{Name: "FAAS_IMAGED_METRICS_ADDR", Owners: []string{"imaged"}, Source: EnvSourceDefault},
	{Name: "FAAS_IMAGED_NODE_NAME", Owners: []string{"shared"}, Source: EnvSourceDefault},
	{Name: "FAAS_IMAGED_ROLE", Owners: []string{"imaged", "shared"}, Source: EnvSourceDropin},
	{Name: "FAAS_INTERNAL_H2C", Owners: []string{"gatewayd-public"}, Source: EnvSourceDefault},
	{Name: "FAAS_INTERNAL_SOCKET", Owners: []string{"gatewayd-public"}, Source: EnvSourceDefault},
	{Name: "FAAS_INTERNAL_SVC_KEY_PATH", Owners: []string{"schedd"}, Source: EnvSourceDefault},
	{Name: "FAAS_INTERNAL_SVC_KEY_SEALED_BLOB", Owners: []string{"schedd"}, Source: EnvSourceSecretsEnv, Note: "delivered by /etc/faas/secrets/schedd/schedd.env (schedd)"},
	{Name: "FAAS_INTERNAL_SVC_KEY_SEALED_NAMESPACE", Owners: []string{"schedd"}, Source: EnvSourceDefault},
	{Name: "FAAS_INTERNAL_SVC_PUBKEYS", Owners: []string{"gatewayd-internal"}, Source: EnvSourceDefault},
	{Name: "FAAS_INTERNAL_TARGET", Owners: []string{"gatewayd-public", "shared"}, Source: EnvSourceUnit},
	{Name: "FAAS_JOB", Owners: []string{"guest"}, Source: EnvSourceGuest},
	{Name: "FAAS_JOBS_DISPATCH", Owners: []string{"schedd"}, Source: EnvSourceUnit, Note: "explicit 0 in faas-schedd.service until the vmmd job RPC ships (Mega-1.5); jobs would otherwise sit pending silently"},
	{Name: "FAAS_LEADER_REDIRECT_TLS_CA", Owners: []string{"gatewayd-internal"}, Source: EnvSourceDefault},
	{Name: "FAAS_LEADER_REDIRECT_TLS_CERT", Owners: []string{"gatewayd-internal"}, Source: EnvSourceDefault},
	{Name: "FAAS_LEADER_REDIRECT_TLS_KEY", Owners: []string{"gatewayd-internal"}, Source: EnvSourceDefault},
	{Name: "FAAS_LOG_ARCHIVE_BUCKET", Owners: []string{"shared"}, Source: EnvSourceDefault},
	{Name: "FAAS_LOG_ARCHIVE_CREDS_PATH", Owners: []string{"shared"}, Source: EnvSourceUnit},
	{Name: "FAAS_LOG_ARCHIVE_ENDPOINT", Owners: []string{"shared"}, Source: EnvSourceDefault},
	{Name: "FAAS_LOG_ARCHIVE_INTERVAL", Owners: []string{"shared"}, Source: EnvSourceDefault},
	{Name: "FAAS_LOG_ARCHIVE_KEY_ID", Owners: []string{"shared"}, Source: EnvSourceDefault},
	{Name: "FAAS_LOG_ARCHIVE_LOCAL_BYTES_MAX", Owners: []string{"shared"}, Source: EnvSourceDefault},
	{Name: "FAAS_LOG_ARCHIVE_REGION", Owners: []string{"shared"}, Source: EnvSourceDefault},
	{Name: "FAAS_LOG_ARCHIVE_RETENTION_DAYS", Owners: []string{"shared"}, Source: EnvSourceDefault},
	{Name: "FAAS_LOG_ARCHIVE_SECRET", Owners: []string{"shared"}, Source: EnvSourceDefault},
	{Name: "FAAS_LOG_ARCHIVE_SPOOL_ROOT", Owners: []string{"shared"}, Source: EnvSourceDefault},
	{Name: "FAAS_LOG_LEVEL", Owners: []string{"shared"}, Source: EnvSourceDefault, Note: "info; re-read on SIGHUP"},
	{Name: "FAAS_MAIL_FROM", Owners: []string{"shared"}, Source: EnvSourceSecretsEnv, Note: "delivered by /etc/faas/secrets/meterd/billing.env (meterd) and /etc/faas/sealed.env (apid)"},
	{Name: "FAAS_MAIL_POSTMARK_TOKEN", Owners: []string{"shared"}, Source: EnvSourceSecretsEnv, Note: "delivered by /etc/faas/secrets/meterd/billing.env (meterd) and /etc/faas/sealed.env (apid)"},
	{Name: "FAAS_MAIL_RESEND_API_KEY", Owners: []string{"shared"}, Source: EnvSourceSecretsEnv, Note: "delivered by /etc/faas/secrets/meterd/billing.env (meterd) and /etc/faas/sealed.env (apid)"},
	{Name: "FAAS_MAIL_RESEND_WEBHOOK_SECRET", Owners: []string{"apid"}, Source: EnvSourceSecretsEnv, Note: "delivered by /etc/faas/secrets/meterd/billing.env (meterd) and /etc/faas/sealed.env (apid)"},
	{Name: "FAAS_MAIL_TRANSPORT", Owners: []string{"apid", "meterd", "shared"}, Source: EnvSourceSecretsEnv, Note: "delivered by /etc/faas/secrets/meterd/billing.env (meterd) and /etc/faas/sealed.env (apid)"},
	{Name: "FAAS_MANAGED_POSTGRES_CONFIG", Owners: []string{"shared"}, Source: EnvSourceDefault, Note: "optional provider-registry JSON path; apid loads the dark-wired Neon adapter and reconciler, while the file's provisioning_enabled flag defaults false (ADR-155)"},
	{Name: "FAAS_MANIFEST_PATH", Owners: []string{"imaged"}, Source: EnvSourceDropin},
	{Name: "FAAS_METERD_ROLE", Owners: []string{"meterd", "shared"}, Source: EnvSourceDropin},
	{Name: "FAAS_MFA_RECOVERY_HMAC_KEY", Owners: []string{"apid"}, Source: EnvSourceSecretsEnv, Note: "delivered by /etc/faas/sealed.env (apid, operator-provisioned via `gregalectl secrets init`)"},
	{Name: "FAAS_MIGRATE_LIVE_LEASE_SECONDS", Owners: []string{"schedd"}, Source: EnvSourceDefault},
	{Name: "FAAS_MIGRATE_LIVE_MAX_PER_TICK", Owners: []string{"schedd"}, Source: EnvSourceDefault},
	{Name: "FAAS_MIGRATING_WATCHDOG_INTERVAL_SECONDS", Owners: []string{"schedd"}, Source: EnvSourceDefault},
	{Name: "FAAS_MIGRATING_WATCHDOG_TICK_LIMIT", Owners: []string{"schedd"}, Source: EnvSourceDefault},
	{Name: "FAAS_NODE_NAME", Owners: []string{"apid", "builderd", "gatewayd-internal", "gatewayd-public", "githubd", "imaged", "meterd", "schedd", "vmmd", "shared"}, Source: EnvSourceDropin},
	{Name: "FAAS_NODE_PUBLIC_IP", Owners: []string{"gatewayd-public"}, Source: EnvSourceDefault},
	{Name: "FAAS_NOTIFICATIONS_UNSUBSCRIBE_URL", Owners: []string{"meterd"}, Source: EnvSourceDefault},
	{Name: "FAAS_OBJECT_STORAGE_CONFIG", Owners: []string{"apid", "shared"}, Source: EnvSourceDefault, Note: "optional provider-registry JSON path; s3_enabled runtime config separately defaults off (docs/object-storage.md); no production activation is promised"},
	{Name: "FAAS_OCI_BLOB_CACHE_DIR", Owners: []string{"imaged"}, Source: EnvSourceDefault, Note: "defaults to <FAAS_STORAGE_CACHE_DIR>/oci-blobs for OCI-backed deployments; local-storage deployments may opt in explicitly"},
	{Name: "FAAS_OCI_BLOB_CACHE_MAX_BYTES", Owners: []string{"imaged"}, Source: EnvSourceDefault, Note: "8 GiB byte budget for the node-local OCI blob cache; override when sizing compute-node disks"},
	{Name: "FAAS_OCI_INSECURE", Owners: []string{"imaged"}, Source: EnvSourceDevOnly, Note: "must never be set on a production host"},
	{Name: "FAAS_OCI_PASSWORD", Owners: []string{"shared"}, Source: EnvSourceEnvFile},
	{Name: "FAAS_OCI_PULL_TIMEOUT_SECONDS", Owners: []string{"imaged"}, Source: EnvSourceDefault},
	{Name: "FAAS_OCI_REGISTRY", Owners: []string{"imaged", "vmmd", "shared"}, Source: EnvSourceEnvFile},
	{Name: "FAAS_OCI_REPO_PREFIX", Owners: []string{"shared"}, Source: EnvSourceEnvFile},
	{Name: "FAAS_OCI_TIMEOUT_SECONDS", Owners: []string{"shared"}, Source: EnvSourceEnvFile},
	{Name: "FAAS_OCI_USERNAME", Owners: []string{"shared"}, Source: EnvSourceEnvFile},
	{Name: "FAAS_OFF_HOST_BACKUP_RCLONE_CONFIG", Owners: []string{"postgres"}, Source: EnvSourceScript, Note: "LoadCredential= path on the postgresql@.service drop-in; consumed by the archive_command shell in the postgres role"},
	{Name: "FAAS_OTEL_FLUSH_INTERVAL", Owners: []string{"gatewayd-public"}, Source: EnvSourceDefault},
	{Name: "FAAS_OTEL_SPANS_WRITER_ENABLED", Owners: []string{"apid", "gatewayd-public"}, Source: EnvSourceDefault},
	{Name: "FAAS_OVERLAY_INTERFACE", Owners: []string{"vmmd"}, Source: EnvSourceDefault},
	{Name: "FAAS_PADDLE_API_KEY", Owners: []string{"shared"}, Source: EnvSourceSecretsEnv, Note: "delivered by /etc/faas/secrets/meterd/billing.env (meterd) and /etc/faas/sealed.env (apid)"},
	{Name: "FAAS_PADDLE_SANDBOX", Owners: []string{"shared"}, Source: EnvSourceSecretsEnv, Note: "delivered by /etc/faas/secrets/meterd/billing.env (meterd) and /etc/faas/sealed.env (apid)"},
	{Name: "FAAS_PADDLE_WEBHOOK_SECRET", Owners: []string{"shared"}, Source: EnvSourceSecretsEnv, Note: "delivered by /etc/faas/secrets/meterd/billing.env (meterd) and /etc/faas/sealed.env (apid)"},
	{Name: "FAAS_PADDLE_WEBHOOK_TOLERANCE_SECONDS", Owners: []string{"shared"}, Source: EnvSourceDefault},
	{Name: "FAAS_PERSISTENT_PROTOCOL_V1", Owners: []string{"guest"}, Source: EnvSourceGuest},
	{Name: "FAAS_PERSISTENT_WORKER", Owners: []string{"shared"}, Source: EnvSourceInternal, Note: "set by the builder path for its own worker process"},
	{Name: "FAAS_PGTEST_TEMPLATE_DATABASE", Owners: []string{"shared"}, Source: EnvSourceDevOnly, Note: "must never be set on a production host"},
	{Name: "FAAS_PLAN_CACHE_ROOT", Owners: []string{"apid"}, Source: EnvSourceDefault},
	{Name: "FAAS_POLAR_ACCESS_TOKEN", Owners: []string{"shared"}, Source: EnvSourceSecretsEnv, Note: "delivered by /etc/faas/secrets/meterd/billing.env (meterd) and /etc/faas/sealed.env (apid)"},
	{Name: "FAAS_POLAR_API_KEY", Owners: []string{"shared"}, Source: EnvSourceSecretsEnv, Note: "delivered by /etc/faas/secrets/meterd/billing.env (meterd) and /etc/faas/sealed.env (apid)"},
	{Name: "FAAS_POLAR_BASE_URL", Owners: []string{"shared"}, Source: EnvSourceSecretsEnv, Note: "delivered by /etc/faas/secrets/meterd/billing.env (meterd) and /etc/faas/sealed.env (apid)"},
	{Name: "FAAS_POLAR_HOBBY_PRODUCT_ID", Owners: []string{"shared"}, Source: EnvSourceSecretsEnv, Note: "delivered by /etc/faas/secrets/meterd/billing.env (meterd) and /etc/faas/sealed.env (apid)"},
	{Name: "FAAS_POLAR_METER_ID", Owners: []string{"shared"}, Source: EnvSourceSecretsEnv, Note: "delivered by /etc/faas/secrets/meterd/billing.env (meterd) and /etc/faas/sealed.env (apid)"},
	{Name: "FAAS_POLAR_PRO_PRODUCT_ID", Owners: []string{"shared"}, Source: EnvSourceSecretsEnv, Note: "delivered by /etc/faas/secrets/meterd/billing.env (meterd) and /etc/faas/sealed.env (apid)"},
	{Name: "FAAS_POLAR_RETURN_URL", Owners: []string{"shared"}, Source: EnvSourceSecretsEnv, Note: "delivered by /etc/faas/secrets/meterd/billing.env (meterd) and /etc/faas/sealed.env (apid)"},
	{Name: "FAAS_POLAR_SANDBOX", Owners: []string{"shared"}, Source: EnvSourceSecretsEnv, Note: "delivered by /etc/faas/secrets/meterd/billing.env (meterd) and /etc/faas/sealed.env (apid)"},
	{Name: "FAAS_POLAR_SCALE_PRODUCT_ID", Owners: []string{"shared"}, Source: EnvSourceSecretsEnv, Note: "delivered by /etc/faas/secrets/meterd/billing.env (meterd) and /etc/faas/sealed.env (apid)"},
	{Name: "FAAS_POLAR_SUCCESS_URL", Owners: []string{"shared"}, Source: EnvSourceSecretsEnv, Note: "delivered by /etc/faas/secrets/meterd/billing.env (meterd) and /etc/faas/sealed.env (apid)"},
	{Name: "FAAS_POLAR_USAGE_EVENT_NAME", Owners: []string{"shared"}, Source: EnvSourceSecretsEnv, Note: "delivered by /etc/faas/secrets/meterd/billing.env (meterd) and /etc/faas/sealed.env (apid)"},
	{Name: "FAAS_POLAR_WEBHOOK_SECRET", Owners: []string{"shared"}, Source: EnvSourceSecretsEnv, Note: "delivered by /etc/faas/secrets/meterd/billing.env (meterd) and /etc/faas/sealed.env (apid)"},
	{Name: "FAAS_POLAR_WEBHOOK_TOLERANCE_SECONDS", Owners: []string{"shared"}, Source: EnvSourceDefault},
	{Name: "FAAS_PREPARED_NETWORKS", Owners: []string{"vmmd"}, Source: EnvSourceDefault, Note: "opt-in unused-network cache size (0–16), disabled by default; ADR-149"},
	{Name: "FAAS_PRESSURE_MIGRATION_POLICY", Owners: []string{"schedd"}, Source: EnvSourceDefault},
	{Name: "FAAS_PRESSURE_REASSESSMENT_SECONDS", Owners: []string{"schedd"}, Source: EnvSourceDefault},
	{Name: "FAAS_PRESSURE_THRESHOLD_PER_MIN", Owners: []string{"schedd"}, Source: EnvSourceDefault},
	{Name: "FAAS_PREVIEW_JANITOR_INTERVAL_SECONDS", Owners: []string{"apid"}, Source: EnvSourceDefault},
	{Name: "FAAS_PREVIEW_JANITOR_STARTUP_DELAY_SECONDS", Owners: []string{"apid"}, Source: EnvSourceDefault},
	{Name: "FAAS_PROMETHEUS_URL", Owners: []string{"apid", "meterd"}, Source: EnvSourceDefault},
	{Name: "FAAS_PUBLIC_CONTROL_ADDR", Owners: []string{"gatewayd-public", "shared"}, Source: EnvSourceUnit},
	{Name: "FAAS_PUBLIC_LISTEN_ADDR", Owners: []string{"gatewayd-public"}, Source: EnvSourceEnvFile},
	{Name: "FAAS_QUOTA_INTERVAL", Owners: []string{"meterd"}, Source: EnvSourceDefault},
	{Name: "FAAS_REBALANCE_COOLDOWN_SECONDS", Owners: []string{"schedd"}, Source: EnvSourceDefault},
	{Name: "FAAS_REBALANCE_MAX_PER_TICK", Owners: []string{"schedd"}, Source: EnvSourceDefault},
	{Name: "FAAS_RECONCILE_INTERVAL", Owners: []string{"meterd"}, Source: EnvSourceDefault},
	{Name: "FAAS_RECOVERY_HMAC_KEY_FILE", Owners: []string{"apid"}, Source: EnvSourceDefault},
	{Name: "FAAS_REGION", Owners: []string{"meterd"}, Source: EnvSourceDefault},
	{Name: "FAAS_REKEY_ENABLED", Owners: []string{"apid"}, Source: EnvSourceRuntimeConfig},
	{Name: "FAAS_REKEY_PROGRESS_FILE", Owners: []string{"apid"}, Source: EnvSourceDefault},
	{Name: "FAAS_REQUEST_TELEMETRY_ENABLED", Owners: []string{"apid", "gatewayd-internal"}, Source: EnvSourceDefault},
	{Name: "FAAS_REQUIRE_SHARED_ARTIFACTS", Owners: []string{"shared"}, Source: EnvSourceEnvFile},
	{Name: "FAAS_RESIDENCY_INTERVAL", Owners: []string{"meterd"}, Source: EnvSourceDefault},
	{Name: "FAAS_RETENTION_INTERVAL", Owners: []string{"meterd"}, Source: EnvSourceDefault},
	{Name: "FAAS_ROLLUP_INTERVAL", Owners: []string{"meterd"}, Source: EnvSourceDefault},
	{Name: "FAAS_RUNTIME_KIND", Owners: []string{"guest"}, Source: EnvSourceGuest},
	{Name: "FAAS_SAFEDEPLOY_STUCK_AFTER", Owners: []string{"apid", "meterd"}, Source: EnvSourceDefault},
	{Name: "FAAS_SAFEDEPLOY_TOKEN", Owners: []string{"meterd"}, Source: EnvSourceSecretsEnv, Note: "delivered by /etc/faas/secrets/meterd/billing.env (meterd) and /etc/faas/sealed.env (apid); Safe Deploy activation requires this and FAAS_CANARY_PROGRESSION_TOKEN together"},
	{Name: "FAAS_SAMPLE_INTERVAL", Owners: []string{"meterd"}, Source: EnvSourceDefault},
	{Name: "FAAS_SBOM_ROOT", Owners: []string{"apid"}, Source: EnvSourceDefault},
	{Name: "FAAS_SCAN_SPOOL_ROOT", Owners: []string{"apid"}, Source: EnvSourceDefault},
	{Name: "FAAS_SCHEDD_ADDR", Owners: []string{"meterd"}, Source: EnvSourceDefault},
	{Name: "FAAS_SCHEDD_CONFIG", Owners: []string{"schedd"}, Source: EnvSourceDefault},
	{Name: "FAAS_SCHEDD_INVOCATION_DISPATCH_CONCURRENCY", Owners: []string{"schedd"}, Source: EnvSourceDefault},
	{Name: "FAAS_SCHEDD_ROLE", Owners: []string{"schedd", "shared"}, Source: EnvSourceDropin},
	{Name: "FAAS_SCHEDD_SOCKET", Owners: []string{"gatewayd-internal"}, Source: EnvSourceDropin},
	{Name: "FAAS_SESSION_KEY", Owners: []string{"apid", "gatewayd-internal", "shared"}, Source: EnvSourceUnit, Note: "LoadCredential= path form in faas-apid.service and faas-gatewayd-internal.service"},
	{Name: "FAAS_SIGN_KEY", Owners: []string{"imaged"}, Source: EnvSourceDefault},
	{Name: "FAAS_SIGN_PUB", Owners: []string{"schedd"}, Source: EnvSourceDefault},
	{Name: "FAAS_SKIP_PG_TESTS", Owners: []string{"shared"}, Source: EnvSourceDevOnly, Note: "must never be set on a production host"},
	{Name: "FAAS_SKIP_SOCKET_GROUP", Owners: []string{"shared"}, Source: EnvSourceDevOnly, Note: "must never be set on a production host"},
	{Name: "FAAS_SNAPSHOT_FANOUT_INTERVAL", Owners: []string{"vmmd"}, Source: EnvSourceDefault},
	{Name: "FAAS_SPOOL_ROOT", Owners: []string{"apid"}, Source: EnvSourceDefault},
	{Name: "FAAS_STANDBY_WARMUP_ENABLED", Owners: []string{"gatewayd-public"}, Source: EnvSourceDefault},
	{Name: "FAAS_STANDBY_WARMUP_INTERVAL_MS", Owners: []string{"gatewayd-public"}, Source: EnvSourceDefault},
	{Name: "FAAS_STANDBY_WARMUP_SLUGS_PATH", Owners: []string{"gatewayd-public"}, Source: EnvSourceDefault},
	{Name: "FAAS_STATIC_EGRESS_IP_ENABLED", Owners: []string{"shared"}, Source: EnvSourceDefault},
	{Name: "FAAS_STATUSPAGE_PATH", Owners: []string{"apid", "shared"}, Source: EnvSourceUnit},
	{Name: "FAAS_STORAGE_BACKEND", Owners: []string{"builderd", "imaged", "vmmd", "shared"}, Source: EnvSourceEnvFile},
	{Name: "FAAS_STORAGE_CACHE_DIR", Owners: []string{"imaged", "shared"}, Source: EnvSourceEnvFile},
	{Name: "FAAS_STORAGE_CACHE_MAX_BYTES", Owners: []string{"shared"}, Source: EnvSourceEnvFile},
	{Name: "FAAS_STORAGE_CACHE_REFRESH", Owners: []string{"shared"}, Source: EnvSourceDefault},
	{Name: "FAAS_STORAGE_CACHE_SERVE_STALE", Owners: []string{"shared"}, Source: EnvSourceEnvFile},
	{Name: "FAAS_STORAGE_LOCAL_PREFIXES", Owners: []string{"shared"}, Source: EnvSourceEnvFile},
	{Name: "FAAS_STORAGE_ROLLUP_INTERVAL", Owners: []string{"meterd"}, Source: EnvSourceDefault},
	{Name: "FAAS_STORAGE_ROOT", Owners: []string{"imaged", "vmmd", "shared"}, Source: EnvSourceDefault},
	{Name: "FAAS_STREAM_BRIDGE_PERSISTENT", Owners: []string{"shared"}, Source: EnvSourceDefault},
	{Name: "FAAS_STREAM_BRIDGE_VERSION", Owners: []string{"shared"}, Source: EnvSourceDefault, Note: "rollback lever, see docs/ops/h2c-rollback.md"},
	{Name: "FAAS_STRIPE_INTERVAL", Owners: []string{"meterd"}, Source: EnvSourceDefault},
	{Name: "FAAS_SYFT_BIN", Owners: []string{"imaged"}, Source: EnvSourceDefault},
	{Name: "FAAS_TAIL_PIPE_PATH", Owners: []string{"guest"}, Source: EnvSourceGuest},
	{Name: "FAAS_TAIL_WAIT_SEC", Owners: []string{"guest"}, Source: EnvSourceGuest},
	{Name: "FAAS_TENANT_SURFACES_ENABLED", Owners: []string{"apid", "shared"}, Source: EnvSourceRuntimeConfig},
	{Name: "FAAS_TEST_BUILDER_BASE_PATH", Owners: []string{"shared"}, Source: EnvSourceDevOnly, Note: "must never be set on a production host"},
	{Name: "FAAS_TEST_BUILDER_BASE_REF", Owners: []string{"shared"}, Source: EnvSourceDevOnly, Note: "must never be set on a production host"},
	{Name: "FAAS_TEST_DEPLOY_BASE_REF", Owners: []string{"imaged", "shared"}, Source: EnvSourceDevOnly, Note: "must never be set on a production host"},
	{Name: "FAAS_TEST_KERNEL", Owners: []string{"shared"}, Source: EnvSourceDevOnly, Note: "must never be set on a production host"},
	{Name: "FAAS_TLS_CONTACT_EMAIL", Owners: []string{"shared"}, Source: EnvSourceDefault},
	{Name: "FAAS_TLS_CUTOVER_STATE_FILE", Owners: []string{"apid", "shared"}, Source: EnvSourceDefault, Note: "optional operator override for the durable issue #252 TLS cutover state path"},
	{Name: "FAAS_TLS_DIR", Owners: []string{"vmmd"}, Source: EnvSourceDefault},
	{Name: "FAAS_TLS_DNS_PROVIDER", Owners: []string{"shared"}, Source: EnvSourceDefault},
	{Name: "FAAS_TLS_DNS_TOKEN", Owners: []string{"gatewayd-internal"}, Source: EnvSourceSecretsEnv, Note: "delivered by /etc/faas/secrets/gatewayd-internal/gatewayd-internal.env (gatewayd-internal)"},
	{Name: "FAAS_TLS_STAGING", Owners: []string{"shared"}, Source: EnvSourceDevOnly, Note: "must never be set on a production host"},
	{Name: "FAAS_TLS_STORAGE_DIR", Owners: []string{"shared"}, Source: EnvSourceDefault},
	{Name: "FAAS_TOKEN", Owners: []string{"shared"}, Source: EnvSourceClient, Note: "read by the CLI/SDK on the operator's machine, never by a daemon"},
	{Name: "FAAS_TRACE_OBSERVER_TOKEN", Owners: []string{"shared"}, Source: EnvSourceDefault},
	{Name: "FAAS_TRACE_RING_CAP", Owners: []string{"shared"}, Source: EnvSourceDefault},
	{Name: "FAAS_TRUSTED_PUBLISHERS_DIR", Owners: []string{"apid", "imaged"}, Source: EnvSourceDefault},
	{Name: "FAAS_UPSTREAM_AFFINITY", Owners: []string{"schedd"}, Source: EnvSourceDefault, Note: "off by design until the §9.A rollout gate (spec)"},
	{Name: "FAAS_UPSTREAM_AFFINITY_TTL", Owners: []string{"schedd"}, Source: EnvSourceDefault},
	{Name: "FAAS_UPSTREAM_PROBE", Owners: []string{"meterd"}, Source: EnvSourceDefault, Note: "off by design until the §9.A rollout gate (spec)"},
	{Name: "FAAS_UPSTREAM_PROBE_INTERVAL", Owners: []string{"meterd"}, Source: EnvSourceDefault},
	{Name: "FAAS_UPSTREAM_PROBE_PARTITION_INTERVAL", Owners: []string{"meterd"}, Source: EnvSourceDefault},
	{Name: "FAAS_VCPU_BUDGET", Owners: []string{"vmmd"}, Source: EnvSourceDefault},
	{Name: "FAAS_VMMD_CONFIG", Owners: []string{"vmmd"}, Source: EnvSourceDefault},
	{Name: "FAAS_VMMD_DBURL", Owners: []string{"vmmd"}, Source: EnvSourceEnvFile},
	{Name: "FAAS_VMMD_LISTEN_ADDR", Owners: []string{"vmmd"}, Source: EnvSourceDropin},
	{Name: "FAAS_VMMD_LOG_ARCHIVE_SPOOL_ROOT", Owners: []string{"vmmd", "shared"}, Source: EnvSourceDefault, Note: "optional compute-side spool override; defaults to /var/log/faas/vmmd-archive so vmmd and apid do not share active files on single-box hosts"},
	{Name: "FAAS_VMMD_NODE_KEY_PATH", Owners: []string{"vmmd"}, Source: EnvSourceDefault},
	{Name: "FAAS_VMMD_RAW_BRIDGE_PATH", Owners: []string{"shared"}, Source: EnvSourceDefault},
	{Name: "FAAS_VMMD_ROLE", Owners: []string{"vmmd", "shared"}, Source: EnvSourceDropin},
	{Name: "FAAS_VMMD_SCHEDD_TARGET", Owners: []string{"vmmd"}, Source: EnvSourceDropin},
	{Name: "FAAS_VMMD_STREAM_BRIDGE_PATH", Owners: []string{"shared"}, Source: EnvSourceDefault},
	{Name: "FAAS_VMMD_TARGET_URL", Owners: []string{"vmmd"}, Source: EnvSourceDropin},
	{Name: "FAAS_VMM_SOCK", Owners: []string{"imaged"}, Source: EnvSourceDropin},
	{Name: "FAAS_VMM_TLS_CA_PATH", Owners: []string{"imaged"}, Source: EnvSourceDropin},
	{Name: "FAAS_VMM_TLS_CERT_PATH", Owners: []string{"imaged"}, Source: EnvSourceDropin},
	{Name: "FAAS_VMM_TLS_KEY_PATH", Owners: []string{"imaged"}, Source: EnvSourceDropin},
	{Name: "FAAS_WEBHOOK_SECRET", Owners: []string{"gatewayd-internal", "githubd"}, Source: EnvSourceSecretsEnv, Note: "deprecated fallback delivered by /etc/faas/secrets/gatewayd-internal/gatewayd-internal.env and /etc/faas/secrets/githubd/githubd.env"},
	{Name: "FAAS_WORKFLOWS_ENABLED", Owners: []string{"schedd"}, Source: EnvSourceUnit, Note: "explicit 0 in faas-schedd.service; set to 1 to activate durable workflow dispatch"},
}

// EnvContractByName indexes EnvContract by variable name.
func EnvContractByName() map[string]EnvVar {
	m := make(map[string]EnvVar, len(EnvContract))
	for _, v := range EnvContract {
		m[v.Name] = v
	}
	return m
}
