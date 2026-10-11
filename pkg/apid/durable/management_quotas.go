package durable

import (
	"context"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
	"time"
)

func (s *Service) InspectEntityTenantQuotas(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if s.TenantQuotas == nil {
		api.WriteProblem(w, api.ErrCapacity("tenant quota preview is disabled"))
		return
	}
	app, ok := s.Host.LoadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	if !s.Apps[app.ID] {
		api.WriteProblem(w, api.ErrCapacity("app is not enabled for durable entities"))
		return
	}
	selectors, err := InspectionQuery(r.URL.RawQuery)
	if err != nil {
		api.WriteProblem(w, api.ErrValidation("provide exact entity selectors"))
		return
	}
	id, _, problem := s.ResolveIdentity(r, acct, app, api.DurableEntityInvokeRequest{Namespace: selectors.Namespace, Key: selectors.Key, Environment: selectors.Environment, PlatformTenantID: selectors.PlatformTenantID}, true)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	usage, err := s.TenantQuotas.Inspect(ctx, id)
	if err != nil {
		WriteTenantQuotaProblem(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpkit.WriteJSON(w, http.StatusOK, usage)
}
