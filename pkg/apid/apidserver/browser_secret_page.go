package apidserver

import (
	"encoding/hex"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/httpsec"
	"github.com/onebox-faas/faas/pkg/middleware"
)

// renderSecretEntryPage shows the one-time secret form (ADR-20261026174727).
// Only the signed-in member who requested it sees it, while they may still
// manage the app's secrets.
func (h *cliAuthHandlers) renderSecretEntryPage(w http.ResponseWriter, r *http.Request) {
	h.promotionReviewHeaders(w)
	ticket, err := h.srv.openSecretEntryTicket(r.URL.Query().Get("secret"))
	acct, authenticated := AccountFrom(r.Context())
	if err != nil || !authenticated || ticket.actorID() != acct.ID {
		h.renderSecretEntry(w, r, map[string]any{"Error": "This request is unavailable for the signed-in account or has expired."})
		return
	}
	if _, _, _, ok := h.srv.secretEntryApp(r, acct, ticket); !ok {
		h.renderSecretEntry(w, r, map[string]any{"Error": "The app changed. Ask the agent for a new request."})
		return
	}
	hash, _ := hex.DecodeString(ticket.ExchangeHash)
	if _, claimed, err := h.srv.store.PeekCliAuthCode(r.Context(), hash); err != nil || claimed != "" {
		h.renderSecretEntry(w, r, map[string]any{"Error": "This request was already used or has expired."})
		return
	}
	csrf, err := middleware.IssueForAuthenticated(h.srv.sessions, secretEntryPurpose, acct.ID+":"+ticket.ExchangeHash)
	if err != nil {
		renderProblem(w, h.log, err)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: middleware.CookieNameAuthenticated, Value: csrf, Path: "/", HttpOnly: true, Secure: h.domain != "", SameSite: http.SameSiteLaxMode, MaxAge: int(secretEntryTTL.Seconds())})
	h.renderSecretEntry(w, r, map[string]any{"Ticket": r.URL.Query().Get("secret"), "CSRFToken": csrf, "Account": acct.Email, "AccountID": acct.ID, "App": ticket.App, "AppID": ticket.AppID, "Key": ticket.Key, "Scope": ticket.Scope, "Reason": ticket.Reason, "ExpiresAt": ticket.ExpiresAt})
}

// postSecretEntryPage stores the value, then consumes the request. A value the
// store refuses leaves the request open, so the owner can correct it.
func (h *cliAuthHandlers) postSecretEntryPage(w http.ResponseWriter, r *http.Request) {
	h.promotionReviewHeaders(w)
	ticket, err := h.srv.openSecretEntryTicket(r.PostForm.Get("secret"))
	acct, authenticated := AccountFrom(r.Context())
	if err != nil || !authenticated || ticket.actorID() != acct.ID || middleware.VerifyAuthenticated(h.srv.sessions, r, secretEntryPurpose, acct.ID+":"+ticket.ExchangeHash) != nil {
		w.WriteHeader(http.StatusForbidden)
		h.renderSecretEntry(w, r, map[string]any{"Error": "The form is invalid or expired. Reload the request link."})
		return
	}
	app, owner, granted, ok := h.srv.secretEntryApp(r, acct, ticket)
	hash, _ := hex.DecodeString(ticket.ExchangeHash)
	if _, claimed, err := h.srv.store.PeekCliAuthCode(r.Context(), hash); !ok || err != nil || claimed != "" {
		w.WriteHeader(http.StatusConflict)
		h.renderSecretEntry(w, r, map[string]any{"Error": "This request was already used, has expired, or its app changed."})
		return
	}
	value := r.PostForm.Get("value")
	if value == "" {
		w.WriteHeader(http.StatusBadRequest)
		h.renderSecretEntry(w, r, map[string]any{"Error": "Enter a value. Reload the request link to try again."})
		return
	}
	if _, prob := h.srv.setAppSecret(granted, owner, app, ticket.Scope, ticket.Key, api.PutAppSecretRequest{Value: value}); prob != nil {
		status := prob.Status
		if status < http.StatusBadRequest {
			status = http.StatusBadRequest
		}
		w.WriteHeader(status)
		h.renderSecretEntry(w, r, map[string]any{"Error": "Gregale could not save the secret: " + prob.Detail + " Reload the request link to try again."})
		return
	}
	// Two concurrent submissions both store a value; one consumes the request.
	if err := h.srv.store.ClaimCliAuthCode(r.Context(), hash, acct.ID); err != nil {
		h.log.Warn("secret entry: request not consumed after save", "app_id", app.ID, "err", err)
	}
	h.srv.audit.Emit(r.Context(), "secret.entry_completed", &acct.ID, map[string]any{"app_id": app.ID, "name": ticket.Key, "scope": ticket.Scope})
	h.renderSecretEntry(w, r, map[string]any{"Saved": true, "App": ticket.App, "Key": ticket.Key, "Scope": ticket.Scope})
}

func (h *cliAuthHandlers) renderSecretEntry(w http.ResponseWriter, r *http.Request, data map[string]any) {
	page := dashboard.Page{Title: "Set a secret", Body: "mcp_secret", Data: data}
	if err := dashboard.Render(w, h.log, httpsec.NonceFromContext(r.Context()), page); err != nil {
		renderProblem(w, h.log, err)
	}
}
