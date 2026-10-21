package apidserver

import (
	"context"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentgitops"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/state"
)

const environmentGitOpsAcceptanceEnforcementFlag = "FAAS_ENVIRONMENT_GITOPS_ACCEPTANCE_ENFORCEMENT"

// environmentGitAcceptanceEnforcement reports an isolated acceptance run. The
// environment contract rejects the dev-only flag on a production control
// plane, and both the dev and acceptance markers keep a stray flag from
// releasing execution to every account.
func environmentGitAcceptanceEnforcement(getenv func(string) string) bool {
	return getenv != nil && strings.TrimSpace(getenv(environmentGitOpsAcceptanceEnforcementFlag)) == "1" &&
		strings.TrimSpace(getenv("GREGALE_GITOPS_ACCEPTANCE")) == "1" &&
		strings.TrimSpace(getenv("FAAS_DEV")) == "1"
}

// startEnvironmentGitEnforcement runs the enforce worker (ADR-20261021080847).
// It claims only enforce-mode sources and executes one only while the
// environment_gitops_execution platform flag admits the source's account;
// otherwise the source is held as blocked. Customer controls open enforce
// mode only while this worker runs.
func (s *server) startEnvironmentGitEnforcement(ctx context.Context) func() {
	store, storeOK := s.store.(state.EnvironmentGitOpsStore)
	intent, intentOK := s.store.(state.EnvironmentGitOpsIntentStore)
	effects, effectsOK := s.store.(state.EnvironmentGitOpsEffectStore)
	_, claimsOK := s.store.(state.EnvironmentGitOpsModeClaimStore)
	_, runtimeOK := s.store.(state.EnvironmentGitOpsRuntimeStore)
	if !storeOK || !intentOK || !effectsOK || !claimsOK || !runtimeOK {
		s.log.Warn("environment GitOps enforcement requires supported store capabilities",
			"error_code", "environment_git_enforcement_unavailable")
		return func() {}
	}
	worker := &environmentgitops.Worker{Store: store, Mode: "enforce", Log: s.log, Admit: s.environmentGitEnforcementAdmitted,
		Backend:       &environmentGitOpsBackend{server: s, intent: intent, effects: effects},
		LeaseDuration: api.EnvironmentGitOpsReportLeaseDuration, CheckInterval: api.EnvironmentGitOpsReportCheckInterval,
		RetryInterval: api.EnvironmentGitOpsReportRetryInterval,
	}
	workerCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	s.environmentGitOpsEnforcementWorkerLive.Store(true)
	go func() {
		defer close(done)
		defer s.environmentGitOpsEnforcementWorkerLive.Store(false)
		_ = worker.Run(workerCtx, api.EnvironmentGitOpsReportIdleInterval)
	}()
	return func() {
		s.environmentGitOpsEnforcementWorkerLive.Store(false)
		cancel()
		<-done
	}
}

// environmentGitEnforcementAdmitted decides execution for a source's account.
// A missing account fails closed.
func (s *server) environmentGitEnforcementAdmitted(ctx context.Context, accountID string) bool {
	if !s.platformFlagOn(platformflags.EnvironmentGitOpsExecution) {
		return false
	}
	acct, err := s.store.AccountByID(ctx, accountID)
	return err == nil && s.platformFlagEnabled(platformflags.EnvironmentGitOpsExecution, acct)
}
