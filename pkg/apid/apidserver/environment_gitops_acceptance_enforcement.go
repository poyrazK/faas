package apidserver

import (
	"context"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentgitops"
	"github.com/onebox-faas/faas/pkg/state"
)

const environmentGitOpsAcceptanceEnforcementFlag = "FAAS_ENVIRONMENT_GITOPS_ACCEPTANCE_ENFORCEMENT"

// startEnvironmentGitAcceptanceEnforcement exists only for isolated acceptance
// runs. The environment contract rejects its dev-only flag on a production
// control plane, and both the dev and acceptance markers prevent a stray flag
// from opening customer controls. Production enforcement remains gated.
func (s *server) startEnvironmentGitAcceptanceEnforcement(ctx context.Context, getenv func(string) string) func() {
	if getenv == nil || strings.TrimSpace(getenv(environmentGitOpsAcceptanceEnforcementFlag)) != "1" ||
		strings.TrimSpace(getenv("GREGALE_GITOPS_ACCEPTANCE")) != "1" ||
		strings.TrimSpace(getenv("FAAS_DEV")) != "1" {
		return func() {}
	}
	store, storeOK := s.store.(state.EnvironmentGitOpsStore)
	intent, intentOK := s.store.(state.EnvironmentGitOpsIntentStore)
	effects, effectsOK := s.store.(state.EnvironmentGitOpsEffectStore)
	_, claimsOK := s.store.(state.EnvironmentGitOpsModeClaimStore)
	_, runtimeOK := s.store.(state.EnvironmentGitOpsRuntimeStore)
	if !storeOK || !intentOK || !effectsOK || !claimsOK || !runtimeOK {
		s.log.Warn("environment GitOps acceptance enforcement requires supported store capabilities",
			"error_code", "environment_git_acceptance_enforcement_unavailable")
		return func() {}
	}
	worker := &environmentgitops.Worker{Store: store, Mode: "enforce", Log: s.log,
		Backend:       &environmentGitOpsBackend{server: s, intent: intent, effects: effects},
		LeaseDuration: api.EnvironmentGitOpsReportLeaseDuration, CheckInterval: api.EnvironmentGitOpsReportCheckInterval,
		RetryInterval: api.EnvironmentGitOpsReportRetryInterval,
	}
	workerCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	s.environmentGitOpsAcceptanceEnforcement.Store(true)
	go func() {
		defer close(done)
		defer s.environmentGitOpsAcceptanceEnforcement.Store(false)
		_ = worker.Run(workerCtx, api.EnvironmentGitOpsReportIdleInterval)
	}()
	return func() {
		s.environmentGitOpsAcceptanceEnforcement.Store(false)
		cancel()
		<-done
	}
}
