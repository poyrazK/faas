package apidserver

import (
	"encoding/hex"
	"net/http"

	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/httpsec"
	"github.com/onebox-faas/faas/pkg/middleware"
)

func (h *cliAuthHandlers) renderPromotionApprovalPage(w http.ResponseWriter, r *http.Request) {
	h.promotionReviewHeaders(w)
	ticket, err := h.srv.openBrowserApprovalTicket(r.URL.Query().Get("approval"))
	acct, authenticated := AccountFrom(r.Context())
	if err != nil || !authenticated || ticket.AccountID != acct.ID {
		h.renderPromotionReview(w, r, map[string]any{"Error": "This review is unavailable for the signed-in account or has expired."})
		return
	}
	plan, problem := h.srv.checkedBrowserApproval(r.Context(), acct, ticket)
	if problem != nil {
		h.renderPromotionReview(w, r, map[string]any{"Error": "The promotion changed or is blocked. Ask the agent for a new plan."})
		return
	}
	hash, _ := hex.DecodeString(ticket.ExchangeHash)
	_, claimed, err := h.srv.store.PeekCliAuthCode(r.Context(), hash)
	if err != nil || claimed != "" {
		h.renderPromotionReview(w, r, map[string]any{"Error": "This review was already approved or has expired."})
		return
	}
	csrf, err := middleware.IssueForAuthenticated(h.srv.sessions, browserApprovalPurpose, acct.ID+":"+ticket.ExchangeHash)
	if err != nil {
		renderProblem(w, h.log, err)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: middleware.CookieNameAuthenticated, Value: csrf, Path: "/", HttpOnly: true, Secure: h.domain != "", SameSite: http.SameSiteLaxMode, MaxAge: int(cliAuthCodeTTL.Seconds())})
	h.renderPromotionReview(w, r, map[string]any{"Ticket": r.URL.Query().Get("approval"), "CSRFToken": csrf, "Account": acct.Email, "AccountID": acct.ID, "Project": ticket.Project, "ProjectID": ticket.ProjectID, "From": plan.Preview.FromEnvironment, "FromID": ticket.FromID, "To": ticket.Environment, "ToID": ticket.EnvironmentID, "Hash": plan.Preview.PromotionHash, "Reason": ticket.Reason, "Changes": plan.Preview.Changes, "ExpiresAt": ticket.ExpiresAt})
}

func (h *cliAuthHandlers) postPromotionApprovalPage(w http.ResponseWriter, r *http.Request) {
	h.promotionReviewHeaders(w)
	ticket, err := h.srv.openBrowserApprovalTicket(r.PostForm.Get("approval"))
	acct, authenticated := AccountFrom(r.Context())
	if err != nil || !authenticated || ticket.AccountID != acct.ID || middleware.VerifyAuthenticated(h.srv.sessions, r, browserApprovalPurpose, acct.ID+":"+ticket.ExchangeHash) != nil {
		w.WriteHeader(http.StatusForbidden)
		h.renderPromotionReview(w, r, map[string]any{"Error": "The review form is invalid or expired. Reload the review link."})
		return
	}
	if _, problem := h.srv.checkedBrowserApproval(r.Context(), acct, ticket); problem != nil {
		w.WriteHeader(http.StatusConflict)
		h.renderPromotionReview(w, r, map[string]any{"Error": "The promotion changed or is blocked. Ask the agent for a new plan."})
		return
	}
	hash, _ := hex.DecodeString(ticket.ExchangeHash)
	if err := h.srv.store.ClaimCliAuthCode(r.Context(), hash, acct.ID); err != nil {
		w.WriteHeader(http.StatusConflict)
		h.renderPromotionReview(w, r, map[string]any{"Error": "This review was already approved or has expired."})
		return
	}
	h.srv.audit.Emit(r.Context(), "mcp.promotion.reviewed", &acct.ID, map[string]any{"project_id": ticket.ProjectID, "environment_id": ticket.EnvironmentID})
	h.renderPromotionReview(w, r, map[string]any{"Approved": true})
}

func (h *cliAuthHandlers) promotionReviewHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
}

func (h *cliAuthHandlers) renderPromotionReview(w http.ResponseWriter, r *http.Request, data map[string]any) {
	page := dashboard.Page{Title: "Review protected promotion", Body: "mcp_approval", Data: data}
	if err := dashboard.Render(w, h.log, httpsec.NonceFromContext(r.Context()), page); err != nil {
		renderProblem(w, h.log, err)
	}
}
