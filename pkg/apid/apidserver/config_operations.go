package apidserver

import (
	"fmt"

	"github.com/onebox-faas/faas/pkg/operations"
	"github.com/onebox-faas/faas/pkg/workloadidentity"
)

func (s *server) configureOperations(cfg Config, getenv func(string) string) error {
	s.operation.Preview = &operations.PreviewAdmission{}
	gate, err := operations.NewPreviewAdmission(cfg.OperationsPreviewPolicyPath)
	if err != nil {
		return err
	}
	path, issuer := workloadTrust(cfg, getenv, "FAAS_OPERATIONS_WORKLOAD_JWKS_PATH", cfg.OperationsWorkloadJWKSPath,
		"FAAS_OPERATIONS_WORKLOAD_ISSUER", cfg.OperationsWorkloadIssuer)
	if path == "" {
		if cfg.OperationsPreviewPolicyPath != "" {
			return fmt.Errorf("operations preview configuration requires workload trust")
		}
		return nil
	}
	verifier, err := workloadidentity.LoadVerifier(path, issuer)
	if err != nil {
		return fmt.Errorf("configure Operations workload trust: %w", err)
	}
	s.operation.WorkloadVerifier = verifier
	s.operation.Preview = gate
	return nil
}
