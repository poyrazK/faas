package apidserver

import (
	"fmt"

	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/workloadidentity"
)

// configureFeatureFlags seeds the customer_flags platform flag (ADR-1700)
// from flags_enabled / FAAS_FLAGS_ENABLED: "1" releases customer Flags to
// every account, any other value keeps it off. An operator value written
// through /v1/admin/config wins and can release it to selected accounts.
func (s *server) configureFeatureFlags(cfg Config, getenv func(string) string) error {
	enabled := cfg.FlagsEnabled
	if v := getenv("FAAS_FLAGS_ENABLED"); v != "" {
		enabled = v == "1"
	}
	s.seedPlatformFlag(platformflags.CustomerFlags, platformflags.FromAccountList(enabled, true, nil))
	path, issuer := workloadTrust(cfg, getenv, "FAAS_FLAGS_WORKLOAD_JWKS_PATH", cfg.FlagsWorkloadJWKSPath,
		"FAAS_FLAGS_WORKLOAD_ISSUER", cfg.FlagsWorkloadIssuer)
	if path == "" {
		return nil
	}
	verifier, err := workloadidentity.LoadVerifier(path, issuer)
	if err != nil {
		return fmt.Errorf("configure Flags workload trust: %w", err)
	}
	s.flagsWorkloadVerifier = verifier
	return nil
}
