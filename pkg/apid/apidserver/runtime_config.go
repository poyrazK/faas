package apidserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/httpsec"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/runtimeconfig"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	runtimeConfigTenantSurfaces                     = "tenant_surfaces_enabled"
	runtimeConfigDomainDoctor                       = "domain_doctor_enabled"
	runtimeConfigDomainDoctorTTL                    = "domain_doctor_ttl_seconds"
	runtimeConfigDataPlacement                      = "data_placement_enabled"
	runtimeConfigAppErrors                          = "app_errors_enabled"
	runtimeConfigRekey                              = "rekey_enabled"
	runtimeConfigHSTS                               = "hsts_enabled"
	runtimeConfigS3                                 = "s3_enabled"
	runtimeConfigFrontendHosting                    = "frontend_hosting_enabled"
	runtimeConfigFrontendHostingDurable             = "frontend_hosting_durable_routing_enabled"
	runtimeConfigFrontendHostingConfig              = "frontend_hosting_config_enabled"
	runtimeConfigFrontendHostingPreviews            = "frontend_hosting_previews_enabled"
	runtimeConfigFrontendHostingHistory             = "frontend_hosting_history_enabled"
	runtimeConfigFrontendHostingRetention           = "frontend_hosting_retention_enabled"
	runtimeConfigFrontendHostingCancellation        = "frontend_hosting_cancellation_enabled"
	runtimeConfigFrontendHostingPreviewReservations = "frontend_hosting_preview_reservations_enabled"
	runtimeConfigFrontendHostingActivity            = "frontend_hosting_activity_enabled"
	runtimeConfigFrontendHostingCISetup             = "frontend_hosting_ci_setup_enabled"
	runtimeConfigFrontendHostingDiagnostics         = "frontend_hosting_diagnostics_enabled"
	runtimeConfigFrontendHostingOIDC                = "frontend_hosting_oidc_enabled"
	runtimeConfigFrontendHostingAutomation          = "frontend_hosting_automation_enabled"
	runtimeConfigFrontendHostingAnalytics           = "frontend_hosting_analytics_enabled"
	runtimeConfigFrontendHostingDomains             = "frontend_hosting_domains_enabled"
	runtimeConfigAPIMonetization                    = "api_monetization_enabled"
	runtimeConfigProductControlPlanPricing          = "product_control_plan_pricing_enabled"
	runtimeConfigPlatformReleasePause               = runtimeconfig.KeyPlatformReleasePause

	// Platform flags (ADR-1700) are catalogued from pkg/platformflags; their
	// values are a flag definition or the shared account groups.
	runtimeConfigKindPlatformFlag       = "platform_flag"
	runtimeConfigKindPlatformFlagGroups = "platform_flag_groups"
	runtimeConfigKindPlatformFlagOptIns = "platform_flag_opt_ins"
	runtimeConfigCategoryPlatformFlags  = "Platform flags"

	// pg_notify is deliberately only a wake-up signal. This repair interval
	// bounds convergence when a notification is lost during a database or
	// listener reconnect, while keeping the hot path independent of a polling
	// request from the operator console.
	runtimeConfigReconcileInterval = 5 * time.Second
)

type runtimeConfigDefinition struct {
	Key         string
	Label       string
	Description string
	Category    string
	Kind        string
	Default     json.RawMessage
	ApplyMode   state.RuntimeConfigApplyMode
	// ControllerEnabled is true only when this deployment has a live
	// consumer for the non-hot apply workflow. Hot settings use the same
	// field to advertise that their watcher/apply path is present.
	ControllerEnabled bool
	Mutable           bool
	Sensitive         bool
}

