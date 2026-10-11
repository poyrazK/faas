package main

import (
	"fmt"

	"github.com/onebox-faas/faas/pkg/operations"
	"github.com/onebox-faas/faas/pkg/workloadidentity"
)

func (s *server) configureOperations(cfg Config, getenv func(string) string) error {
	s.operationsPreview = &operations.PreviewAdmission{}
	gate, err := operations.NewPreviewAdmission(cfg.OperationsPreviewPolicyPath)
	if err != nil {
		return err
	}
	path := cfg.OperationsWorkloadJWKSPath
	if raw := getenv("FAAS_OPERATIONS_WORKLOAD_JWKS_PATH"); raw != "" {
		path = raw
	}
	if path == "" {
		if cfg.OperationsPreviewPolicyPath != "" {
			return fmt.Errorf("operations preview configuration requires workload trust")
		}
		return nil
	}
	issuer := cfg.OperationsWorkloadIssuer
	if raw := getenv("FAAS_OPERATIONS_WORKLOAD_ISSUER"); raw != "" {
		issuer = raw
	}
	if issuer == "" {
		issuer = workloadidentity.DefaultIssuer
	}
	verifier, err := workloadidentity.LoadVerifier(path, issuer)
	if err != nil {
		return fmt.Errorf("configure Operations workload trust: %w", err)
	}
	s.operationsWorkloadVerifier = verifier
	s.operationsPreview = gate
	return nil
}
