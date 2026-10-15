package apidserver

import (
	"context"
	"log/slog"
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
		if err := reconcileHostingRoutes(ctx, store, s.hostingConfig, host.HostingEnabled, s.log); err != nil && ctx.Err() == nil {
			s.log.Warn("hosting reconciliation failed")
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Intent is durable in hosting_sites. Re-scan rather than depending on a
// best-effort notification; publication errors leave the previous finite lease.
func reconcileHostingRoutes(ctx context.Context, store state.HostingStore, cfg *hosting.Config, enabled func() bool, log *slog.Logger) error {
	var after string
	for {
		sites, err := store.ListHostingRoutes(ctx, after, api.HostingRoutePageSize)
		if err != nil {
			return err
		}
		for _, site := range sites {
			releases, err := store.ListHostingReleases(ctx, site.AccountID, site.ID)
			if err != nil {
				return err
			}
			matchingOrigin := site.OriginID == cfg.Artifacts.ID()
			route := hosting.NewRoute(site, releases, enabled() && matchingOrigin, time.Now())
			if err := cfg.Routes.Put(ctx, route); err != nil {
				return err
			}
			for _, release := range releases {
				if !matchingOrigin {
					continue
				}
				if release.State == "retiring" && release.RetiredAt != nil && time.Since(*release.RetiredAt) >= api.HostingRetirementGrace {
					if err := cfg.Artifacts.Remove(ctx, site.ID, release.ID, release.Manifest); err != nil {
						return err
					}
					if err := store.DeleteHostingReleaseRecord(ctx, site.AccountID, site.ID, release.ID); err != nil {
						return err
					}
					log.Info("hosting release artifacts retired", "site_id", site.ID, "release_id", release.ID)
				}
			}
			if matchingOrigin && site.Status == "deleting" {
				remaining, err := store.ListHostingReleases(ctx, site.AccountID, site.ID)
				if err != nil {
					return err
				}
				if len(remaining) == 0 {
					if err := store.PurgeHostingSite(ctx, site.AccountID, site.ID); err != nil {
						return err
					}
				}
			}
			after = site.ID
		}
		if len(sites) < api.HostingRoutePageSize {
			return nil
		}
	}
}
