package hosting

import (
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s Service) GetStatus(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	site, ok := s.site(w, r, acct)
	if !ok {
		return
	}
	out := api.HostingSiteStatus{Site: site, Publication: api.HostingPublicationStatus{State: "untracked"}}
	if site.DurableRouting {
		snapshot, err := s.Host.HostingStore().HostingRouteSnapshot(r.Context(), site.ID)
		if err != nil {
			failure(w, err)
			return
		}
		if snapshot.Site.AccountID != acct.ID || snapshot.Site.OriginID != s.Host.HostingOriginID() {
			failure(w, state.ErrNotFound)
			return
		}
		out.Site, out.Publication = snapshot.Site, publicationStatus(snapshot)
	}
	httpkit.WriteJSON(w, http.StatusOK, out)
}

func timePointer(at time.Time) *time.Time {
	if at.IsZero() {
		return nil
	}
	return &at
}

func publicationStatus(snapshot state.HostingRouteSnapshot) api.HostingPublicationStatus {
	status := api.HostingPublicationStatus{State: "pending", Generation: snapshot.Generation,
		PublishedGeneration: snapshot.PublishedGeneration, PublishedAt: timePointer(snapshot.PublishedAt),
		PendingSince: timePointer(snapshot.PendingSince), NextAttemptAt: timePointer(snapshot.NextAttemptAt),
		LastAttemptAt: timePointer(snapshot.LastAttemptAt), RetryCount: snapshot.RetryCount, LastErrorCode: snapshot.LastErrorCode}
	if snapshot.PublishedGeneration == snapshot.Generation {
		status.State = "published"
	}
	return status
}