var runtimeConfigCatalog = []runtimeConfigDefinition{
	{Key: runtimeConfigFrontendHostingConfig, Label: "Internal hosting release configuration", Description: "Admit release redirects, response headers and custom 404 pages. Worker serving has a separate default-off switch.", Category: "Feature flags", Kind: "boolean", Default: json.RawMessage("false"), ApplyMode: state.RuntimeConfigApplyHot, ControllerEnabled: true, Mutable: true},
	{Key: runtimeConfigFrontendHostingPreviews, Label: "Internal hosting private previews", Description: "Admit immutable private release previews. Worker serving has a separate default-off switch.", Category: "Feature flags", Kind: "boolean", Default: json.RawMessage("false"), ApplyMode: state.RuntimeConfigApplyHot, ControllerEnabled: true, Mutable: true},
	{Key: runtimeConfigFrontendHostingHistory, Label: "Internal hosting release history", Description: "Admit caller-provided provenance, record accepted promotions transactionally, and expose bounded history.", Category: "Feature flags", Kind: "boolean", Default: json.RawMessage("false"), ApplyMode: state.RuntimeConfigApplyHot, ControllerEnabled: true, Mutable: true},
	{Key: runtimeConfigFrontendHostingRetention, Label: "Internal hosting retention", Description: "Opt in to per-site automatic retirement of abandoned uploads and expired never-published CI releases.", Category: "Feature flags", Kind: "boolean", Default: json.RawMessage("false"), ApplyMode: state.RuntimeConfigApplyHot, ControllerEnabled: true, Mutable: true},
	{Key: runtimeConfigFrontendHostingCancellation, Label: "Internal hosting CI cancellation", Description: "Cancel an exact owned CI run and queue safe resource cleanup; operator only.", Category: "Feature flags", Kind: "boolean", Default: json.RawMessage("false"), ApplyMode: state.RuntimeConfigApplyHot, ControllerEnabled: true, Mutable: true},
	{Key: runtimeConfigFrontendHostingPreviewReservations, Label: "Internal hosting preview reservations", Description: "Reserve preview capacity atomically at explicit CI admission and show stored capacity.", Category: "Feature flags", Kind: "boolean", Default: json.RawMessage("false"), ApplyMode: state.RuntimeConfigApplyHot, ControllerEnabled: true, Mutable: true},
	{Key: runtimeConfigFrontendHostingActivity, Label: "Internal hosting deployment activity", Description: "List bounded stored CI intent and release/preview evidence without mutations or provider probes.", Category: "Feature flags", Kind: "boolean", Default: json.RawMessage("false"), ApplyMode: state.RuntimeConfigApplyHot, ControllerEnabled: true, Mutable: true},
	{Key: runtimeConfigFrontendHostingCISetup, Label: "Internal hosting GitHub setup", Description: "Render and preflight reviewed deployment configuration without repository writes or deployments.", Category: "Feature flags", Kind: "boolean", Default: json.RawMessage("false"), ApplyMode: state.RuntimeConfigApplyHot, ControllerEnabled: true, Mutable: true},
	{Key: runtimeConfigFrontendHostingDiagnostics, Label: "Internal hosting diagnostics", Description: "Read stored site and CI evidence with safe recovery guidance. Does not probe or mutate providers.", Category: "Feature flags", Kind: "boolean", Default: json.RawMessage("false"), ApplyMode: state.RuntimeConfigApplyHot, ControllerEnabled: true, Mutable: true},
	{Key: runtimeConfigFrontendHostingOIDC, Label: "Internal hosting GitHub OIDC", Description: "Exchange explicitly trusted GitHub job identities for short-lived site staging and private preview credentials.", Category: "Feature flags", Kind: "boolean", Default: json.RawMessage("false"), ApplyMode: state.RuntimeConfigApplyHot, ControllerEnabled: true, Mutable: true},
	{Key: runtimeConfigFrontendHostingAutomation, Label: "Internal hosting automation", Description: "Admit one-site staging/preview credentials and durable CI runs. No production promotion authority.", Category: "Feature flags", Kind: "boolean", Default: json.RawMessage("false"), ApplyMode: state.RuntimeConfigApplyHot, ControllerEnabled: true, Mutable: true},
	{Key: runtimeConfigFrontendHostingAnalytics, Label: "Internal hosting traffic analytics", Description: "Read approximate provider traffic observations for owned internal sites. Worker collection has an independent default-off switch.", Category: "Feature flags", Kind: "boolean", Default: json.RawMessage("false"), ApplyMode: state.RuntimeConfigApplyHot, ControllerEnabled: true, Mutable: true},
	{Key: runtimeConfigFrontendHostingDomains, Label: "Internal hosting custom domains", Description: "Admit operator-only custom domain management. Existing domains continue safety reconciliation when disabled; the Worker has an independent serving switch.", Category: "Feature flags", Kind: "boolean", Default: json.RawMessage("false"), ApplyMode: state.RuntimeConfigApplyHot, ControllerEnabled: true, Mutable: true},
	{Key: runtimeConfigFrontendHosting, Label: "Internal frontend hosting", Description: "Enable operator-only static hosting management. Disabling expires leased routes within 120 seconds; durable sites continue serving. The independent Worker switch stops all traffic.", Category: "Feature flags", Kind: "boolean", Default: json.RawMessage("false"), ApplyMode: state.RuntimeConfigApplyHot, ControllerEnabled: true, Mutable: true},
	{Key: runtimeConfigFrontendHostingDurable, Label: "Internal durable frontend routing", Description: "Create new static sites with durable edge routing. Existing sites keep their routing mode. Disabling does not stop existing durable sites or their safety reconciliation; the Worker has a separate durable-routing switch.", Category: "Feature flags", Kind: "boolean", Default: json.RawMessage("false"), ApplyMode: state.RuntimeConfigApplyHot, ControllerEnabled: true, Mutable: true},
	{Key: runtimeConfigAPIMonetization, Label: "API monetization", Description: "Expose rate cards, plans, statements, invoice exports, billing runs and their enforcement for every account. To release it to some accounts only, leave this off and use the api_monetization platform flag. Disabled routes answer 404, background billing pauses and gateways stop enforcing monetization policy; stored data is kept.", Category: "Feature flags", Kind: "boolean", Default: json.RawMessage("false"), ApplyMode: state.RuntimeConfigApplyHot, ControllerEnabled: true, Mutable: true},
	{Key: runtimeConfigProductControlPlanPricing, Label: "Product Control plan pricing", Description: "Allow Product Control catalogs with plan fees or included starts (ADR-20261021154511). Enable only after every apid replica runs the release that prices them; disabling refuses new such catalogs and keeps published ones.", Category: "Feature flags", Kind: "boolean", Default: json.RawMessage("false"), ApplyMode: state.RuntimeConfigApplyHot, ControllerEnabled: true, Mutable: true},
	{Key: runtimeConfigS3, Label: "S3 object storage", Description: "Allow bucket provisioning and new signed URLs. Disabling preserves metadata and cleanup; existing URLs expire normally. Provider configuration is still required.", Category: "Feature flags", Kind: "boolean", Default: json.RawMessage("false"), ApplyMode: state.RuntimeConfigApplyHot, ControllerEnabled: true, Mutable: true},
	{Key: runtimeConfigTenantSurfaces, Label: "Tenant surfaces", Description: "Expose the tenant surface and certificate lifecycle API.", Category: "Feature flags", Kind: "boolean", Default: json.RawMessage("false"), ApplyMode: state.RuntimeConfigApplyHot, ControllerEnabled: true, Mutable: true},
	{Key: runtimeConfigDomainDoctor, Label: "Domain doctor", Description: "Run DNS and certificate readiness probes for customer domains.", Category: "Feature flags", Kind: "boolean", Default: json.RawMessage("true"), ApplyMode: state.RuntimeConfigApplyHot, ControllerEnabled: true, Mutable: true},
	{Key: runtimeConfigDomainDoctorTTL, Label: "Domain doctor TTL", Description: "Maximum age in seconds before a domain doctor result is stale.", Category: "Operational policies", Kind: "integer", Default: json.RawMessage("300"), ApplyMode: state.RuntimeConfigApplyHot, ControllerEnabled: true, Mutable: true},
	{Key: runtimeConfigDataPlacement, Label: "Data placement", Description: "Enable customer data-upstream placement and affinity behavior.", Category: "Feature flags", Kind: "boolean", Default: json.RawMessage("false"), ApplyMode: state.RuntimeConfigApplyHot, ControllerEnabled: true, Mutable: true},
	{Key: runtimeConfigAppErrors, Label: "Automatic app errors", Description: "Accept and aggregate gateway error reports; enabling the socket is a graceful daemon change.", Category: "Feature flags", Kind: "boolean", Default: json.RawMessage("true"), ApplyMode: state.RuntimeConfigApplyGraceful, ControllerEnabled: false, Mutable: true},
	{Key: runtimeConfigPlatformReleasePause, Label: "Break glass: pause releases", Description: "Refuse every new deployment and scheduled release start on the platform, and stop builders claiming queued builds (ADR 20261022140000). Running apps keep serving and rollbacks still work. For incident containment; set with gregalectl break-glass pause.", Category: "Security", Kind: "boolean", Default: json.RawMessage("false"), ApplyMode: state.RuntimeConfigApplyHot, ControllerEnabled: true, Mutable: true},
	{Key: runtimeConfigRekey, Label: "Background secret rekey", Description: "Run the background app-secret re-seal worker when host identities are ready; worker lifecycle is rolling.", Category: "Security", Kind: "boolean", Default: json.RawMessage("false"), ApplyMode: state.RuntimeConfigApplyRolling, ControllerEnabled: false, Mutable: true},
	{Key: runtimeConfigHSTS, Label: "Strict transport security", Description: "Emit the HSTS response header on the customer-facing API.", Category: "Security", Kind: "boolean", Default: json.RawMessage("true"), ApplyMode: state.RuntimeConfigApplyHot, ControllerEnabled: true, Mutable: true},
	{Key: "request_read_timeout", Label: "Request read timeout", Description: "HTTP request body read timeout.", Category: "HTTP listener", Kind: "duration", Default: json.RawMessage(`"60s"`), ApplyMode: state.RuntimeConfigApplyGraceful, ControllerEnabled: false, Mutable: true},
	{Key: "request_write_timeout", Label: "Request write timeout", Description: "HTTP response write timeout.", Category: "HTTP listener", Kind: "duration", Default: json.RawMessage(`"300s"`), ApplyMode: state.RuntimeConfigApplyGraceful, ControllerEnabled: false, Mutable: true},
	{Key: "request_idle_timeout", Label: "Request idle timeout", Description: "HTTP keep-alive idle timeout.", Category: "HTTP listener", Kind: "duration", Default: json.RawMessage(`"120s"`), ApplyMode: state.RuntimeConfigApplyGraceful, ControllerEnabled: false, Mutable: true},
	{Key: "listen_addr", Label: "API listen address", Description: "Bootstrap listener address; changes use a rolling listener transition.", Category: "Bootstrap", Kind: "string", Default: json.RawMessage(`"127.0.0.1:8081"`), ApplyMode: state.RuntimeConfigApplyRolling, Mutable: false},
	{Key: "metrics_addr", Label: "Metrics listen address", Description: "Bootstrap metrics listener address; changes use a rolling listener transition.", Category: "Bootstrap", Kind: "string", Default: json.RawMessage(`""`), ApplyMode: state.RuntimeConfigApplyRolling, Mutable: false},
	{Key: "billing_provider", Label: "Billing provider", Description: "Provider selection is changed through a rolling deployment with credential preflight.", Category: "Billing", Kind: "enum", Default: json.RawMessage(`"polar"`), ApplyMode: state.RuntimeConfigApplyRolling, Mutable: false},
	{Key: "db_url", Label: "Database endpoint", Description: "Database endpoint and credentials are bootstrap secrets and are never edited in the web console.", Category: "Bootstrap", Kind: "secret_reference", Default: json.RawMessage(`""`), ApplyMode: state.RuntimeConfigApplyBreakGlass, Mutable: false, Sensitive: true},
	{Key: "role", Label: "Daemon role", Description: "Daemon topology identity is deployment-managed.", Category: "Bootstrap", Kind: "enum", Default: json.RawMessage(`"single_box"`), ApplyMode: state.RuntimeConfigApplyBreakGlass, Mutable: false},
}

