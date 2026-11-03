package sched

import (
	"context"

	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/state"
)

// ADR-20261031193635 slice 5a, for package sched_test.

var ErrCredentialFreeCaptureUnsupported = errCredentialFreeCaptureUnsupported

// XCredentialFreeCaptureEnv applies the capture boot's secret rule.
func XCredentialFreeCaptureEnv(entries []fcvm.SealedEnvEntry, candidates []state.AppSecretDeliveryCandidate, apiEnv []fcvm.APIEnvEntry, rows []state.AppSecret) ([]fcvm.SealedEnvEntry, []state.AppSecretDeliveryCandidate, []fcvm.APIEnvEntry) {
	delivery, env := credentialFreeCaptureEnv(sealedEnvDelivery{Entries: entries, Candidates: candidates}, apiEnv, managedSecretKinds(rows))
	return delivery.Entries, delivery.Candidates, env
}

func XSidecarsReceiveManagedSecrets(sidecars []fcvm.WorkloadSpec, rows []state.AppSecret) bool {
	return sidecarsReceiveManagedSecrets(sidecars, managedSecretKinds(rows))
}

func (e *Engine) XRequestEnvironmentBranchCapture(ctx context.Context, app state.App, dep state.Deployment) {
	e.requestEnvironmentBranchCapture(ctx, app, dep)
}
