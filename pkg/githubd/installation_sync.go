package githubd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/onebox-faas/faas/pkg/githubdgrpc"
	"github.com/onebox-faas/faas/pkg/state"
)

// InstallationSyncStore is the persistence seam for installation drift
// reconciliation. It intentionally contains only the queries and mutation
// needed by the worker so it can be exercised without a database.
type InstallationSyncStore interface {
	ListGitHubInstallations(context.Context) ([]state.GitHubInstall, error)
	ListGitHubInstallBindingsForInstallation(context.Context, int64) ([]state.GitHubBinding, error)
	RemoveGitHubRepositories(context.Context, int64, []string) error
	RecordGitHubInstallationSync(context.Context, int64, time.Time, string, int, int) error
}

// InstallationRepositoryClient lists repositories for an exact GitHub App
// installation. AccountID is part of the call so a stale or forged account
// association cannot silently select a different installation's token.
type InstallationRepositoryClient interface {
	ListInstallableReposContext(context.Context, string, int64) ([]githubdgrpc.Repo, error)
}

// InstallationSyncSummary describes one complete reconciliation pass.
type InstallationSyncSummary struct {
	Installations      int
	RemoteRepositories int
	ExistingBindings   int
	DetachedBindings   int
	Failures           int
}

// InstallationSyncer compares durable bindings with GitHub's current
// installation repository list. New repositories are deliberately not bound
// automatically; they become available to the customer through the picker.
type InstallationSyncer struct {
	Store        InstallationSyncStore
	Repositories InstallationRepositoryClient
	Metrics      *InstallationSyncMetrics
	Now          func() time.Time
}

// NewInstallationSyncer constructs a reconciler with production defaults.
func NewInstallationSyncer(store InstallationSyncStore, repositories InstallationRepositoryClient) *InstallationSyncer {
	return &InstallationSyncer{
		Store:        store,
		Repositories: repositories,
		Now:          time.Now,
	}
}

// SyncOnce reconciles all currently durable installations. It continues past
// an individual installation failure and returns an aggregate error after
// recording the failure on that installation's health columns.
func (s *InstallationSyncer) SyncOnce(ctx context.Context) (InstallationSyncSummary, error) {
	var summary InstallationSyncSummary
	if s == nil || s.Store == nil || s.Repositories == nil {
		err := errors.New("githubd: installation sync is not configured")
		s.observe(summary, err)
		return summary, err
	}
	installs, err := s.Store.ListGitHubInstallations(ctx)
	if err != nil {
		s.observe(summary, err)
		return summary, fmt.Errorf("githubd: list installations: %w", err)
	}
	summary.Installations = len(installs)
	errs := make([]error, 0)
	for _, install := range installs {
		if ctx.Err() != nil {
			errs = append(errs, ctx.Err())
			break
		}
		remote, syncErr := s.Repositories.ListInstallableReposContext(ctx, install.AccountID, install.InstallationID)
		if syncErr != nil {
			summary.Failures++
			errs = append(errs, fmt.Errorf("installation %d: list remote repositories: %w", install.InstallationID, syncErr))
			if recordErr := s.record(ctx, install.InstallationID, syncErr, 0, 0); recordErr != nil {
				errs = append(errs, recordErr)
			}
			continue
		}

		summary.RemoteRepositories += len(remote)
		bindings, listErr := s.Store.ListGitHubInstallBindingsForInstallation(ctx, install.InstallationID)
		if listErr != nil {
			summary.Failures++
			errs = append(errs, fmt.Errorf("installation %d: list bindings: %w", install.InstallationID, listErr))
			if recordErr := s.record(ctx, install.InstallationID, listErr, len(remote), 0); recordErr != nil {
				errs = append(errs, recordErr)
			}
			continue
		}
		summary.ExistingBindings += len(bindings)

		remoteNames := make(map[string]struct{}, len(remote))
		for _, repo := range remote {
			if name := canonicalRepoName(repo.FullName); name != "" {
				remoteNames[name] = struct{}{}
			}
		}
		missingNames := make(map[string]string)
		for _, binding := range bindings {
			name := canonicalRepoName(binding.RepoFullName)
			if name == "" {
				continue
			}
			if _, ok := remoteNames[name]; !ok {
				// Keep the durable spelling because the SQL mutation matches
				// the exact value stored on apps.
				missingNames[name] = binding.RepoFullName
			}
		}
		missing := make([]string, 0, len(missingNames))
		for _, name := range missingNames {
			missing = append(missing, name)
		}
		sort.Strings(missing)
		detached := len(missing)
		var installErr error
		if detached > 0 {
			if removeErr := s.Store.RemoveGitHubRepositories(ctx, install.InstallationID, missing); removeErr != nil {
				summary.Failures++
				installErr = fmt.Errorf("installation %d: detach inaccessible repositories: %w", install.InstallationID, removeErr)
				errs = append(errs, installErr)
				detached = 0 // the transaction may have rolled back; do not over-report
			}
		}
		summary.DetachedBindings += detached
		if recordErr := s.record(ctx, install.InstallationID, installErr, len(remote), detached); recordErr != nil {
			summary.Failures++
			errs = append(errs, recordErr)
		}
	}
	joined := errors.Join(errs...)
	s.observe(summary, joined)
	return summary, joined
}

