package apidserver

import (
	"context"
	"net"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	hostingapi "github.com/onebox-faas/faas/pkg/apid/hosting"
	"github.com/onebox-faas/faas/pkg/hosting"
	"github.com/onebox-faas/faas/pkg/state"
)

type hostingHost struct{ s *server }

var _ hostingapi.Host = hostingHost{}

func (h hostingHost) HostingEnabled() bool {
	return h.s.runtimeBool(runtimeConfigFrontendHosting, false)
}
func (h hostingHost) HostingDurableRoutingEnabled() bool {
	return h.s.runtimeBool(runtimeConfigFrontendHostingDurable, false)
}
func (h hostingHost) AdminAllows(a state.Account) (bool, *api.Problem) { return h.s.adminAllows(a) }
func (h hostingHost) HostingStore() state.HostingStore {
	store, _ := h.s.store.(state.HostingStore)
	return store
}
func (h hostingHost) HostingArtifacts() hosting.ArtifactStore {
	if h.s.hostingConfig == nil {
		return nil
	}
	return h.s.hostingConfig.Artifacts
}
func (h hostingHost) HostingDomain() string {
	if h.s.hostingConfig == nil {
		return ""
	}
	return h.s.hostingConfig.Domain
}

func (h hostingHost) HostingOriginID() string {
	if h.s.hostingConfig == nil {
		return ""
	}
	return h.s.hostingConfig.Artifacts.ID()
}
func (h hostingHost) HostingProject(ctx context.Context, account, project, environment string) (state.Project, error) {
	p, err := h.s.store.ProjectBySlug(ctx, account, project)
	if err != nil {
		return state.Project{}, err
	}
	_, err = h.s.store.ProjectEnvironmentBySlug(ctx, account, p.ID, environment)
	return p, err
}
func (h hostingHost) HostingAudit(ctx context.Context, account, kind string, data map[string]any) {
	h.s.audit.Emit(ctx, kind, &account, data)
}

func (s *server) configureHosting(getenv func(string) string) error {
	cfg, err := hosting.Load(getenv, s.domain)
	if err != nil {
		return err
	}
	s.hostingConfig = cfg
	return nil
}

func (h hostingHost) HostingUploadBaseURL() string {
	if h.s.hostingConfig == nil {
		return ""
	}
	return h.s.hostingConfig.Artifacts.UploadBaseURL()
}

func (h hostingHost) HostingDomainsEnabled() bool {
	return h.s.runtimeBool(runtimeConfigFrontendHostingDomains, false)
}
func (h hostingHost) HostingDomainStore() state.HostingDomainStore {
	store, _ := h.s.store.(state.HostingDomainStore)
	return store
}
func (h hostingHost) HostingDomainProvider() hosting.DomainProvider {
	if h.s.hostingConfig == nil || h.s.hostingConfig.Domains == nil {
		return nil
	}
	return h.s.hostingConfig.Domains
}
func (h hostingHost) HostingReservedDomains() []string {
	if h.s.hostingConfig == nil {
		return nil
	}
	return h.s.hostingConfig.ReservedDomains
}

