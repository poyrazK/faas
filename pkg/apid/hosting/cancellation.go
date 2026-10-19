package hosting

import (
	"context"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/hosting"
	"github.com/onebox-faas/faas/pkg/state"
)

type CancellationHost interface {
	HostingCancellationEnabled() bool
	HostingCancellationStore() state.HostingCancellationStore
}

func (s Service) cancellationEnabled() bool {
	h, ok := s.Host.(CancellationHost)
	a, automation := s.Host.(AutomationHost)
	return ok && h.HostingCancellationEnabled() && h.HostingCancellationStore() != nil && automation && a.HostingAutomationEnabled() && a.HostingAutomationStore() != nil && s.Host.HostingDurableRoutingEnabled()
}
func (s Service) CancelCIRun(w http.ResponseWriter, r *http.Request, a state.Account) {
	if !s.guard(w, a) {
		return
	}
	if !s.cancellationEnabled() {
		api.WriteProblem(w, api.NewProblem(503, "frontend_hosting_cancellation_disabled", "CI cancellation unavailable", "internal operator cancellation or its automation and durable-routing prerequisites are disabled or unconfigured"))
		return
	}
	id := r.PathValue("run")
	if !hosting.ValidCIRunID(id) {
		api.WriteProblem(w, api.ErrValidation("provide the exact canonical CI run UUID"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.HostingCancellationTimeout)
	defer cancel()
	r = r.WithContext(ctx)
	site, ok := s.site(w, r, a)
	if !ok {
		return
	}
	run, err := s.Host.(CancellationHost).HostingCancellationStore().CancelHostingCIRun(ctx, a.ID, site.ID, id)
	if err != nil {
		failure(w, err)
		return
	}
	s.Host.HostingAudit(ctx, a.ID, "hosting_ci_run_canceled", map[string]any{"site_id": site.ID, "run_id": run.ID, "release_id": run.ReleaseID, "preview_id": run.PreviewID})
	httpkit.WriteJSON(w, 200, run)
}