type runtimeConfigManager struct {
	mu       sync.RWMutex
	values   map[string]json.RawMessage
	versions map[string]int64
	defs     map[string]runtimeConfigDefinition
	catalog  []runtimeConfigDefinition
	getenv   func(string) string
	// flags is the compiled platform-flag view (ADR-1700). applyVersion
	// updates it under mu so flag decisions follow version order.
	flags *platformflags.Set
	// receiptDaemon, when set, makes this manager record apply receipts
	// (ADR-1700) as that daemon on receiptInstance. failedReceipts holds
	// the last version whose failure was recorded.
	receiptDaemon   string
	receiptInstance string
	failedReceipts  map[string]int64
}

// enableReceipts makes the manager report what it applies, as daemon on
// this host. Only the serving apid enables it; one-shot workers that share
// the manager would otherwise overwrite its receipts.
func (m *runtimeConfigManager) enableReceipts(daemon string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.receiptDaemon, m.receiptInstance = daemon, runtimeconfig.DefaultInstance()
}

func (m *runtimeConfigManager) version(key string) int64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.versions[key]
}

// recordReceipt reports an apply outcome for one durable row. A failure is
// recorded once per version so a value that keeps failing does not write on
// every repair pass. A failed receipt write is ignored: receipts are
// evidence, never a condition for serving, and the next version reports
// again.
func (m *runtimeConfigManager) recordReceipt(ctx context.Context, store state.Store, row state.RuntimeConfig, applyErr error) {
	m.mu.Lock()
	daemon, instance := m.receiptDaemon, m.receiptInstance
	if daemon == "" || store == nil {
		m.mu.Unlock()
		return
	}
	status, errText := state.RuntimeConfigReceiptApplied, ""
	if applyErr != nil {
		if m.failedReceipts == nil {
			m.failedReceipts = map[string]int64{}
		}
		if m.failedReceipts[row.Key] == row.Version {
			m.mu.Unlock()
			return
		}
		m.failedReceipts[row.Key] = row.Version
		status, errText = state.RuntimeConfigReceiptFailed, applyErr.Error()
	} else {
		delete(m.failedReceipts, row.Key)
	}
	m.mu.Unlock()
	_ = store.RecordRuntimeConfigApplyReceipt(ctx, state.RuntimeConfigApplyReceipt{
		Key: row.Key, Scope: row.Scope, ScopeID: row.ScopeID, Daemon: daemon, Instance: instance,
		Version: row.Version, Status: status, Error: errText,
	})
}

