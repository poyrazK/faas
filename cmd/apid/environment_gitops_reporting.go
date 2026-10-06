package main

import (
	"context"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentgitops"
	"github.com/onebox-faas/faas/pkg/state"
)

// Reporting observes persisted approved definitions without executing intent,
// recovering enforce effects, or launching workload qualification. The complete
// graph executor remains separately gated on native acceptance.
func (s *server) startEnvironmentGitDriftReporting(ctx context.Context, getenv func(string) string) func() {
	if !strings.EqualFold(strings.TrimSpace(getenv("FAAS_ENVIRONMENT_GIT_DRIFT_REPORTING_ENABLED")), "true") {
		return func() {}
	}
	store, storeOK := s.store.(state.EnvironmentGitOpsStore)
	intent, intentOK := s.store.(state.EnvironmentGitOpsIntentStore)
	effects, effectsOK := s.store.(state.EnvironmentGitOpsEffectStore)
	_, claimsOK := s.store.(state.EnvironmentGitOpsModeClaimStore)
	_, runtimeOK := s.store.(state.EnvironmentGitOpsRuntimeStore)
	if !storeOK || !intentOK || !effectsOK || !claimsOK || !runtimeOK {
		s.log.Warn("environment Git drift reporting requires supported store capabilities", "error_code", "environment_git_reporting_unavailable")
		return func() {}
	}
	worker := &environmentgitops.Worker{Store: store, Mode: "report", Log: s.log,
		Backend:       &environmentGitOpsBackend{server: s, intent: intent, effects: effects},
		LeaseDuration: api.EnvironmentGitOpsReportLeaseDuration, CheckInterval: api.EnvironmentGitOpsReportCheckInterval,
		RetryInterval: api.EnvironmentGitOpsReportRetryInterval,
	}
	workerCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = worker.Run(workerCtx, api.EnvironmentGitOpsReportIdleInterval)
	}()
	return func() { cancel(); <-done }
}
