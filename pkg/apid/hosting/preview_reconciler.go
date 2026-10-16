package hosting

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	artifacts "github.com/onebox-faas/faas/pkg/hosting"
	"github.com/onebox-faas/faas/pkg/state"
	"golang.org/x/sync/errgroup"
)

type PreviewReconciler struct {
	Store state.HostingPreviewStore
	Sites interface {
		GetHostingSite(context.Context, string, string) (api.HostingSite, error)
	}
	Publisher artifacts.PreviewRoutePublisher
	OriginID  string
	Now       func() time.Time
}

func (r PreviewReconciler) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now().UTC()
}

// RunOnce repairs immutable heads even when preview admission is disabled.
// Duplicate dispatch is safe: R2 CAS and generation-fenced receipts are the fence.
func (r PreviewReconciler) RunOnce(ctx context.Context) error {
	ids, err := r.Store.DueHostingPreviews(ctx, r.now())
	if err != nil {
		return err
	}
	work, cancel := context.WithTimeout(ctx, api.HostingRouteDispatchTimeout)
	defer cancel()
	var group errgroup.Group
	group.SetLimit(api.HostingPreviewConcurrency)
	for _, id := range ids {
		if work.Err() != nil {
			break
		}
		group.Go(func() error {
			if work.Err() != nil {
				return work.Err()
			}
			return r.reconcile(work, id)
		})
	}
	return group.Wait()
}
func (r PreviewReconciler) reconcile(ctx context.Context, id string) error {
	p, err := r.Store.PrepareHostingPreview(ctx, id, r.now())
	if errors.Is(err, state.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if p.Revoked && p.PublishedGeneration == p.Generation && p.PublishedAt != nil && r.now().Sub(*p.PublishedAt) >= api.HostingPreviewRemovalGrace {
		return r.Store.PurgeHostingPreview(ctx, p, r.now())
	}
	site, err := r.Sites.GetHostingSite(ctx, p.AccountID, p.SiteID)
	code := ""
	if err != nil || p.OriginID != r.OriginID || site.OriginID != r.OriginID {
		code = api.HostingRouteErrorOrigin
		err = errors.New("preview origin unavailable")
	} else {
		err = r.Publisher.PutPreview(ctx, artifacts.PreviewRoute{Version: 1, ID: p.ID, Hostname: p.Hostname, SiteID: p.SiteID, CanonicalHostname: site.Hostname, OriginID: p.OriginID, ReleaseID: p.ReleaseID, ExpiresAt: p.ExpiresAt.Unix(), Generation: p.Generation, Revoked: p.Revoked})
		if err != nil {
			code = api.HostingRouteErrorProvider
		}
	}
	receipt, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.HostingRouteStatusReadTimeout)
	defer cancel()
	finish := r.Store.FinishHostingPreview(receipt, p, r.now(), code)
	return errors.Join(err, finish)
}
