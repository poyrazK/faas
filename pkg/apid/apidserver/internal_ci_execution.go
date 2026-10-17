package apidserver

import (
	"context"
	"io"
	"log/slog"
	"sync"

	"github.com/onebox-faas/faas/pkg/apid/ciapi"
	"github.com/onebox-faas/faas/pkg/ci"
)

func (s *server) internalCIExecutionReady() bool {
	return s.ciConfig.WorkerEnabled && s.ciConfig.TransportEnabled && ci.ValidRuntimeOrigin(s.ciRuntimeOrigin) &&
		s.ciBlobs != nil && s.ciBlobs.Store != nil && s.ciBlobs.Physical != nil && s.githubd != nil
}

func (s *server) startInternalCIWorker(ctx context.Context, log *slog.Logger) func() {
	if !s.ciConfig.HealthMonitoringEnabled() {
		return func() {}
	}
	provider, ok := s.store.(interface{ CIStore() ci.Store })
	if !ok {
		return func() {}
	}
	store := provider.CIStore()
	configured := s.ciConfig.WorkerConfiguration("apid")
	githubStore, githubReady := store.(ci.GitHubStore)
	blobsReady := s.ciBlobs != nil && s.ciBlobs.Store != nil && s.ciBlobs.Physical != nil
	workerCtx, cancel := context.WithCancel(ctx)
	var workers sync.WaitGroup
	launch := func(kind ci.WorkerKind, enabled, ready bool, run func(context.Context)) {
		workers.Go(func() { ci.ObserveWorker(workerCtx, store, kind, enabled, ready, run) })
	}
	launch(ci.WorkerMetadataAdmission, configured[ci.WorkerMetadataAdmission], githubReady && s.githubd != nil, func(ctx context.Context) {
		ci.RunGitHubAdmissionWorker(ctx, s.ciConfig, githubStore, s.verifyCIGitHubRepository, log)
	})
	launch(ci.WorkerDispatcher, configured[ci.WorkerDispatcher], blobsReady, func(ctx context.Context) {
		source := ci.GitHubArchiveSource{Verify: s.verifyCIGitHubRepository, Blobs: s.ciBlobs, Stream: ciapi.RPCSource{Open: s.openCISource}}
		dispatcher := ci.Dispatcher{Config: s.ciConfig, Store: s.ciBlobs.Store, Log: log, Backend: ci.JobsBackend{
			Config: s.ciConfig, Store: s.ciBlobs.Store, Source: source, Output: ci.SealedJobsOutput{Store: s.ciBlobs.Store, Blobs: s.ciBlobs}}}
		dispatcher.Run(ctx)
	})
	automationStore, automationReady := store.(ci.AutomationStore)
	launch(ci.WorkerAutomationAdmission, configured[ci.WorkerAutomationAdmission], automationReady && s.internalCIExecutionReady(), func(ctx context.Context) {
		ci.RunAutomationWorkerWithResolvers(ctx, s.ciConfig, automationStore, s.verifyCIGitHubRepository, log, ci.AutomationResolvers{Branch: s.resolveCIAutomationHead, PullRequest: s.resolveCIAutomationPullRequest, Changes: s.resolveCIAutomationChanges})
	})
	launch(ci.WorkerRetention, configured[ci.WorkerRetention], blobsReady, func(ctx context.Context) {
		ci.RunStorageRetention(ctx, s.ciConfig, s.ciBlobs, log)
	})
	return func() { cancel(); workers.Wait() }
}

func (s *server) resolveCIAutomationHead(ctx context.Context, binding ci.GitHubBinding, branch string) (string, bool, error) {
	branches, ok := s.githubd.(interface {
		GetBranchHead(context.Context, string, int64, string, string) (string, bool, error)
	})
	if !ok {
		return "", false, ci.ErrGitHubUnavailable
	}
	sha, found, err := branches.GetBranchHead(ctx, binding.AccountID, binding.InstallationID, binding.Repository, branch)
	if err != nil {
		return "", false, ci.ErrGitHubUnavailable
	}
	return sha, found, nil
}

func (s *server) configureInternalCIHealthMetrics(ctx context.Context) {
	if s.ops == nil {
		s.ciHealthMetrics = nil
		return
	}
	if s.ciHealthMetrics != nil && s.ciHealthMetrics.Registry() == s.ops.Registry() {
		return
	}
	var store ci.FleetHealthReader
	if provider, ok := s.store.(interface{ CIStore() ci.Store }); ok {
		store, _ = provider.CIStore().(ci.FleetHealthReader)
	}
	s.ciHealthMetrics = ci.NewHealthMetrics(ctx, s.ops.Registry(), s.ops.MetricPrefix(), store, func() bool { return s.ciConfig.HealthMonitoringEnabled() }, func() map[ci.WorkerKind]bool { return s.ciConfig.WorkerConfiguration("apid") })
}

func (s *server) openCISource(ctx context.Context, account string, installation int64, repository, sha string, bound int64) (io.ReadCloser, func() (string, bool, error), error) {
	if s.githubd == nil {
		return nil, nil, ci.ErrGitHubUnavailable
	}
	result, err := s.githubd.StreamSourceRef(ctx, account, installation, repository, sha, bound)
	if err != nil || result == nil {
		return nil, nil, ci.ErrGitHubUnavailable
	}
	receipt := func() (string, bool, error) {
		if result.Stats == nil {
			return "", false, ci.ErrGitHubUnavailable
		}
		return result.Stats.ResolvedCommitSHA, result.Stats.Truncated, result.Stats.Err
	}
	return result.Body, receipt, nil
}
