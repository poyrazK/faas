package hosting

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// WaitPublication watches exactly the accepted revision. A newer revision never
// substitutes for the one the operator published. ctx bounds every HTTP read.
func WaitPublication(ctx context.Context, read func(context.Context, string) (api.HostingSiteStatus, error), accepted api.HostingSite) (api.HostingPublicationObservation, error) {
	out := api.HostingPublicationObservation{Site: accepted, Publication: api.HostingPublicationStatus{State: "unknown"}, WaitState: "pending"}
	if !accepted.DurableRouting {
		out.WaitState = "untracked"
		return out, errors.New("leased sites have no durable publication receipt")
	}
	for {
		if ctx.Err() != nil {
			out.WaitState = "timeout"
			return out, errors.New("publication wait ended; accepted intent remains committed")
		}
		status, err := read(ctx, accepted.ID)
		if err != nil {
			out.WaitState = "unavailable"
			if ctx.Err() != nil {
				out.WaitState = "timeout"
				return out, errors.New("publication wait ended; accepted intent remains committed")
			}
			return out, errors.New("publication status unavailable; inspect site status before retrying")
		}
		if status.Site.ID != accepted.ID || status.Site.OriginID != accepted.OriginID {
			out.WaitState = "unavailable"
			return out, errors.New("publication status does not match the accepted site")
		}
		out.Site, out.Publication = status.Site, status.Publication
		if status.Site.Revision != accepted.Revision || status.Site.ActiveReleaseID != accepted.ActiveReleaseID || status.Site.Status != "active" || status.Site.Suspended != accepted.Suspended {
			out.WaitState = "superseded"
			return out, errors.New("site intent changed while waiting; review the current revision")
		}
		p := status.Publication
		if p.State == "untracked" {
			out.WaitState = "untracked"
			return out, errors.New("publication is untracked")
		}
		if p.Generation < 1 || p.PublishedGeneration < 0 || p.PublishedGeneration > p.Generation || (p.State != "pending" && p.State != "published") || p.State == "published" && (p.PublishedGeneration != p.Generation || p.PublishedAt == nil) {
			out.WaitState = "unavailable"
			return out, errors.New("invalid publication receipt")
		}
		if p.State == "published" {
			out.WaitState = "published"
			return out, nil
		}
		timer := time.NewTimer(api.HostingPublicationPollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			out.WaitState = "timeout"
			return out, errors.New("publication wait ended; accepted intent remains committed")
		case <-timer.C:
		}
	}
}
