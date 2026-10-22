// adr: 20261020085706 — the approval page Slack and email buttons open.
package apidserver

import (
	"net/http"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/httpsec"
)

// renderReleaseApprovalPage serves GET /dashboard/releases/{id}/approval.
// It renders no release data: the page's script reads and decides through
// /v1/deployments/{id}/approvals with the signed-in session, so the API's
// organization access, role, MFA and self-approval checks decide what the
// person may see and do.
func (s *server) renderReleaseApprovalPage(w http.ResponseWriter, r *http.Request) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		writeDashboardUnauthorized(w, r)
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	intent := r.URL.Query().Get("intent")
	if intent != "approve" && intent != "reject" {
		intent = ""
	}
	w.Header().Set("Referrer-Policy", "no-referrer")
	page := dashboard.Page{Title: "Release approval", Body: "release_approval", Account: dashboardAccountView(acct, 0),
		Data: dashboard.ReleaseApprovalPageData{DeploymentID: id.String(), Intent: intent}}
	if err := dashboard.Render(w, s.log, httpsec.NonceFromContext(r.Context()), page); err != nil {
		renderProblem(w, s.log, err)
	}
}
