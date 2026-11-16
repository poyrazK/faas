package apidserver

// Dashboard page for customer-granted support access (ADR-20261116163508):
// owners and admins grant, revoke, approve and decline here.

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/httpsec"
	csrf "github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

const dashboardSupportAccessAction = "support_access"
const dashboardSupportAccessCSRFCookie = "faas_support_access_csrf"

// supportManagedOrgs lists the organizations acct may manage support access
// for: those it owns or administers, and its personal organization.
func (s *server) supportManagedOrgs(r *http.Request, acct state.Account) []state.Org {
	orgs, err := s.store.ListOrgsForAccount(r.Context(), acct.ID)
	if err != nil {
		return nil
	}
	var out []state.Org
	for _, org := range orgs {
		if s.managesSupportAccess(r, acct, org) {
			out = append(out, org)
		}
	}
	return out
}

func (s *server) managesSupportAccess(r *http.Request, acct state.Account, org state.Org) bool {
	if org.Personal {
		return org.PersonalOwnerAccountID != nil && *org.PersonalOwnerAccountID == acct.ID
	}
	mem, err := s.store.OrgMemberByAccount(r.Context(), org.ID, acct.ID)
	return err == nil && mem.RemovedAt == nil && (mem.Role == state.OrgRoleOwner || mem.Role == state.OrgRoleAdmin)
}

func (s *server) renderSupportAccessDashboard(w http.ResponseWriter, r *http.Request) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		writeDashboardUnauthorized(w, r)
		return
	}
	svc := s.supportAccessService()
	if svc == nil {
		s.notFound(w, "support access unavailable")
		return
	}
	actor := actorAccount(r, acct)
	data := dashboard.SupportAccessData{Flash: supportAccessDone[r.URL.Query().Get("done")], Error: supportAccessErrors[r.URL.Query().Get("error")]}
	for _, org := range s.supportManagedOrgs(r, actor) {
		access, err := svc.Overview(r.Context(), org, actor)
		if err != nil {
			renderProblem(w, s.log, err)
			return
		}
		item := dashboard.SupportAccessOrg{Slug: org.Slug, Name: org.Name, Personal: org.Personal, Enabled: access.Enabled, Access: access}
		if lister, ok := s.store.(state.OrgAppLister); ok {
			if apps, err := lister.ListAppsByOrg(r.Context(), org.ID); err == nil {
				for _, app := range apps {
					item.Apps = append(item.Apps, app.Slug)
				}
			}
		}
		data.Orgs = append(data.Orgs, item)
	}
	token, err := csrf.IssueForAuthenticatedNamed(s.sessions, dashboardSupportAccessAction, actor.ID, dashboardSupportAccessCSRFCookie)
	if err != nil {
		renderProblem(w, s.log, err)
		return
	}
	data.CSRFToken = token
	http.SetCookie(w, &http.Cookie{Name: dashboardSupportAccessCSRFCookie, Value: token, Path: "/", HttpOnly: true, Secure: s.domain != "", SameSite: http.SameSiteLaxMode, MaxAge: int(csrf.DefaultCSRFTTL.Seconds())})
	w.Header().Set("Cache-Control", "no-store")
	page := dashboard.Page{Title: "Support access", Body: "support_access", Account: dashboardAccountView(acct, 0), Data: data}
	if err := dashboard.Render(w, s.log, httpsec.NonceFromContext(r.Context()), page); err != nil {
		renderProblem(w, s.log, err)
	}
}

// dashboardSupportAccessPost serves every form on the page.
func (s *server) dashboardSupportAccessPost(w http.ResponseWriter, r *http.Request) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		writeDashboardUnauthorized(w, r)
		return
	}
	actor := actorAccount(r, acct)
	if err := csrf.VerifyAuthenticatedNamed(s.sessions, r, dashboardSupportAccessAction, actor.ID, dashboardSupportAccessCSRFCookie); err != nil {
		http.Error(w, "reload the support access page and try again", http.StatusForbidden)
		return
	}
	svc := s.supportAccessService()
	org, err := s.store.OrgBySlug(r.Context(), r.PathValue("org"))
	if svc == nil || err != nil || !s.managesSupportAccess(r, actor, org) {
		s.notFound(w, "no such organization")
		return
	}
	prob, done := s.applySupportAccessForm(r, actor, org)
	target := "/dashboard/support-access?done=" + done
	if prob != nil {
		target = "/dashboard/support-access?error=" + supportAccessErrorKey(prob)
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

// The page shows only these fixed messages: a link cannot put its own text
// in a first-party banner.
var supportAccessDone = map[string]string{
	"granted":  "Support access granted.",
	"approved": "Request approved. Gregale support has access until the time shown.",
	"revoked":  "Support access revoked.",
	"declined": "Request declined.",
}

var supportAccessErrors = map[string]string{
	"invalid":   "That did not work: check the app slugs, the duration and the reason.",
	"forbidden": "That did not work: support access is not available, or you cannot change it.",
	"not_found": "That grant or request no longer exists, or has already ended or been decided.",
	"conflict":  "That did not work: the request limit was reached.",
	"failed":    "That did not work. Try again in a moment.",
}

func supportAccessErrorKey(prob *api.Problem) string {
	switch prob.Status {
	case http.StatusBadRequest:
		return "invalid"
	case http.StatusForbidden:
		return "forbidden"
	case http.StatusNotFound:
		return "not_found"
	case http.StatusConflict:
		return "conflict"
	default:
		return "failed"
	}
}

func (s *server) applySupportAccessForm(r *http.Request, actor state.Account, org state.Org) (*api.Problem, string) {
	svc := s.supportAccessService()
	ctx := r.Context()
	id := r.PathValue("id")
	switch {
	case strings.HasSuffix(r.URL.Path, "/revoke"):
		return svc.Revoke(ctx, actor, org, id), "revoked"
	case strings.HasSuffix(r.URL.Path, "/decline"):
		return svc.Decline(ctx, actor, org, id), "declined"
	case strings.HasSuffix(r.URL.Path, "/approve"):
		_, prob := svc.Approve(ctx, actor, org, id, supportGrantForm(r))
		return prob, "approved"
	default:
		_, prob := svc.Grant(ctx, actor, org, supportGrantForm(r))
		return prob, "granted"
	}
}

func supportGrantForm(r *http.Request) api.CreateSupportGrantRequest {
	req := api.CreateSupportGrantRequest{Reason: strings.TrimSpace(r.PostFormValue("reason"))}
	req.TTLHours, _ = strconv.Atoi(r.PostFormValue("ttl_hours"))
	for _, slug := range strings.Split(r.PostFormValue("apps"), ",") {
		if slug = strings.TrimSpace(slug); slug != "" {
			req.Apps = append(req.Apps, slug)
		}
	}
	return req
}