func newRuntimeConfigManager(getenv func(string) string) *runtimeConfigManager {
	if getenv == nil {
		getenv = os.Getenv
	}
	m := &runtimeConfigManager{
		values:   make(map[string]json.RawMessage),
		versions: make(map[string]int64),
		defs:     make(map[string]runtimeConfigDefinition),
		getenv:   getenv,
		flags:    platformflags.NewSet(nil),
	}
	m.catalog = append(append([]runtimeConfigDefinition(nil), runtimeConfigCatalog...), platformFlagDefinitions()...)
	for _, def := range m.catalog {
		m.defs[def.Key] = def
		m.values[def.Key] = runtimeConfigEnvDefault(def, getenv)
	}
	return m
}

// platformFlagDefinitions catalogues every platform flag, its self-serve
// opt-ins (ADR 20261021080640) and the shared groups row as hot runtime
// configuration, applied through the same versioned path as the other hot
// settings.
func platformFlagDefinitions() []runtimeConfigDefinition {
	specs := platformflags.Catalog()
	defs := make([]runtimeConfigDefinition, 0, 2*len(specs)+1)
	for _, spec := range specs {
		defs = append(defs, runtimeConfigDefinition{
			Key: platformflags.ConfigKey(spec.Key), Label: spec.Label, Description: spec.Description,
			Category: runtimeConfigCategoryPlatformFlags, Kind: runtimeConfigKindPlatformFlag,
			Default: spec.Default.Marshal(), ApplyMode: state.RuntimeConfigApplyHot, ControllerEnabled: true, Mutable: true,
		}, runtimeConfigDefinition{
			Key: platformflags.OptInsConfigKey(spec.Key), Label: spec.Label + " opt-ins",
			Description: "Accounts and organizations that joined the " + spec.Label + " preview themselves.",
			Category:    runtimeConfigCategoryPlatformFlags, Kind: runtimeConfigKindPlatformFlagOptIns,
			Default: json.RawMessage(`{}`), ApplyMode: state.RuntimeConfigApplyHot, ControllerEnabled: true, Mutable: true,
		})
	}
	return append(defs, runtimeConfigDefinition{
		Key: platformflags.GroupsConfigKey, Label: "Platform flag groups",
		Description: "Named account sets that platform-flag rules reference.",
		Category:    runtimeConfigCategoryPlatformFlags, Kind: runtimeConfigKindPlatformFlagGroups,
		Default: json.RawMessage(`{}`), ApplyMode: state.RuntimeConfigApplyHot, ControllerEnabled: true, Mutable: true,
	})
}

