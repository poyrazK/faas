// Package sealdomain is the registry of seal domains (ADR 20261022111812).
//
// A sealed value is sealed to the domain of the service that reads it. Each
// domain is an age X25519 identity with a fixed list of reader daemons and
// the node roles that stage it. Writers hold only public recipients, so the
// daemon that seals a customer's AI provider key (apid) cannot open it, and a
// node that never runs the reader never holds the identity.
//
// A domain is "wired" once its identity is loaded by its readers and its
// writers seal to it. Unwired domains are still sealed to the shared fleet
// identity (ADR-178); their rows in this registry say where they are going.
package sealdomain

import (
	"fmt"
	"sort"
)

// Domain names one seal domain. The name is part of identity file names,
// credential names and probe rows, so it never changes once shipped.
type Domain string

const (
	Runtime  Domain = "runtime"
	Outbound Domain = "outbound"
	Build    Domain = "build"
	Source   Domain = "source"
	Edge     Domain = "edge"
	Alerting Domain = "alerting"
	Storage  Domain = "storage"
	Delivery Domain = "delivery"
	Control  Domain = "control"
	Account  Domain = "account"

	// Dedicated identities that predate the registry (ADR-1737, ADR
	// 20261016151947). They are listed so every sealed column has an owner.
	Mail             Domain = "mail"
	MailVerification Domain = "mail-verification"
	LiveQuery        Domain = "livequery"
)

// Node roles, as pkg/daemonunitspec names them.
const (
	RoleControlPlane = "control-plane"
	RoleCompute      = "compute-only"
)

// Column is one sealed column.
type Column struct {
	Table string
	Name  string
}

func (c Column) String() string { return c.Table + "." + c.Name }

// Spec describes one domain.
type Spec struct {
	Domain Domain
	// Readers are the daemons that open the domain's envelopes.
	Readers []string
	// Roles are the node roles whose nodes stage the private identity.
	Roles []string
	// Columns are the columns whose envelopes belong to the domain.
	Columns []Column
	// Wired reports that the domain has its own identity in this release.
	Wired bool
	// Dedicated marks an identity that predates the registry and never
	// used the fleet identity.
	Dedicated bool
	// File is a dedicated domain's identity path; empty means
	// IdentityPath(DefaultDir, Domain).
	File string
}

// IdentityFile is where nodes stage the domain's private identity.
func (s Spec) IdentityFile() string {
	if s.File != "" {
		return s.File
	}
	return IdentityPath(DefaultDir, s.Domain)
}

func cols(table string, names ...string) []Column {
	out := make([]Column, 0, len(names))
	for _, n := range names {
		out = append(out, Column{Table: table, Name: n})
	}
	return out
}

func join(groups ...[]Column) []Column {
	var out []Column
	for _, g := range groups {
		out = append(out, g...)
	}
	return out
}

var cpOnly = []string{RoleControlPlane}
var computeOnly = []string{RoleCompute}
var bothRoles = []string{RoleControlPlane, RoleCompute}