func (s *InstallationSyncer) record(ctx context.Context, installationID int64, syncErr error, remote, detached int) error {
	if s.Now == nil {
		s.Now = time.Now
	}
	errText := ""
	if syncErr != nil {
		errText = syncErr.Error()
	}
	if err := s.Store.RecordGitHubInstallationSync(ctx, installationID, s.Now().UTC(), errText, remote, detached); err != nil {
		return fmt.Errorf("installation %d: record sync result: %w", installationID, err)
	}
	return nil
}

func (s *InstallationSyncer) observe(summary InstallationSyncSummary, err error) {
	if s != nil && s.Metrics != nil {
		s.Metrics.Observe(summary, err)
	}
}

func canonicalRepoName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

// DefaultInstallationSyncInterval is intentionally short enough to repair a
// missed webhook within a normal workday while keeping GitHub API traffic low.
const DefaultInstallationSyncInterval = 15 * time.Minute

// RunInstallationSyncWorker runs one immediate pass and then periodic passes
// until ctx is cancelled. A failed pass is logged and does not stop future
// reconciliation attempts.
func RunInstallationSyncWorker(ctx context.Context, syncer *InstallationSyncer, interval time.Duration, log *slog.Logger) {
	if syncer == nil {
		return
	}
	if interval <= 0 {
		interval = DefaultInstallationSyncInterval
	}
	if log == nil {
		log = slog.Default()
	}
	run := func() {
		summary, err := syncer.SyncOnce(ctx)
		if err != nil {
			log.Warn("githubd: installation reconciliation failed", "err", err, "installations", summary.Installations, "detached_bindings", summary.DetachedBindings)
			return
		}
		log.Info("githubd: installation reconciliation complete", "installations", summary.Installations, "remote_repositories", summary.RemoteRepositories, "detached_bindings", summary.DetachedBindings)
	}
	run()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run()
		}
	}
}

// InstallationSyncMetrics is a low-cardinality Prometheus collector for the
// reconciler. Error text is persisted per installation, not exposed as a
// metric label.
type InstallationSyncMetrics struct {
	mu                   sync.Mutex
	runs                 float64
	failures             float64
	lastRun              float64
	lastSuccess          float64
	remoteRepositories   float64
	detachedBindings     float64
	runsDesc             *prometheus.Desc
	failuresDesc         *prometheus.Desc
	lastRunDesc          *prometheus.Desc
	lastSuccessDesc      *prometheus.Desc
	remoteReposDesc      *prometheus.Desc
	detachedBindingsDesc *prometheus.Desc
}

// NewInstallationSyncMetrics creates a collector suitable for a per-daemon
// registry.
func NewInstallationSyncMetrics() *InstallationSyncMetrics {
	return &InstallationSyncMetrics{
		runsDesc:             prometheus.NewDesc("githubd_installation_sync_runs_total", "Installation drift reconciliation passes.", nil, nil),
		failuresDesc:         prometheus.NewDesc("githubd_installation_sync_failures_total", "Installation drift reconciliation failures.", nil, nil),
		lastRunDesc:          prometheus.NewDesc("githubd_installation_sync_last_run_timestamp_seconds", "Unix timestamp of the last installation reconciliation pass.", nil, nil),
		lastSuccessDesc:      prometheus.NewDesc("githubd_installation_sync_last_success_timestamp_seconds", "Unix timestamp of the last successful installation reconciliation pass.", nil, nil),
		remoteReposDesc:      prometheus.NewDesc("githubd_installation_sync_remote_repositories", "Remote repositories observed during the last reconciliation pass.", nil, nil),
		detachedBindingsDesc: prometheus.NewDesc("githubd_installation_sync_detached_bindings_total", "Bindings detached by installation drift reconciliation.", nil, nil),
	}
}

// Observe records one pass and its aggregate result.
func (m *InstallationSyncMetrics) Observe(summary InstallationSyncSummary, err error) {
	if m == nil {
		return
	}
	now := float64(time.Now().Unix())
	m.mu.Lock()
	defer m.mu.Unlock()
	m.runs++
	if err != nil || summary.Failures > 0 {
		m.failures++
	} else {
		m.lastSuccess = now
	}
	m.lastRun = now
	m.remoteRepositories = float64(summary.RemoteRepositories)
	m.detachedBindings += float64(summary.DetachedBindings)
}

// Describe implements prometheus.Collector.
func (m *InstallationSyncMetrics) Describe(ch chan<- *prometheus.Desc) {
	if m == nil {
		return
	}
	ch <- m.runsDesc
	ch <- m.failuresDesc
	ch <- m.lastRunDesc
	ch <- m.lastSuccessDesc
	ch <- m.remoteReposDesc
	ch <- m.detachedBindingsDesc
}

// Collect implements prometheus.Collector.
func (m *InstallationSyncMetrics) Collect(ch chan<- prometheus.Metric) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	ch <- prometheus.MustNewConstMetric(m.runsDesc, prometheus.CounterValue, m.runs)
	ch <- prometheus.MustNewConstMetric(m.failuresDesc, prometheus.CounterValue, m.failures)
	ch <- prometheus.MustNewConstMetric(m.lastRunDesc, prometheus.GaugeValue, m.lastRun)
	ch <- prometheus.MustNewConstMetric(m.lastSuccessDesc, prometheus.GaugeValue, m.lastSuccess)
	ch <- prometheus.MustNewConstMetric(m.remoteReposDesc, prometheus.GaugeValue, m.remoteRepositories)
	ch <- prometheus.MustNewConstMetric(m.detachedBindingsDesc, prometheus.CounterValue, m.detachedBindings)
}