// PlatformFlags returns the compiled platform-flag holder.
func (m *runtimeConfigManager) PlatformFlags() *platformflags.Set {
	if m == nil {
		return nil
	}
	return m.flags
}

// seedPlatformFlag installs a boot-time fallback definition (version 0). It
// never replaces a durable operator value that has already been reconciled.
func (m *runtimeConfigManager) seedPlatformFlag(flagKey string, def platformflags.Definition) {
	if m == nil {
		return
	}
	_, _ = m.applyVersion(platformflags.ConfigKey(flagKey), def.Marshal(), 0)
}

func runtimeConfigEnvDefault(def runtimeConfigDefinition, getenv func(string) string) json.RawMessage {
	var envName string
	switch def.Key {
	case runtimeConfigFrontendHosting:
		envName = "FAAS_FRONTEND_HOSTING_ENABLED"
	case runtimeConfigFrontendHostingDurable:
		envName = "FAAS_FRONTEND_HOSTING_DURABLE_ROUTING_ENABLED"
	case runtimeConfigFrontendHostingRetention:
		envName = "FAAS_FRONTEND_HOSTING_RETENTION_ENABLED"
	case runtimeConfigFrontendHostingCancellation:
		envName = "FAAS_FRONTEND_HOSTING_CANCELLATION_ENABLED"
	case runtimeConfigFrontendHostingPreviewReservations:
		envName = "FAAS_FRONTEND_HOSTING_PREVIEW_RESERVATIONS_ENABLED"
	case runtimeConfigFrontendHostingActivity:
		envName = "FAAS_FRONTEND_HOSTING_ACTIVITY_ENABLED"
	case runtimeConfigFrontendHostingCISetup:
		envName = "FAAS_FRONTEND_HOSTING_CI_SETUP_ENABLED"
	case runtimeConfigFrontendHostingDiagnostics:
		envName = "FAAS_FRONTEND_HOSTING_DIAGNOSTICS_ENABLED"
	case runtimeConfigFrontendHostingOIDC:
		envName = "FAAS_FRONTEND_HOSTING_OIDC_ENABLED"
	case runtimeConfigFrontendHostingAutomation:
		envName = "FAAS_FRONTEND_HOSTING_AUTOMATION_ENABLED"
	case runtimeConfigFrontendHostingHistory:
		envName = "FAAS_FRONTEND_HOSTING_HISTORY_ENABLED"
	case runtimeConfigFrontendHostingPreviews:
		envName = "FAAS_FRONTEND_HOSTING_PREVIEWS_ENABLED"
	case runtimeConfigFrontendHostingConfig:
		envName = "FAAS_FRONTEND_HOSTING_CONFIG_ENABLED"
	case runtimeConfigFrontendHostingAnalytics:
		envName = "FAAS_FRONTEND_HOSTING_ANALYTICS_ENABLED"
	case runtimeConfigFrontendHostingDomains:
		envName = "FAAS_FRONTEND_HOSTING_DOMAINS_ENABLED"
	case runtimeConfigTenantSurfaces:
		envName = "FAAS_TENANT_SURFACES_ENABLED"
	case runtimeConfigAPIMonetization:
		envName = "FAAS_API_MONETIZATION_ENABLED"
	case runtimeConfigDomainDoctor:
		envName = "FAAS_DOMAIN_DOCTOR_ENABLED"
	case runtimeConfigDataPlacement:
		envName = "FAAS_DATA_PLACEMENT"
	case runtimeConfigAppErrors:
		envName = "FAAS_APP_ERRORS_ENABLED"
	case runtimeConfigRekey:
		envName = "FAAS_REKEY_ENABLED"
	case runtimeConfigHSTS:
		envName = "FAAS_HSTS_ENABLED"
	case runtimeConfigDomainDoctorTTL:
		envName = "FAAS_DOMAIN_DOCTOR_TTL_SECONDS"
	}
	if envName == "" {
		return append(json.RawMessage(nil), def.Default...)
	}
	raw := strings.ToLower(strings.TrimSpace(getenv(envName)))
	if raw == "" {
		return append(json.RawMessage(nil), def.Default...)
	}
	if def.Key == runtimeConfigDomainDoctor || def.Key == runtimeConfigAppErrors || def.Key == runtimeConfigHSTS {
		if raw == "0" || raw == "false" || raw == "no" || raw == "off" {
			return json.RawMessage("false")
		}
		return json.RawMessage("true")
	}
	if def.Key == runtimeConfigDomainDoctorTTL {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			value, _ := json.Marshal(n)
			return value
		}
		return append(json.RawMessage(nil), def.Default...)
	}
	for _, value := range []string{"1", "true", "yes", "on"} {
		if raw == value {
			return json.RawMessage("true")
		}
	}
	return json.RawMessage("false")
}

