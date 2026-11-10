package apidserver

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/agentconnections"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/httpsec"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

// Human approval of agents' risky requests (ADR-20261104140058). A person
// decides on the session-authenticated /cli-auth surface; no API key reaches
// these handlers, so an agent cannot approve its own request.

const agentApprovalPurpose = "agent-approval"

// agentApprovalReviewURL is the page where a person decides an agent's
// approval request.
func (s *server) agentApprovalReviewURL(id string) string {
	return strings.TrimRight(s.cliAuthURLBase, "/") + cliAuthPath + "?agent_approval=" + url.QueryEscape(id)
}

var approvalCategoryLabels = map[string]string{
	api.AgentApprovalDelete:      "Delete",
	api.AgentApprovalProduction:  "Change production",
	api.AgentApprovalCredentials: "Mint or rotate credentials",
	api.AgentApprovalBilling:     "Change billing",
}

// decidableApproval loads a request the signed-in person may decide.
func (h *cliAuthHandlers) decidableApproval(r *http.Request, id string) (state.AgentApproval, state.Account, string) {
	acct, ok := AccountFrom(r.Context())
	if !ok || acct.ID == "" {
		return state.AgentApproval{}, acct, "Sign in to Gregale to review this request."
	}
	store, ok := h.srv.store.(state.AgentApprovalStore)
	if !ok {
		return state.AgentApproval{}, acct, "This request is unavailable."
	}
	approval, err := store.AgentApproval(r.Context(), id)
	if err != nil {
		return state.AgentApproval{}, acct, "This request is unavailable."
	}
	may, err := agentconnections.MayDecide(r.Context(), h.srv.store, approval, acct.ID)
	if err != nil || !may {
		return state.AgentApproval{}, acct, "This request is unavailable for the signed-in account."
	}
	if approval.EffectiveStatus(time.Now()) != api.AgentApprovalPending {
		return state.AgentApproval{}, acct, "This request was already decided or has expired."
	}
	return approval, acct, ""
}

func (h *cliAuthHandlers) renderAgentApprovalPage(w http.ResponseWriter, r *http.Request) {
	h.promotionReviewHeaders(w)
	id := r.URL.Query().Get("agent_approval")
	approval, acct, problem := h.decidableApproval(r, id)
	if problem != "" {
		h.renderAgentApproval(w, r, map[string]any{"Error": problem})
		return
	}
	csrf, err := middleware.IssueForAuthenticated(h.srv.sessions, agentApprovalPurpose, acct.ID+":"+approval.ID)
	if err != nil {
		renderProblem(w, h.log, err)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: middleware.CookieNameAuthenticated, Value: csrf, Path: "/", HttpOnly: true, Secure: h.domain != "", SameSite: http.SameSiteLaxMode, MaxAge: int(api.AgentApprovalTTL.Seconds())})
	connectedBy := approval.AccountID
	if member, err := h.srv.store.AccountByID(r.Context(), approval.AccountID); err == nil {
		connectedBy = member.Email
	}
	h.renderAgentApproval(w, r, map[string]any{
		"ID": approval.ID, "CSRFToken": csrf, "Account": acct.Email,
		"Agent": agentconnections.AgentName(r.Context(), h.srv.store, approval), "ConnectedBy": connectedBy,
		"CategoryLabel": approvalCategoryLabels[approval.Category], "Method": approval.Method, "Path": approval.Path,
		"BodyBound": approval.BodySHA256 != nil, "ExpiresAt": approval.ExpiresAt.UTC().Format(time.RFC1123),
	})
}

func (h *cliAuthHandlers) postAgentApprovalPage(w http.ResponseWriter, r *http.Request) {
	h.promotionReviewHeaders(w)
	id := r.PostForm.Get("agent_approval")
	approval, acct, problem := h.decidableApproval(r, id)
	if problem == "" && middleware.VerifyAuthenticated(h.srv.sessions, r, agentApprovalPurpose, acct.ID+":"+approval.ID) != nil {
		problem = "The review form is invalid or expired. Reload the review link."
	}
	if problem != "" {
		w.WriteHeader(http.StatusForbidden)
		h.renderAgentApproval(w, r, map[string]any{"Error": problem})
		return
	}
	approve := r.PostForm.Get("decision") == "approve"
	store := h.srv.store.(state.AgentApprovalStore)
	note := r.PostForm.Get("note") // shown to the agent on a denial (ADR-20261107140000 §6)
	if err := store.DecideAgentApproval(r.Context(), approval.ID, approve, acct.ID, note); err != nil {
		w.WriteHeader(http.StatusConflict)
		message := "This request was already decided or has expired."
		if !errors.Is(err, state.ErrNotFound) {
			message = "Gregale could not record the decision. Reload the review link and try again."
		}
		h.renderAgentApproval(w, r, map[string]any{"Error": message})
		return
	}
	kind := "agent.approval.denied"
	if approve {
		kind = "agent.approval.approved"
	}
	h.srv.audit.Emit(r.Context(), kind, &acct.ID, map[string]any{
		"approval_id": approval.ID, "key_id": approval.KeyID, "org_id": approval.OrgID, "category": approval.Category,
		"method": approval.Method, "path": approval.Path, "connected_by": approval.AccountID,
		"note_given": !approve && strings.TrimSpace(note) != "",
	})
	if approve {
		h.renderAgentApproval(w, r, map[string]any{"Approved": true, "ExpiresAt": approval.ExpiresAt.UTC().Format(time.RFC1123)})
		return
	}
	h.renderAgentApproval(w, r, map[string]any{"Denied": true})
}

func (h *cliAuthHandlers) renderAgentApproval(w http.ResponseWriter, r *http.Request, data map[string]any) {
	page := dashboard.Page{Title: "Review an agent's request", Body: "agent_approval", Data: data}
	if err := dashboard.Render(w, h.log, httpsec.NonceFromContext(r.Context()), page); err != nil {
		renderProblem(w, h.log, err)
	}
}
