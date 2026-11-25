package apidserver

// Operator disk pressure report (ADR-20261124150000).
//
//	GET /v1/admin/obs/disk-pressure

import (
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/diskpressure"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) obsDiskPressure(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if allowed, prob := s.adminAllows(acct); !allowed {
		api.WriteProblem(w, prob)
		return
	}
	if r.URL.RawQuery != "" {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid disk pressure query", "disk pressure accepts no query parameters"))
		return
	}
	ctx := r.Context()
	now := time.Now().UTC()
	in := diskpressure.Input{Now: now, Samples: diskpressure.Read(ctx, s.promqlClient, now)}
	in.Lookup = func(instanceID string) (diskpressure.GuestInfo, bool) {
		ins, err := s.store.InstanceByID(ctx, instanceID)
		if err != nil {
			return diskpressure.GuestInfo{}, false
		}
		info := diskpressure.GuestInfo{AppID: ins.AppID}
		if app, err := s.store.AppByID(ctx, ins.AppID); err == nil {
			info.AppSlug, info.AccountID = app.Slug, app.AccountID
		}
		return info, true
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, diskpressure.Build(in))
}