func (s *server) runHostingDomainReconciler(ctx context.Context) {
	host := hostingHost{s}
	store := host.HostingDomainStore()
	if store == nil {
		return
	}
	ticker := time.NewTicker(api.HostingReconcileInterval)
	defer ticker.Stop()
	for {
		r := hostingapi.DomainReconciler{Store: store, Sites: host.HostingStore(), Provider: host.HostingDomainProvider(), Publisher: s.hostingConfig.Artifacts, OriginID: host.HostingOriginID(), DNS: net.DefaultResolver}
		if err := r.RunOnce(ctx); err != nil && ctx.Err() == nil {
			s.log.Warn("hosting custom domain reconciliation pending")
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *server) runHostingReconciler(ctx context.Context) {
	if s.hostingConfig == nil {
		return
	}
	host := hostingHost{s}
	store := host.HostingStore()
	if store == nil {
		return
	}
	go s.runHostingDomainReconciler(ctx)
	go s.runHostingPreviewReconciler(ctx)
	go s.runHostingRetentionReconciler(ctx)
	ticker := time.NewTicker(api.HostingReconcileInterval)
	defer ticker.Stop()
	for {
		reconciler := hostingapi.Reconciler{Store: store, Artifacts: s.hostingConfig.Artifacts, LeasedRoutes: s.hostingConfig.Routes, DurableRoutes: s.hostingConfig.Artifacts, AdmissionEnabled: host.HostingEnabled, Log: s.log, Metrics: s.hostingMetrics}
		if err := reconciler.RunOnce(ctx); err != nil && ctx.Err() == nil {
			s.log.Warn("hosting reconciliation failed")
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (h hostingHost) HostingAnalyticsEnabled() bool {
	return h.s.runtimeBool(runtimeConfigFrontendHostingAnalytics, false)
}
func (h hostingHost) HostingTrafficReader() hosting.TrafficReader {
	if h.s.hostingConfig == nil || h.s.hostingConfig.Analytics == nil {
		return nil
	}
	return h.s.hostingConfig.Analytics
}

func (h hostingHost) HostingConfigEnabled() bool {
	return h.s.runtimeBool(runtimeConfigFrontendHostingConfig, false)
}

func (h hostingHost) HostingPreviewsEnabled() bool {
	return h.s.runtimeBool(runtimeConfigFrontendHostingPreviews, false)
}
func (h hostingHost) HostingPreviewStore() state.HostingPreviewStore {
	store, _ := h.s.store.(state.HostingPreviewStore)
	return store
}
func (s *server) runHostingPreviewReconciler(ctx context.Context) {
	h := hostingHost{s}
	store := h.HostingPreviewStore()
	if store == nil {
		return
	}
	ticker := time.NewTicker(api.HostingReconcileInterval)
	defer ticker.Stop()
	for {
		r := hostingapi.PreviewReconciler{Store: store, Sites: h.HostingStore(), Publisher: s.hostingConfig.Artifacts, OriginID: h.HostingOriginID()}
		if err := r.RunOnce(ctx); err != nil && ctx.Err() == nil {
			s.log.Warn("hosting preview reconciliation pending")
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (h hostingHost) HostingHistoryEnabled() bool {
	return h.s.runtimeBool(runtimeConfigFrontendHostingHistory, false)
}
func (h hostingHost) HostingHistoryStore() state.HostingHistoryStore {
	store, _ := h.s.store.(state.HostingHistoryStore)
	return store
}

func (h hostingHost) HostingAutomationEnabled() bool {
	return h.s.runtimeBool(runtimeConfigFrontendHostingAutomation, false)
}
func (h hostingHost) HostingAutomationStore() state.HostingAutomationStore {
	store, _ := h.s.store.(state.HostingAutomationStore)
	return store
}

func (h hostingHost) HostingRetentionEnabled() bool {
	return h.s.runtimeBool(runtimeConfigFrontendHostingRetention, false)
}
func (h hostingHost) HostingRetentionStore() state.HostingRetentionStore {
	store, _ := h.s.store.(state.HostingRetentionStore)
	return store
}
func (s *server) runHostingRetentionReconciler(ctx context.Context) {
	h := hostingHost{s}
	store := h.HostingRetentionStore()
	if store == nil {
		return
	}
	reconciler := hostingapi.RetentionReconciler{Store: store, OriginID: h.HostingOriginID(), Enabled: func() bool { return h.HostingEnabled() && h.HostingRetentionEnabled() },
		Authorize: func(ctx context.Context, id string) (bool, error) {
			a, err := s.store.AccountByID(ctx, id)
			if err != nil {
				return false, err
			}
			allowed, _ := s.adminAllows(a)
			return allowed && a.Active(), nil
		},
		Audit: func(ctx context.Context, account, site string, ids []string) {
			h.HostingAudit(ctx, account, "hosting_retention_releases_retired", map[string]any{"site_id": site, "release_ids": ids})
		}}
	ticker := time.NewTicker(api.HostingReconcileInterval)
	defer ticker.Stop()
	for {
		if err := reconciler.RunOnce(ctx); err != nil && ctx.Err() == nil {
			s.log.Warn("hosting retention pass incomplete")
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (h hostingHost) HostingOIDCEnabled() bool {
	return h.s.runtimeBool(runtimeConfigFrontendHostingOIDC, false)
}
func (h hostingHost) HostingOIDCStore() state.HostingOIDCStore {
	store, _ := h.s.store.(state.HostingOIDCStore)
	return store
}
func (h hostingHost) HostingOIDCAccount(ctx context.Context, id string) (state.Account, error) {
	return h.s.store.AccountByID(ctx, id)
}

func (h hostingHost) HostingDiagnosticsEnabled() bool {
	return h.s.runtimeBool(runtimeConfigFrontendHostingDiagnostics, false)
}
func (h hostingHost) HostingDiagnosticReader() hostingapi.DiagnosticReader {
	reader, _ := h.s.store.(hostingapi.DiagnosticReader)
	return reader
}

func (h hostingHost) HostingCISetupEnabled() bool {
	return h.s.runtimeBool(runtimeConfigFrontendHostingCISetup, false)
}

func (h hostingHost) HostingActivityEnabled() bool {
	return h.s.runtimeBool(runtimeConfigFrontendHostingActivity, false)
}
func (h hostingHost) HostingActivityReader() state.HostingActivityReader {
	reader, _ := h.s.store.(state.HostingActivityReader)
	return reader
}

func (h hostingHost) HostingPreviewReservationsEnabled() bool {
	return h.s.runtimeBool(runtimeConfigFrontendHostingPreviewReservations, false)
}
func (h hostingHost) HostingPreviewCapacityReader() state.HostingPreviewCapacityReader {
	reader, _ := h.s.store.(state.HostingPreviewCapacityReader)
	return reader
}

func (h hostingHost) HostingCancellationEnabled() bool {
	return h.s.runtimeBool(runtimeConfigFrontendHostingCancellation, false)
}
func (h hostingHost) HostingCancellationStore() state.HostingCancellationStore {
	store, _ := h.s.store.(state.HostingCancellationStore)
	return store
}
