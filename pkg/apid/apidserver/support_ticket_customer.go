package apidserver

import (
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/support"
	"github.com/onebox-faas/faas/pkg/apid/supportportal"
	"github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

// supportTicketCustomer serves GET /v1/admin/support/tickets/{ticket}/customer
// (ADR-20261115175927): the account panel the staff ticket page shows, for
// the operator console's support inbox. Only support staff may read it, under
// the same roster, MFA and session checks as every staff route.
func (s *server) supportTicketCustomer(w http.ResponseWriter, r *http.Request, _ state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	if s.customerSupport == nil || s.customerSupport.Pool == nil {
		api.WriteProblem(w, api.NewProblem(http.StatusServiceUnavailable, "support_unavailable", "Support unavailable", "Customer support is not configured on this apid."))
		return
	}
	actor, key, ok := middleware.AccountFromContext(r)
	if !ok || key != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusUnauthorized, api.CodeUnauthorized, "Unauthorized", "Sign in is required."))
		return
	}
	if _, err := s.customerSupport.StaffRole(r.Context(), support.Actor{ID: actor.ID, Session: true}); err != nil {
		var problem *api.Problem
		if !errors.As(err, &problem) {
			problem = api.NewProblem(http.StatusServiceUnavailable, "support_unavailable", "Support unavailable", "The support inbox could not be loaded. Please try again shortly.")
		}
		api.WriteProblem(w, problem)
		return
	}
	ticketID := r.PathValue("ticket")
	if _, err := uuid.Parse(ticketID); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Ticket not found", "no support ticket has this id"))
		return
	}
	customer, err := s.customerSupport.TicketCustomer(r.Context(), ticketID)
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusServiceUnavailable, "support_unavailable", "Support unavailable", "The ticket could not be loaded. Please try again shortly."))
		return
	}
	if customer == (support.TicketCustomer{}) {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Ticket not found", "no support ticket has this id"))
		return
	}
	writeJSON(w, http.StatusOK, ticketCustomerResponse(ticketID, s.supportCustomerContext(r, ticketID)))
}

func ticketCustomerResponse(ticketID string, panel *supportportal.CustomerContext) api.ObsSupportTicketCustomer {
	out := api.ObsSupportTicketCustomer{TicketID: ticketID, Unavailable: panel.Unavailable}
	if panel.Unavailable {
		return out
	}
	out.AccountID, out.OrgSlug, out.Plan, out.Status, out.Apps = panel.AccountID, panel.OrgSlug, panel.Plan, panel.Status, panel.Apps
	diagnosis := panel.Diagnosis
	out.Diagnosis = &diagnosis
	out.Lifted, out.Added = panel.Lifted, panel.Added
	if snap := panel.Snapshot; snap != nil {
		out.Snapshot = &api.ObsSupportTicketSnapshot{CapturedAt: snap.CapturedAt, Plan: snap.Plan, Status: snap.Status,
			Health: snap.Health, Request: snap.Request, RequestID: snap.RequestID}
	}
	return out
}
