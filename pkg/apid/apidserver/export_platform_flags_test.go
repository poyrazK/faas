package apidserver

import (
	"encoding/json"

	"github.com/onebox-faas/faas/pkg/apid/livequeries"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/state"
)

// EnablePlatformFlagForAll seeds a platform flag on for every account: the
// boot fallback FAAS_FLAGS_ENABLED=1 or FAAS_AI_GATEWAY_ENABLED=1 produces.
func (r *server) EnablePlatformFlagForAll(flagKey string) {
	r.seedPlatformFlag(flagKey, platformflags.FromAccountList(true, true, nil))
}

// SeedPlatformFlag installs a boot-time platform-flag definition.
func (r *server) SeedPlatformFlag(flagKey string, def platformflags.Definition) {
	r.seedPlatformFlag(flagKey, def)
}

// SeedEnvPlatformFlags seeds the env-gated flags' boot fallbacks from getenv.
func (r *server) SeedEnvPlatformFlags(getenv func(string) string) { r.seedEnvPlatformFlags(getenv) }

// ApplicationAuthEnabledFor is the application_auth release gate.
func (r *server) ApplicationAuthEnabledFor(acct state.Account) bool {
	return r.applicationAuthEnabled(acct)
}

// ChangeTimelineEnabledFor is the change_timeline release gate; legacy sets
// FAAS_CHANGE_TIMELINE_ENABLED's parsed value first.
func (r *server) ChangeTimelineEnabledFor(acct state.Account, legacy bool) bool {
	r.changeTimelineEnabled = legacy
	return r.changeTimelineFor(acct)
}

// AnomalyAlertsAllowedFor reports whether a baseline comparison is admitted
// for acct; legacy sets FAAS_ANOMALY_ALERTS_ENABLED's parsed value first.
func (r *server) AnomalyAlertsAllowedFor(acct state.Account, legacy bool) bool {
	r.anomalyAlertsEnabled = legacy
	return r.anomalyAlertsGate(acct, "above_baseline") == nil
}

// LiveGate is the live-query gate currently in effect.
func (r *server) LiveGate() livequeries.Gate { return r.liveGate() }

// PlatformFlagOn reports a platform flag's kill switch.
func (r *server) PlatformFlagOn(flagKey string) bool { return r.platformFlagOn(flagKey) }

// PlatformFlagSet exposes the compiled platform-flag holder.
func (r *server) PlatformFlagSet() *platformflags.Set { return r.platformFlags() }

// EnableRuntimeConfigReceipts makes the server's manager report apply
// receipts as daemon.
func (r *server) EnableRuntimeConfigReceipts(daemon string) {
	if r.runtimeConfig == nil {
		r.runtimeConfig = newRuntimeConfigManager(nil)
	}
	r.runtimeConfig.enableReceipts(daemon)
}

// ApplyRuntimeConfig installs a durable runtime-configuration value as if
// the subscriber had reconciled it at the given version.
func (r *server) ApplyRuntimeConfig(key string, value json.RawMessage, version int64) (bool, error) {
	if r.runtimeConfig == nil {
		r.runtimeConfig = newRuntimeConfigManager(nil)
	}
	return r.runtimeConfig.applyVersion(key, value, version)
}
