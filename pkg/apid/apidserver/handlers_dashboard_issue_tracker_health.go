package apidserver

import (
	"context"
	"net/http"
	"time"

	"github.com/google/uuid"
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

	id, err := uuid.Parse(r.PathValue("link_id"))
	if err != nil || id == uuid.Nil {
		api.WriteProblem(w, api.ErrValidation("link_id must be a nonzero UUID"))
		return
	}
	r.SetPathValue("link_id", id.String())
	st, app, ok := s.dashboardTrackerNotificationsAccess(w, r, acct)
	if !ok {
		return
	}
	policies, ok := s.store.(state.IssueTrackerAutoSyncStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("sync health unavailable"))
		return
	}
	row, err := policies.GetIssueTrackerAutoSync(r.Context(), app.ID, app.AccountID, r.PathValue("issue_id"), r.PathValue("link_id"))
	if err != nil {
		writeNativeIssueTrackerError(w, err)
		return
	}
	health, err := st.GetIssueTrackerNotificationHealth(r.Context(), app.ID, app.AccountID, acct.ID, r.PathValue("issue_id"), r.PathValue("link_id"))
	if err != nil {
		writeNativeIssueTrackerError(w, err)
		return
	}
	s.renderDashboardTrackerHealth(w, r, acct, app, row, health)
}

func (s *server) renderDashboardTrackerHealth(w http.ResponseWriter, r *http.Request, acct state.Account, app state.App, row state.IssueTrackerAutoSyncRecord, health state.IssueTrackerSyncHealth) {
	token, err := middleware.IssueForAuthenticatedNamed(s.sessions, dashboardIssueAction, acct.ID, dashboardIssueCookie)
	if err != nil {
		renderProblem(w, s.log, err)
		return
	}
	// #nosec G124 -- configured production domains use Secure; empty domain supports local HTTP development.
	http.SetCookie(w, &http.Cookie{Name: dashboardIssueCookie, Value: token, Path: "/", HttpOnly: true, Secure: s.domain != "", SameSite: http.SameSiteLaxMode, MaxAge: int(middleware.DefaultCSRFTTL.Seconds())})
	view := dashboard.NewIssueTrackerSyncHealthView(app.Slug, r.PathValue("issue_id"), row.Policy.LinkID, row.Baseline.Provider, token, row.Policy, health)
	view.CanManage = app.AccountID == acct.ID
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
