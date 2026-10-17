package apidserver

import (
	"encoding/json"

	"github.com/onebox-faas/faas/pkg/platformflags"
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