var registry = []Spec{
	{Domain: Runtime, Readers: []string{"vmmd"}, Roles: computeOnly, Wired: true, Columns: join(
		cols("app_secrets", "ciphertext"),
		cols("project_environment_secrets", "ciphertext"),
		cols("app_config_guard_secrets", "ciphertext"),
		cols("managed_postgres_cutover_credentials", "ciphertext"),
		cols("deployment_release_secret_snapshots", "values_json"),
		cols("project_release_secret_bundles", "values_json"),
	)},
	{Domain: Outbound, Readers: []string{"outboundd"}, Roles: cpOnly, Wired: true, Columns: join(
		cols("outbound_integration_credentials", "authorization_sealed"),
		cols("outbound_integration_credential_keys", "authorization_sealed"),
	)},
	{Domain: Build, Readers: []string{"imaged"}, Roles: computeOnly, Columns: join(
		cols("app_registry_credentials", "password_encrypted"),
		cols("job_registry_credentials", "password_encrypted"),
	)},
	{Domain: Source, Readers: []string{"githubd"}, Roles: cpOnly, Columns: join(
		cols("github_installations", "sealed_install_token"),
		cols("github_webhook_secrets", "secret_sealed"),
	)},
	{Domain: Edge, Readers: []string{"gatewayd-internal", "gatewayd-public"}, Roles: bothRoles, Columns: join(
		cols("apps", "public_auth_basic"),
		cols("app_log_drains", "auth_header_sealed"),
		cols("application_standard_log_destinations", "auth_header_sealed"),
	)},
	{Domain: Alerting, Readers: []string{"meterd"}, Roles: cpOnly, Columns: join(
		cols("alert_rules", "webhook_secret_sealed"),
		cols("notification_channels", "target_sealed"),
		cols("mcp_readiness_pagerduty_policies", "integration_key_sealed"),
		cols("mcp_readiness_fallback_policies", "integration_key_sealed", "webhook_secret_sealed"),
	)},
	{Domain: Storage, Readers: []string{"s3-gatewayd"}, Roles: cpOnly, Columns: join(
		cols("object_storage_s3_credentials", "secret_sealed"),
	)},
	{Domain: Delivery, Readers: []string{"schedd"}, Roles: bothRoles, Columns: join(
		cols("execution_payloads", "sealed_payload"),
		cols("agent_execution_workflows", "sealed_plan"),
		cols("app_webhooks", "secret_sealed"),
		cols("commit_sources", "sealed_connection"),
	)},
	{Domain: Control, Readers: []string{"schedd"}, Roles: bothRoles, Columns: join(
		cols("cluster_signing_keys", "sealed_blob"),
	)},
	{Domain: Account, Readers: []string{"apid", "apid-clone-worker", "apid-data-pipeline-worker"}, Roles: cpOnly, Columns: join(
		cols("accounts", "mfa_secret_encrypted"),
		cols("account_billing_connections", "sealed_secret"),
		cols("inbound_webhook_endpoints", "signing_secret_sealed"),
		cols("gitlab_oauth_connections", "credentials_sealed"),
		cols("issue_slack_installations", "token_sealed"),
		cols("issue_slack_interaction_policies", "secret_sealed"),
		cols("issue_slack_oauth_flows", "sealed"),
		cols("issue_team_slack_destinations", "token_sealed"),
		cols("issue_tracker_linear_connections", "sealed_credential"),
		cols("issue_tracker_linear_oauth_flows", "sealed_state"),
		cols("managed_realtime_endpoints", "auth_token_sealed", "auth_token_previous_sealed", "callback_auth_token_sealed"),
		cols("managed_realtime_push_devices", "sealed"),
		cols("managed_realtime_push_providers", "sealed"),
		cols("mcp_readiness_pagerduty_acknowledgements", "signing_secret_sealed"),
		cols("data_pipeline_alerts", "ciphertext"),
		cols("data_pipeline_connections", "ciphertext"),
		cols("product_control_email_deliveries", "recipient_sealed", "unsubscribe_sealed"),
		cols("product_control_email_subscriptions", "grant_sealed", "pause_recipient_sealed"),
		cols("project_environment_clone_checkpoint_pg_connection_values", "ciphertext"),
		cols("project_environment_clone_checkpoint_postgres_credentials", "ciphertext"),
		cols("project_environment_clone_postgres_checkpoint_selections", "ciphertext"),
		cols("project_environment_clone_postgres_contents", "ciphertext"),
		cols("project_environment_clone_postgres_database_plans", "ciphertext"),
		cols("project_environment_clone_postgres_database_sql_pins", "ciphertext"),
		cols("project_environment_clone_postgres_inventories", "ciphertext"),
		cols("project_environment_clone_postgres_membership_plans", "ciphertext"),
		cols("project_environment_clone_postgres_role_plans", "ciphertext"),
		cols("project_environment_clone_postgres_target_sql_pins", "ciphertext"),
		cols("project_environment_clone_postgres_verification_attempts", "ciphertext"),
		cols("project_environment_clone_postgres_verifications", "ciphertext"),
	)},
	{Domain: Mail, Readers: []string{"maild"}, Roles: cpOnly, Dedicated: true, File: "/etc/faas/secrets/internal-mail/mail.age", Columns: join(
		cols("internal_mail_dkim_keys", "sealed_key"),
		cols("internal_mail_domains", "sealed_key"),
		cols("internal_mail_messages", "sealed_payload"),
	)},
	{Domain: MailVerification, Readers: []string{"apid"}, Roles: cpOnly, Dedicated: true, File: "/etc/faas/secrets/internal-mail/producer-verification.age", Columns: join(
		cols("platform_mail_producer_outbox", "sealed_security_alert", "sealed_verification"),
	)},
	{Domain: LiveQuery, Readers: []string{"livequeryd"}, Roles: cpOnly, Dedicated: true, File: "/etc/faas/secrets/livequeryd/keys.age", Columns: join(
		cols("managed_postgres_live_keys", "sealed_secret"),
		cols("managed_postgres_live_logins", "sealed_login"),
	)},
}

// ProbeColumns hold non-secret probes, each sealed to the domain it proves
// (ADR-178's fleet probe, and one row per domain in seal_domain_probe).
var ProbeColumns = []Column{
	{Table: "fleet_seal_domain_probe", Name: "sealed_blob"},
	{Table: "seal_domain_probe", Name: "sealed_blob"},
}

// PlaintextSecretColumns hold secrets that are not sealed yet. Each has a
// sealed replacement being rolled out and is dropped in a later release.
var PlaintextSecretColumns = []Column{
	{Table: "github_webhook_secrets", Name: "secret_value"},
}

// All returns every domain in registry order.
func All() []Spec {
	out := make([]Spec, len(registry))
	copy(out, registry)
	return out
}

// Lookup returns the spec of d.
func Lookup(d Domain) (Spec, bool) {
	for _, s := range registry {
		if s.Domain == d {
			return s, true
		}
	}
	return Spec{}, false
}

// Parse validates a domain name from a flag or config value.
func Parse(name string) (Domain, error) {
	if _, ok := Lookup(Domain(name)); !ok {
		return "", fmt.Errorf("sealdomain: unknown domain %q", name)
	}
	return Domain(name), nil
}

// ColumnDomain returns the domain that owns table.column.
func ColumnDomain(table, column string) (Domain, bool) {
	for _, s := range registry {
		for _, c := range s.Columns {
			if c.Table == table && c.Name == column {
				return s.Domain, true
			}
		}
	}
	return "", false
}

// ReaderDomains returns the domains a daemon opens, sorted by name.
func ReaderDomains(daemon string) []Domain {
	var out []Domain
	for _, s := range registry {
		for _, r := range s.Readers {
			if r == daemon {
				out = append(out, s.Domain)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
