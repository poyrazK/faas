package apidserver

import (
	"context"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/issuetrackers"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/httpsec"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) dashboardIssueTrackerHealthHandler(w http.ResponseWriter, r *http.Request) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		writeDashboardUnauthorized(w, r)
		return
	}
	policies, app, ok := s.issueTrackerAutoSyncAccess(w, r, acct)
	if !ok {
		return
	}
	if app.AccountID != acct.ID {
		api.WriteProblem(w, api.NewProblem(403, api.CodeForbidden, "Owner required", "only the app owner may inspect sync history"))
		return
	}
	st, ok := s.store.(state.IssueTrackerSyncHealthStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("sync health unavailable"))
		return
	}
	row, err := policies.GetIssueTrackerAutoSync(r.Context(), app.ID, acct.ID, r.PathValue("issue_id"), r.PathValue("link_id"))
	if err != nil {
		writeNativeIssueTrackerError(w, err)
		return
	}
	health, err := st.GetIssueTrackerSyncHealth(r.Context(), app.ID, acct.ID, r.PathValue("issue_id"), r.PathValue("link_id"))
	if err != nil {
		writeNativeIssueTrackerError(w, err)
		return
	}
	token, err := middleware.IssueForAuthenticatedNamed(s.sessions, dashboardIssueAction, acct.ID, dashboardIssueCookie)
	if err != nil {
		renderProblem(w, s.log, err)
		return
	}
	// #nosec G124 -- configured production domains use Secure; empty domain supports local HTTP development.
	http.SetCookie(w, &http.Cookie{Name: dashboardIssueCookie, Value: token, Path: "/", HttpOnly: true, Secure: s.domain != "", SameSite: http.SameSiteLaxMode, MaxAge: int(middleware.DefaultCSRFTTL.Seconds())})
	view := dashboard.NewIssueTrackerSyncHealthView(app.Slug, r.PathValue("issue_id"), row.Policy.LinkID, row.Baseline.Provider, token, row.Policy, health)
	if err := dashboard.Render(w, s.log, httpsec.NonceFromContext(r.Context()), dashboard.Page{Title: "Tracker sync health", Body: "issue_tracker_sync_health", Account: dashboardAccountView(acct, 0), Data: view}); err != nil {
		renderProblem(w, s.log, err)
	}
}

func (s *server) recordDashboardTrackerInspection(r *http.Request, row state.IssueTrackerAutoSyncRecord, guard int64, status api.IssueTrackerProviderStatus, reason string, failed bool) error {
	if row.Policy.Revision == 0 {
		return nil
	}
	st, ok := s.store.(state.IssueTrackerSyncHealthStore)
	if !ok {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), time.Duration(api.IssueTrackerRequestTimeoutSeconds)*time.Second)
	defer cancel()
	return st.RecordIssueTrackerSyncInspection(ctx, state.IssueTrackerSyncInspection{Record: row, GuardRevision: guard, Observed: status, Reason: reason, Failed: failed})
}

func (s *server) recordDashboardTrackerInspectionFailure(r *http.Request, row state.IssueTrackerAutoSyncRecord, guard int64, err error) {
	if e := s.recordDashboardTrackerInspection(r, row, guard, api.IssueTrackerProviderStatus{}, issuetrackers.InspectionFailureReason(err), true); e != nil {
		s.log.Warn("tracker inspection receipt unavailable", "link_id", row.Policy.LinkID)
	}
}