func (m *runtimeConfigManager) Definitions() []runtimeConfigDefinition {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]runtimeConfigDefinition, 0, len(m.catalog))
	for _, def := range m.catalog {
		def.Default = append(json.RawMessage(nil), def.Default...)
		out = append(out, def)
	}
	return out
}

func (m *runtimeConfigManager) Definition(key string) (runtimeConfigDefinition, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	def, ok := m.defs[key]
	return def, ok
}

func (m *runtimeConfigManager) Value(key string) json.RawMessage {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append(json.RawMessage(nil), m.values[key]...)
}

func (m *runtimeConfigManager) Bool(key string, fallback bool) bool {
	value := m.Value(key)
	var out bool
	if err := json.Unmarshal(value, &out); err != nil {
		return fallback
	}
	return out
}

func (m *runtimeConfigManager) Int(key string, fallback int) int {
	value := m.Value(key)
	var out int
	if err := json.Unmarshal(value, &out); err != nil {
		return fallback
	}
	return out
}

func (m *runtimeConfigManager) Duration(key string, fallback time.Duration) time.Duration {
	value := m.Value(key)
	var raw string
	if err := json.Unmarshal(value, &raw); err != nil {
		return fallback
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return fallback
	}
	return d
}

func (m *runtimeConfigManager) apply(key string, value json.RawMessage) error {
	_, err := m.applyVersion(key, value, 0)
	return err
}

