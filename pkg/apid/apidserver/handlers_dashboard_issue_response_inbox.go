package apidserver

import (
	"net/http"
	"net/url"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/issuetriage"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/httpsec"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) dashboardResponseInboxHandler(w http.ResponseWriter, r *http.Request) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		writeDashboardUnauthorized(w, r)
		return
	}
	filter, err := issuetriage.ParseInboxFilter(r.URL.Query())
	if err != nil {
		writeNativeIssueTrackerError(w, err)
		return
	}
	_, app, ok := s.dashboardApplicationTriageAccess(w, r, acct)
	if !ok {
		return
	}
	st, ok := s.store.(state.IssueApplicationResponseInboxStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("issue response inbox unavailable"))
		return
	}
	inbox, err := st.ListIssueApplicationResponses(r.Context(), app.ID, app.AccountID, acct.ID, filter)
	if err != nil {
		writeNativeIssueTrackerError(w, err)
		return
	}
	token, err := s.responseInboxCSRF(w, acct.ID)
	if err != nil {
		renderProblem(w, s.log, err)
		return
	}
	view := dashboard.NewIssueApplicationResponseInboxView(app.Slug, token, filter, inbox)
	if err = dashboard.Render(w, s.log, httpsec.NonceFromContext(r.Context()), dashboard.Page{Title: "Issue responses", Body: "issue_application_response_inbox", Account: dashboardAccountView(acct, 0), Data: view}); err != nil {
		renderProblem(w, s.log, err)
	}
}
func (s *server) responseInboxCSRF(w http.ResponseWriter, actor string) (string, error) {
	token, err := middleware.IssueForAuthenticatedNamed(s.sessions, dashboardIssueAction, actor, dashboardIssueCookie)
	if err == nil {
		// #nosec G124 -- empty domain supports local development; production domains use Secure.
		http.SetCookie(w, &http.Cookie{Name: dashboardIssueCookie, Value: token, Path: "/", HttpOnly: true, Secure: s.domain != "", SameSite: http.SameSiteLaxMode, MaxAge: int(middleware.DefaultCSRFTTL.Seconds())})
	}
	return token, err
}
func applicationResponseRedirect(r *http.Request, slug string) (string, error) {
	if r.PostForm.Get("return_to") == "responses" {
		f, err := issuetriage.ParseInboxFilter(r.PostForm)
		f.AfterID = ""
		return issuetriage.InboxURL(slug, f), err
	}
	return "/dashboard/apps/" + url.PathEscape(slug) + "/issues/" + url.PathEscape(r.PathValue("issue_id")) + "/triage", nil
}
