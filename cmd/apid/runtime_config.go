package main

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
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	runtimeConfigTenantSurfaces  = "tenant_surfaces_enabled"
	runtimeConfigDomainDoctor    = "domain_doctor_enabled"
	runtimeConfigDomainDoctorTTL = "domain_doctor_ttl_seconds"
	runtimeConfigDataPlacement   = "data_placement_enabled"
	runtimeConfigAppErrors       = "app_errors_enabled"
	runtimeConfigRekey           = "rekey_enabled"
	runtimeConfigHSTS            = "hsts_enabled"

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
	{Key: runtimeConfigTenantSurfaces, Label: "Tenant surfaces", Description: "Expose the tenant surface and certificate lifecycle API.", Category: "Feature flags", Kind: "boolean", Default: json.RawMessage("false"), ApplyMode: state.RuntimeConfigApplyHot, ControllerEnabled: true, Mutable: true},
	{Key: runtimeConfigDomainDoctor, Label: "Domain doctor", Description: "Run DNS and certificate readiness probes for customer domains.", Category: "Feature flags", Kind: "boolean", Default: json.RawMessage("true"), ApplyMode: state.RuntimeConfigApplyHot, ControllerEnabled: true, Mutable: true},
	{Key: runtimeConfigDomainDoctorTTL, Label: "Domain doctor TTL", Description: "Maximum age in seconds before a domain doctor result is stale.", Category: "Operational policies", Kind: "integer", Default: json.RawMessage("300"), ApplyMode: state.RuntimeConfigApplyHot, ControllerEnabled: true, Mutable: true},
	{Key: runtimeConfigDataPlacement, Label: "Data placement", Description: "Enable customer data-upstream placement and affinity behavior.", Category: "Feature flags", Kind: "boolean", Default: json.RawMessage("false"), ApplyMode: state.RuntimeConfigApplyHot, ControllerEnabled: true, Mutable: true},
	{Key: runtimeConfigAppErrors, Label: "Automatic app errors", Description: "Accept and aggregate gateway error reports; enabling the socket is a graceful daemon change.", Category: "Feature flags", Kind: "boolean", Default: json.RawMessage("true"), ApplyMode: state.RuntimeConfigApplyGraceful, ControllerEnabled: false, Mutable: true},
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
	getenv   func(string) string
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
	}
	for _, def := range runtimeConfigCatalog {
		m.defs[def.Key] = def
		m.values[def.Key] = runtimeConfigEnvDefault(def, getenv)
	}
	return m
}

func runtimeConfigEnvDefault(def runtimeConfigDefinition, getenv func(string) string) json.RawMessage {
	var envName string
	switch def.Key {
	case runtimeConfigTenantSurfaces:
		envName = "FAAS_TENANT_SURFACES_ENABLED"
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
	out := make([]runtimeConfigDefinition, 0, len(runtimeConfigCatalog))
	for _, def := range runtimeConfigCatalog {
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
		applied, applyErr := m.applyVersion(row.Key, row.DesiredValue, row.Version)
		if applyErr != nil {
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
