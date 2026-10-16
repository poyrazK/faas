package apidserver

import (
	"fmt"

	"github.com/onebox-faas/faas/pkg/workloadidentity"
)

func (s *server) configureFeatureFlags(cfg Config, getenv func(string) string) error {
	s.featureFlagsEnabled = cfg.FlagsEnabled
	if v := getenv("FAAS_FLAGS_ENABLED"); v != "" {
		s.featureFlagsEnabled = v == "1"
	}
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
