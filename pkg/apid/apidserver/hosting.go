package apidserver

import (
	"context"
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

func (s *server) runHostingReconciler(ctx context.Context) {
	if s.hostingConfig == nil {
		return
	}
	host := hostingHost{s}
	store := host.HostingStore()
	if store == nil {
		return
	}
	ticker := time.NewTicker(api.HostingReconcileInterval)
	defer ticker.Stop()
	for {
		reconciler := hostingapi.Reconciler{Store: store, Artifacts: s.hostingConfig.Artifacts, LeasedRoutes: s.hostingConfig.Routes, DurableRoutes: s.hostingConfig.Artifacts, AdmissionEnabled: host.HostingEnabled, Log: s.log}
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
