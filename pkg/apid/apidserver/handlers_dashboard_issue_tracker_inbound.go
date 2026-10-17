package apidserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/issuetrackers"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/issuetracker"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) dashboardTrackerInboundGuard(w http.ResponseWriter, r *http.Request, app state.App) (int64, bool) {
	store, ok := s.store.(state.IssueTrackerInboundStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("inbound sync unavailable"))
		return 0, false
	}
	guard, err := store.GetIssueTrackerInboundGuard(r.Context(), app.ID, app.AccountID)
	if err != nil {
		writeNativeIssueTrackerError(w, err)
		return 0, false
	}
	return guard, true
}

func (s *server) finishDashboardInboundPreview(w http.ResponseWriter, r *http.Request, app state.App, row state.IssueTrackerAutoSyncRecord, guard int64, inbound issuetrackers.InboundPreview, view *dashboard.IssueTrackerSyncPreview) bool {
	current, ok := s.dashboardTrackerInboundGuard(w, r, app)
	if !ok {
		return false
	}
	if current != guard {
		writeNativeIssueTrackerError(w, state.IssueTrackerInboundChanged())
		return false
	}
	view.InboundGuardRevision = guard
	view.CanPull = (inbound.Outcome == "candidate" || inbound.Outcome == "needs_deployment") && !row.ClaimUntil.After(time.Now())
	if view.CanPull && view.Status.State == "resolved" {
		store := s.store.(state.IssueTrackerInboundStore)
		choices, err := store.ListIssueTrackerInboundDeployments(r.Context(), app.ID, app.AccountID, row.Policy.SourceIssueID)
		if err != nil {
			writeNativeIssueTrackerError(w, err)
			return false
		}
		view.FixingDeployments = dashboard.SuggestFixingDeployments(choices)
		view.CanPull = len(choices) > 0
	}
	return true
}

func dashboardInboundPullForm(r *http.Request) (issuetrackers.InboundPull, int64, error) {
	in := issuetrackers.InboundPull{FixedDeploymentID: r.PostForm.Get("fixed_deployment_id")}
	guard, e1 := strconv.ParseInt(r.PostForm.Get("inbound_guard_revision"), 10, 64)
	revision, e2 := strconv.ParseInt(r.PostForm.Get("expected_revision"), 10, 64)
	if e1 != nil || e2 != nil || guard <= 0 || revision <= 0 {
		return in, 0, api.ErrValidation("reload the inbound preview before applying")
	}
	in.GuardRevision = guard
	switch r.PostForm.Get("provider") {
	case "github":
		var expected api.NativeIssueTrackerStatus
		if err := json.Unmarshal([]byte(r.PostForm.Get("expected")), &expected); err != nil {
			return in, 0, api.ErrValidation("invalid provider preview")
		}
		in.Expected = expected.Status
	case "linear":
		var expected api.NativeLinearIssueTrackerStatus
		if err := json.Unmarshal([]byte(r.PostForm.Get("expected")), &expected); err != nil {
			return in, 0, api.ErrValidation("invalid provider preview")
		}
		in.Expected = expected.Status
	default:
		return in, 0, api.ErrValidation("provider must be GitHub or Linear")
	}
	if in.Expected.Provider != r.PostForm.Get("provider") {
		return in, 0, api.ErrValidation("provider snapshot does not match the form")
	}
	return in, revision, nil
}

func (s *server) dashboardTrackerInboundPull(w http.ResponseWriter, r *http.Request, acct state.Account) {
	policies, app, ok := s.issueTrackerAutoSyncAccess(w, r, acct)
	if !ok {
		return
	}
	if app.AccountID != acct.ID {
		api.WriteProblem(w, api.NewProblem(403, api.CodeForbidden, "Owner required", "only the owner may pull a tracker lifecycle"))
		return
	}
	store, ok := s.store.(state.IssueTrackerInboundStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("inbound sync unavailable"))
		return
	}
	in, revision, err := dashboardInboundPullForm(r)
	if err != nil {
		writeNativeIssueTrackerError(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(api.IssueTrackerRequestTimeoutSeconds)*time.Second)
	defer cancel()
	row, err := policies.GetIssueTrackerAutoSync(ctx, app.ID, app.AccountID, r.PathValue("issue_id"), r.PathValue("link_id"))
	if err != nil {
		writeNativeIssueTrackerError(w, err)
		return
	}
	if row.Policy.Revision != revision {
		writeNativeIssueTrackerError(w, state.IssueTrackerInboundChanged())
		return
	}
	out, err := s.applyDashboardTrackerInbound(w, r.WithContext(ctx), acct, app, store, row, in)
	if err != nil {
		writeNativeIssueTrackerError(w, err)
		return
	}
	if s.audit != nil {
		s.audit.Emit(ctx, "issue.tracker_lifecycle_pulled", &acct.ID, map[string]any{"app_id": app.ID, "issue_id": out.ID, "link_id": row.Policy.LinkID, "state": out.State, "policy_revision": revision, "outbound_paused": true})
	}
	http.Redirect(w, r, "/dashboard/apps/"+url.PathEscape(app.Slug)+"/issues?issue="+url.QueryEscape(out.ID), http.StatusSeeOther)
}

func (s *server) applyDashboardTrackerInbound(w http.ResponseWriter, r *http.Request, acct state.Account, app state.App, store state.IssueTrackerInboundStore, row state.IssueTrackerAutoSyncRecord, in issuetrackers.InboundPull) (api.Issue, error) {
	if in.Expected.Provider == "linear" {
		provider := s.nativeTrackerProvider
		if provider == nil {
			provider = issuetracker.NewClient(nil)
		}
		service := issuetrackers.LinearService{Host: nativeLinearCreationHost{server: s, app: app, account: acct, issueID: r.PathValue("issue_id"), linkID: r.PathValue("link_id")}, Provider: provider}
		return service.Pull(r.Context(), store, row, in)
	}
	return s.issueTrackerServiceFor(app, acct, r.PathValue("issue_id"), r.PathValue("link_id")).Pull(r.Context(), store, row, in)
}
