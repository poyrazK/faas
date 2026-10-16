package hosting

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	artifacts "github.com/onebox-faas/faas/pkg/hosting"
	"github.com/onebox-faas/faas/pkg/state"
	"golang.org/x/sync/errgroup"
)

type RoutingArtifacts interface {
	ID() string
	Remove(context.Context, string, string, api.HostingManifest) error
}

type Reconciler struct {
	Store                       state.HostingStore
	Artifacts                   RoutingArtifacts
	LeasedRoutes, DurableRoutes artifacts.RoutePublisher
	AdmissionEnabled            func() bool
	Log                         *slog.Logger
	Metrics                     *Metrics
	Now                         func() time.Time
}

func (r Reconciler) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now().UTC()
}

func (r Reconciler) RunOnce(ctx context.Context) (resultErr error) {
	defer func() {
		r.Metrics.observePass(resultErr, ctx.Err() != nil, r.now())
		if r.Metrics != nil {
			readCtx, cancel := context.WithTimeout(ctx, api.HostingRouteStatusReadTimeout)
			defer cancel()
			backlog, err := r.Store.HostingRouteBacklog(readCtx)
			r.Metrics.observeBacklog(backlog, err, r.now())
		}
	}()
	// Due work persists across process restarts. An individual failed site must
	// not prevent publication or cleanup for other accounts.
	var failures []error
	ids, err := r.Store.ListDueHostingRoutePublications(ctx, r.now(), api.HostingRoutePageSize)
	if err != nil {
		return err
	}
	var publications errgroup.Group
	publications.SetLimit(api.HostingRoutePublishConcurrency)
	dispatchCtx, cancel := context.WithTimeout(ctx, api.HostingRouteDispatchTimeout)
	deadline, _ := dispatchCtx.Deadline()
	for _, id := range ids {
		if dispatchCtx.Err() != nil || !time.Now().Before(deadline) {
			break
		}
		publications.Go(func() error {
			if dispatchCtx.Err() != nil {
				return dispatchCtx.Err()
			}
			if !time.Now().Before(deadline) {
				return context.DeadlineExceeded
			}
			return r.publish(dispatchCtx, ctx, id)
		})
	}
	if err = publications.Wait(); err != nil {
		failures = append(failures, err)
	}
	cancel()
	var after string
	for {
		sites, listErr := r.Store.ListHostingRoutes(ctx, after, api.HostingRoutePageSize)
		if listErr != nil {
			return errors.Join(append(failures, listErr)...)
		}
		for _, site := range sites {
			if err = r.reconcileSite(ctx, site); err != nil {
				failures = append(failures, err)
			}
		}
		if len(sites) < api.HostingRoutePageSize {
			return errors.Join(failures...)
		}
		after = sites[len(sites)-1].ID
	}
}

func (r Reconciler) publish(ctx, receiptCtx context.Context, id string) error {
	snapshot, err := r.Store.HostingRouteSnapshot(ctx, id)
	if errors.Is(err, state.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	started, outcome := time.Now(), api.HostingRouteErrorProvider
	defer func() { r.Metrics.observePublication(outcome, time.Since(started)) }()
	if r.DurableRoutes == nil || snapshot.Site.OriginID != r.Artifacts.ID() {
		outcome = api.HostingRouteErrorOrigin
		err = errors.New("durable hosting origin is unavailable")
	} else {
		publishCtx, cancel := context.WithTimeout(ctx, api.HostingStorageTimeout)
		err = r.DurableRoutes.Put(publishCtx, artifacts.NewDurableRoute(snapshot.Site, snapshot.Releases, snapshot.Generation))
		cancel()
	}
	if err != nil {
		if errors.Is(err, artifacts.ErrStaleRoute) {
			outcome = api.HostingRouteErrorStale
		}
		if r.Log != nil {
			r.Log.Warn("hosting route publication pending", "site_id", id, "generation", snapshot.Generation)
		}
		// A dispatch deadline must not discard the persistent retry schedule.
		retryErr := r.Store.RetryHostingRoute(receiptCtx, id, snapshot.Generation, r.now(), outcome)
		return errors.Join(err, retryErr)
	}
	err = r.Store.AcknowledgeHostingRoute(receiptCtx, id, snapshot.Generation, r.now())
	outcome = "published"
	if err != nil {
		outcome = "receipt_unavailable"
	}
	return err
}

func (r Reconciler) reconcileSite(ctx context.Context, site api.HostingSite) error {
	releases, err := r.Store.ListHostingReleases(ctx, site.AccountID, site.ID)
	if err != nil {
		return err
	}
	matching := site.OriginID == r.Artifacts.ID()
	if !site.DurableRouting {
		route := artifacts.NewRoute(site, releases, r.AdmissionEnabled() && matching, r.now())
		if err = r.LeasedRoutes.Put(ctx, route); err != nil {
			return err
		}
	}
	if !matching {
		return nil
	}
	// Preview references must be checked before deleting bytes, including while
	// their management flag is off. Creation is fenced by the site lock/state.
	var previews []api.HostingPreview
	if store, ok := r.Store.(state.HostingPreviewStore); ok {
		previews, err = store.ListHostingPreviews(ctx, site.AccountID, site.ID)
		if err != nil {
			return err
		}
	}
	for _, release := range releases {
		pinned := false
		for _, p := range previews {
			if p.ReleaseID == release.ID {
				pinned = true
				break
			}
		}
		if pinned {
			continue
		}
		since := release.RetiredAt
		if site.DurableRouting {
			since = release.RetirementPublishedAt
		}
		if release.State != "retiring" || since == nil || r.now().Sub(*since) < api.HostingRetirementGrace {
			continue
		}
		if err = r.Artifacts.Remove(ctx, site.ID, release.ID, release.Manifest); err != nil {
			return err
		}
		if err = r.Store.DeleteHostingReleaseRecord(ctx, site.AccountID, site.ID, release.ID); err != nil {
			return err
		}
		if r.Log != nil {
			r.Log.Info("hosting release artifacts retired", "site_id", site.ID, "release_id", release.ID)
		}
	}
	if site.Status != "deleting" {
		return nil
	}
	remaining, err := r.Store.ListHostingReleases(ctx, site.AccountID, site.ID)
	if err != nil || len(remaining) > 0 {
		return err
	}
	if site.DurableRouting {
		snapshot, snapshotErr := r.Store.HostingRouteSnapshot(ctx, site.ID)
		if snapshotErr != nil {
			return snapshotErr
		}
		if snapshot.PublishedGeneration != snapshot.Generation || snapshot.PublishedAt.IsZero() || r.now().Sub(snapshot.PublishedAt) < api.HostingRetirementGrace {
			return nil
		}
	}
	return r.Store.PurgeHostingSite(ctx, site.AccountID, site.ID)
}