// applyVersion installs a value only when it is at least as new as the
// manager's current durable version. A version of zero is the bootstrap
// environment fallback; it must not overwrite an operator value that has
// already been reconciled. The version check closes the race where two
// concurrent PATCH requests finish their database writes in one order but
// reach the local process in the opposite order.
func (m *runtimeConfigManager) applyVersion(key string, value json.RawMessage, version int64) (bool, error) {
	def, ok := m.Definition(key)
	if !ok {
		return false, fmt.Errorf("unknown runtime config key %q", key)
	}
	if err := validateRuntimeConfigValue(def, value); err != nil {
		return false, err
	}
	m.mu.Lock()
	if version == 0 && m.versions[key] > 0 {
		m.mu.Unlock()
		return false, nil
	}
	if version > 0 && version < m.versions[key] {
		m.mu.Unlock()
		return false, nil
	}
	if platformflags.IsConfigKey(key) {
		// Recompile the flag snapshot first so a value the set rejects never
		// becomes this manager's effective value.
		if err := m.flags.Apply(key, value, version); err != nil {
			m.mu.Unlock()
			return false, err
		}
	}
	m.values[key] = append(json.RawMessage(nil), value...)
	if version > m.versions[key] {
		m.versions[key] = version
	}
	if key == runtimeConfigHSTS {
		var enabled bool
		if err := json.Unmarshal(value, &enabled); err == nil {
			// Keep the package-level header switch ordered with the versioned
			// snapshot. Applying it while the manager lock is held prevents a
			// stale concurrent update from winning after a newer one.
			httpsec.SetHSTSEnabled(enabled)
		}
	}
	m.mu.Unlock()
	return true, nil
}

func (m *runtimeConfigManager) reconcile(ctx context.Context, store state.Store) error {
	rows, err := store.ListRuntimeConfigs(ctx, state.RuntimeConfigScopeGlobal, "")
	if err != nil {
		return err
	}
	var firstStoreErr error
	for _, row := range rows {
		// A non-hot row is only effective after its durable apply
		// operation reaches a terminal success. Pending/failed/blocked
		// desired values must not become live merely because apid restarted.
		if row.ApplyMode != state.RuntimeConfigApplyHot && row.Status != state.RuntimeConfigApplied {
			continue
		}
		previous := m.version(row.Key)
		applied, applyErr := m.applyVersion(row.Key, row.DesiredValue, row.Version)
		if applyErr != nil {
			m.recordReceipt(ctx, store, row, applyErr)
			// Invalid durable data must become visible as a failed setting,
			// not disappear into a retry loop. The process keeps serving on
			// its last valid snapshot, which preserves availability while
			// the operator repairs the value from the console.
			if row.ApplyMode == state.RuntimeConfigApplyHot {
				if err := store.MarkRuntimeConfigApplied(ctx, row.Key, row.Scope, row.ScopeID, row.Version, nil, applyErr.Error()); err != nil && !errors.Is(err, state.ErrRuntimeConfigConflict) && firstStoreErr == nil {
					firstStoreErr = err
				}
			}
			continue
		}
		if !applied {
			// A newer version is already live in this process. Do not
			// acknowledge this older row; the next durable read will carry
			// the current version and effective value.
			continue
		}
		if row.Version > previous {
			m.recordReceipt(ctx, store, row, nil)
		}
		if row.ApplyMode == state.RuntimeConfigApplyHot && row.Status != state.RuntimeConfigApplied {
			if err := store.MarkRuntimeConfigApplied(ctx, row.Key, row.Scope, row.ScopeID, row.Version, row.DesiredValue, ""); err != nil {
				if !errors.Is(err, state.ErrRuntimeConfigConflict) && firstStoreErr == nil {
					firstStoreErr = err
				}
			}
		}
	}
	return firstStoreErr
}

