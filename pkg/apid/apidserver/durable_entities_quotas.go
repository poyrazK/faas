package apidserver

import (
	"context"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/durable"
	"github.com/onebox-faas/faas/pkg/durableentity"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) reserveEntityTenantConnection(w http.ResponseWriter, r *http.Request, id durableentity.ID) (string, func(), bool) {
	if s.durable.TenantQuotas == nil {
		return "", func() {}, true
	}
	quotas := s.durable.TenantQuotas
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	lease, err := quotas.AcquireConnection(ctx, id)
	if err != nil {
		durable.WriteTenantQuotaProblem(w, err)
		return "", nil, false
	}
	return lease, func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Second)
		defer cancel()
		_ = quotas.ReleaseConnection(ctx, id, lease)
	}, true
}
func (s *server) inspectEntityTenantQuotas(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if s.durable.TenantQuotas == nil {
		api.WriteProblem(w, api.ErrCapacity("tenant quota preview is disabled"))
		return
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	if !s.durable.Apps[app.ID] {
		api.WriteProblem(w, api.ErrCapacity("app is not enabled for durable entities"))
		return
	}
	selectors, err := durable.InspectionQuery(r.URL.RawQuery)
	if err != nil {
		api.WriteProblem(w, api.ErrValidation("provide exact entity selectors"))
		return
	}
	id, _, problem := s.durable.ResolveIdentity(r, acct, app, api.DurableEntityInvokeRequest{Namespace: selectors.Namespace, Key: selectors.Key, Environment: selectors.Environment, PlatformTenantID: selectors.PlatformTenantID}, true)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	usage, err := s.durable.TenantQuotas.Inspect(ctx, id)
	if err != nil {
		durable.WriteTenantQuotaProblem(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, usage)
}
