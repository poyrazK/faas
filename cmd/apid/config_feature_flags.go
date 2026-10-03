package main

import (
	"fmt"
	"github.com/onebox-faas/faas/pkg/workloadidentity"
)

func (s *server) configureFeatureFlags(cfg Config, getenv func(string) string) error {
	s.featureFlagsEnabled = cfg.FlagsEnabled
	if v := getenv("FAAS_FLAGS_ENABLED"); v != "" {
		s.featureFlagsEnabled = v == "1"
	}
	path := cfg.FlagsWorkloadJWKSPath
	if v := getenv("FAAS_FLAGS_WORKLOAD_JWKS_PATH"); v != "" {
		path = v
	}
	if path == "" {
		return nil
	}
	issuer := cfg.FlagsWorkloadIssuer
	if v := getenv("FAAS_FLAGS_WORKLOAD_ISSUER"); v != "" {
		issuer = v
	}
	if issuer == "" {
		issuer = workloadidentity.DefaultIssuer
	}
	verifier, err := workloadidentity.LoadVerifier(path, issuer)
	if err != nil {
		return fmt.Errorf("configure Flags workload trust: %w", err)
	}
	s.flagsWorkloadVerifier = verifier
	return nil
}