func validateRuntimeConfigValue(def runtimeConfigDefinition, value json.RawMessage) error {
	if !json.Valid(value) {
		return fmt.Errorf("%s must be valid JSON", def.Key)
	}
	switch def.Kind {
	case "boolean":
		var v bool
		if err := json.Unmarshal(value, &v); err != nil {
			return fmt.Errorf("%s must be boolean", def.Key)
		}
	case "integer":
		var v int
		if err := json.Unmarshal(value, &v); err != nil || v < 1 || v > 86400 {
			return fmt.Errorf("%s must be an integer between 1 and 86400", def.Key)
		}
	case "duration":
		var v string
		if err := json.Unmarshal(value, &v); err != nil {
			return fmt.Errorf("%s must be a duration string", def.Key)
		}
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 || d > 24*time.Hour {
			return fmt.Errorf("%s must be a positive duration no longer than 24h", def.Key)
		}
	case runtimeConfigKindPlatformFlag:
		if _, err := platformflags.ParseDefinition(value); err != nil {
			return fmt.Errorf("%s: %w", def.Key, err)
		}
	case runtimeConfigKindPlatformFlagGroups:
		if _, err := platformflags.ParseGroups(value); err != nil {
			return fmt.Errorf("%s: %w", def.Key, err)
		}
	case runtimeConfigKindPlatformFlagOptIns:
		if _, err := platformflags.ParseOptIns(value); err != nil {
			return fmt.Errorf("%s: %w", def.Key, err)
		}
	case "string", "enum", "secret_reference":
		var v string
		if err := json.Unmarshal(value, &v); err != nil {
			return fmt.Errorf("%s must be a string", def.Key)
		}
		if len(v) > 512 {
			return fmt.Errorf("%s is too long", def.Key)
		}
	default:
		return fmt.Errorf("%s has unsupported kind %q", def.Key, def.Kind)
	}
	return nil
}

func runRuntimeConfigSubscriber(ctx context.Context, pool *pgxpool.Pool, srv *server, log *slog.Logger) error {
	if pool == nil || srv == nil || srv.runtimeConfig == nil || srv.store == nil {
		return nil
	}
	const (
		initialSubscribeBackoff  = 250 * time.Millisecond
		maxSubscribeRetryBackoff = 5 * time.Second
	)
	backoff := initialSubscribeBackoff
	var ch <-chan db.Notification
	for {
		var err error
		ch, err = db.SubscribeWithReconnect(ctx, pool, []string{db.NotifyRuntimeConfigChanged}, log)
		if err == nil {
			break
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if log != nil {
			log.Warn("runtime_config subscriber setup failed; retrying", "err", err, "backoff", backoff)
		}
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return ctx.Err()
		case <-timer.C:
		}
		backoff *= 2
		if backoff > maxSubscribeRetryBackoff {
			backoff = maxSubscribeRetryBackoff
		}
	}
	// Reconcile once after LISTEN is established. This closes the startup
	// race where a setting changes between the boot reconciliation and the
	// subscriber registration.
	if err := srv.runtimeConfig.reconcile(ctx, srv.store); err != nil && log != nil {
		log.Error("runtime_config.reconcile_failed", "err", err)
	}
	ticker := time.NewTicker(runtimeConfigReconcileInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			// Notifications reduce propagation latency; this periodic repair
			// guarantees eventual convergence across LISTEN reconnects and
			// transient delivery gaps.
			if err := srv.runtimeConfig.reconcile(ctx, srv.store); err != nil && log != nil {
				log.Error("runtime_config.reconcile_failed", "err", err)
			}
		case _, ok := <-ch:
			if !ok {
				return nil
			}
			if err := srv.runtimeConfig.reconcile(ctx, srv.store); err != nil && log != nil {
				log.Error("runtime_config.reconcile_failed", "err", err)
			}
		}
	}
}
