package apidserver

import (
	"fmt"

	"github.com/onebox-faas/faas/pkg/workloadidentity"
)

// Cluster workload trust (ADR-1141): the public JWKS of the workload-identity
// key every vmmd signs with. Queue leases verify against it, and Flags and
// Operations fall back to it when their own trust settings are empty.
const (
	envWorkloadJWKSPath = "FAAS_WORKLOAD_JWKS_PATH"
	envWorkloadIssuer   = "FAAS_WORKLOAD_ISSUER"
)

// workloadTrustSetting resolves one trust setting: the surface's own env and
// TOML values win, then the cluster-wide env and TOML values.
func workloadTrustSetting(getenv func(string) string, specificEnv, specific, generalEnv, general string) string {
	for _, v := range []string{getenv(specificEnv), specific, getenv(generalEnv), general} {
		if v != "" {
			return v
		}
	}
	return ""
}

// workloadTrust returns the JWKS path and issuer for a surface; an empty path
// means no trust is configured.
func workloadTrust(cfg Config, getenv func(string) string, pathEnv, path, issuerEnv, issuer string) (string, string) {
	resolvedPath := workloadTrustSetting(getenv, pathEnv, path, envWorkloadJWKSPath, cfg.WorkloadJWKSPath)
	resolvedIssuer := workloadTrustSetting(getenv, issuerEnv, issuer, envWorkloadIssuer, cfg.WorkloadIssuer)
	if resolvedIssuer == "" {
		resolvedIssuer = workloadidentity.DefaultIssuer
	}
	return resolvedPath, resolvedIssuer
}

// configureWorkloadTrust loads the cluster verifier used by queue leases.
func (s *server) configureWorkloadTrust(cfg Config, getenv func(string) string) error {
	path, issuer := workloadTrust(cfg, getenv, "", "", "", "")
	if path == "" {
		return nil
	}
	verifier, err := workloadidentity.LoadVerifier(path, issuer)
	if err != nil {
		return fmt.Errorf("configure workload trust: %w", err)
	}
	s.workloadVerifier = verifier
	return nil
}
