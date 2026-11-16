package supportaccess

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/authz"
	"github.com/onebox-faas/faas/pkg/state"
)

// HTTP mounts the service on the /v1 routes.
type HTTP struct {
	Service *Service
	// Org resolves {slug} and checks the caller's role for action.
	Org func(w http.ResponseWriter, r *http.Request, action authz.OrgAction) (state.Org, bool)
	// BrowserSession refuses an API key on an action only a signed-in
	// person may take.
	BrowserSession func(w http.ResponseWriter, r *http.Request, what string) bool
	// Actor is the person behind the request.
	Actor func(r *http.Request, acct state.Account) state.Account
}

// maxBody bounds a grant body: a hundred slugs and a reason.
const maxBody = 16 << 10

// Overview is GET /v1/orgs/{slug}/support-access. Every member sees who
// gave support access and until when.
func (h HTTP) Overview(w http.ResponseWriter, r *http.Request, acct state.Account) {
	org, ok := h.Org(w, r, authz.OrgActionView)
	if !ok {
		return
	}
	out, err := h.Service.Overview(r.Context(), org, h.Actor(r, acct))
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not read support access"))
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, out)
}

// Grant is POST /v1/orgs/{slug}/support-access/grants.
func (h HTTP) Grant(w http.ResponseWriter, r *http.Request, acct state.Account) {
	org, ok := h.Org(w, r, authz.OrgActionManageSupportAccess)
	if !ok || !h.BrowserSession(w, r, "Granting Gregale support access") {
		return
	}
	var req api.CreateSupportGrantRequest
	if !httpkit.DecodeJSONLimit(w, r, &req, maxBody) {
		return
	}
	out, prob := h.Service.Grant(r.Context(), h.Actor(r, acct), org, req)
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	httpkit.WriteJSON(w, http.StatusCreated, out)
}

// Revoke is POST /v1/orgs/{slug}/support-access/grants/{id}/revoke. A key
// may revoke: taking access away is always safe.
func (h HTTP) Revoke(w http.ResponseWriter, r *http.Request, acct state.Account) {
	org, ok := h.Org(w, r, authz.OrgActionManageSupportAccess)
	if !ok {
		return
	}
	if prob := h.Service.Revoke(r.Context(), h.Actor(r, acct), org, r.PathValue("id")); prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Approve is POST /v1/orgs/{slug}/support-access/requests/{id}/approve.
func (h HTTP) Approve(w http.ResponseWriter, r *http.Request, acct state.Account) {
	org, ok := h.Org(w, r, authz.OrgActionManageSupportAccess)
	if !ok || !h.BrowserSession(w, r, "Approving Gregale support access") {
		return
	}
	var req api.CreateSupportGrantRequest
	if r.ContentLength != 0 && !httpkit.DecodeJSONLimit(w, r, &req, maxBody) {
		return
	}
	out, prob := h.Service.Approve(r.Context(), h.Actor(r, acct), org, r.PathValue("id"), req)
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	httpkit.WriteJSON(w, http.StatusCreated, out)
}

// Decline is POST /v1/orgs/{slug}/support-access/requests/{id}/decline.
func (h HTTP) Decline(w http.ResponseWriter, r *http.Request, acct state.Account) {
	org, ok := h.Org(w, r, authz.OrgActionManageSupportAccess)
	if !ok {
		return
	}
	if prob := h.Service.Decline(r.Context(), h.Actor(r, acct), org, r.PathValue("id")); prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// OperatorRequest is POST /v1/admin/support-access/requests.
func (h HTTP) OperatorRequest(w http.ResponseWriter, r *http.Request, acct state.Account) {
	var req api.OperatorSupportAccessRequest
	if !httpkit.DecodeJSONLimit(w, r, &req, maxBody) {
		return
	}
	out, prob := h.Service.Request(r.Context(), h.Actor(r, acct), req.Org, req.Reason)
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	httpkit.WriteJSON(w, http.StatusCreated, out)
}

// OperatorGrants is GET /v1/admin/support-access/grants.
func (h HTTP) OperatorGrants(w http.ResponseWriter, r *http.Request, _ state.Account) {
	grants, err := h.Service.LiveGrants(r.Context())
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list support grants"))
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, api.SupportGrantList{Grants: grants})
}

// RecordAction writes the customer-visible trail of one request support
// made under a grant: an activity row and an audit event. It reports
// whether the request changed something, for the owners' mail.
func (s *Service) RecordAction(ctx context.Context, operator state.Account, grant state.SupportGrant, app state.App, method, route, deploymentID string, status int) bool {
	mutation := method != http.MethodGet && method != http.MethodHead
	data := map[string]any{"grant_id": grant.ID, "route": route, "status": status}
	if deploymentID != "" {
		data["deployment_id"] = deploymentID
	}
	if app.ID != "" {
		data["app"] = app.Slug
	}
	s.audit(ctx, "support.action", operator.ID, map[string]any{
		"org_id": grant.OrgID, "grant_id": grant.ID, "app_id": app.ID, "route": route, "status": status,
		"deployment_id": deploymentID, "operator": operator.Email, "at": time.Now().UTC().Format(time.RFC3339),
	})
	if mutation {
		s.activity(ctx, grant.OrgID, operator, true, "support_access.action", "app", app.ID, data)
	}
	return mutation && status < 400
}

func activityJSON(data map[string]any) json.RawMessage {
	if data == nil {
		return json.RawMessage(`{}`)
	}
	b, err := json.Marshal(data)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return b
}
