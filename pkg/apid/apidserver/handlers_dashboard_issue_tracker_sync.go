package apidserver

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/issuetrackers"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/httpsec"
	"github.com/onebox-faas/faas/pkg/issuetracker"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) populateDashboardTrackerSync(w http.ResponseWriter, r *http.Request, app state.App, account string, view *dashboard.IssueTrackerLinksView) bool {
	view.CanManage = app.AccountID == account
	view.LinearOAuthAvailable = s.linearOAuthConfig.Enabled()
	if store, ok := s.store.(state.IssueTrackerCallbackHealthStore); ok && view.CanManage {
		view.CallbackHealth = map[string]issuetracker.CallbackHealth{}
		for _, link := range view.Items {
			health, err := store.GetIssueTrackerCallbackHealth(r.Context(), app.ID, app.AccountID, view.IssueID, link.ID)
			if err != nil {
				writeIssueError(w, err)
				return false
			}
			view.CallbackHealth[link.ID] = health
		}
	}
	if store, ok := s.store.(state.IssueTrackerAutoSyncStore); ok {
		view.Policies = map[string]api.IssueTrackerAutoSync{}
		view.Reasons = map[string]string{}
		for _, link := range view.Items {
			row, err := store.GetIssueTrackerAutoSync(r.Context(), app.ID, app.AccountID, view.IssueID, link.ID)
			if err != nil {
				writeIssueError(w, err)
				return false
			}
			view.Policies[link.ID] = row.Policy
			view.Reasons[link.ID] = dashboard.IssueTrackerSyncReason(row.Policy.Reason)
		}
	}
	if store, ok := s.store.(state.IssueTrackerLinearStore); ok && view.CanManage {
		row, err := store.GetIssueTrackerLinearConnection(r.Context(), app.ID, app.AccountID)
		if err != nil {
			writeIssueError(w, err)
			return false
		}
		view.LinearConnection = &row.Connection
	}
	return true
}

func (s *server) dashboardIssueTrackerSyncHandler(w http.ResponseWriter, r *http.Request) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		writeDashboardUnauthorized(w, r)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, api.IssueEventMaxBytes)
	if err := middleware.VerifyAuthenticatedNamed(s.sessions, r, dashboardIssueAction, acct.ID, dashboardIssueCookie); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid CSRF token; reload the issues page"))
		return
	}
	s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.dashboardIssueTrackerSyncForAccount)))(w, r)
}
func (s *server) dashboardIssueTrackerSyncForAccount(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if err := r.ParseForm(); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid tracker sync form"))
		return
	}
	action := r.PostForm.Get("action")
	if action == "oauth_start" || action == "oauth_finish" || action == "oauth_revoke" {
		s.dashboardLinearOAuthForm(w, r, acct)
		return
	}
	if action == "connect" || action == "disconnect" {
		s.dashboardLinearConnectionForm(w, r, acct)
		return
	}
	r.SetPathValue("link_id", r.PostForm.Get("link_id"))
	if action == "pull" {
		s.dashboardTrackerInboundPull(w, r, acct)
		return
	}
	if action == "preview" {
		s.dashboardTrackerSyncPreview(w, r, acct)
		return
	}
	if action == "push" && !s.dashboardTrackerPushAllowed(w, r, acct) {
		return
	}
	body, err := dashboardTrackerSyncFormBody(r)
	if err != nil {
		writeNativeIssueTrackerError(w, err)
		return
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.ContentLength = int64(len(body))
	target := "/dashboard/apps/" + url.PathEscape(r.PathValue("slug")) + "/issues?issue=" + url.QueryEscape(r.PathValue("issue_id"))
	response := &dashboardTrackerRedirect{ResponseWriter: w, r: r, target: target}
	switch action {
	case "enable", "disable":
		s.updateIssueTrackerAutoSync(response, r, acct)
	case "push":
		if r.PostForm.Get("provider") == "linear" {
			s.updateNativeLinearIssueTrackerStatus(response, r, acct)
		} else {
			s.updateNativeIssueTrackerStatus(response, r, acct)
		}
	default:
		api.WriteProblem(w, api.ErrValidation("unknown tracker sync action"))
	}
}
func dashboardTrackerSyncFormBody(r *http.Request) ([]byte, error) {
	action := r.PostForm.Get("action")
	revision, err := strconv.ParseInt(r.PostForm.Get("expected_revision"), 10, 64)
	if err != nil || revision < 0 {
		return nil, api.ErrValidation("reload the policy before continuing")
	}
	in := api.UpdateIssueTrackerAutoSyncRequest{Direction: r.PostForm.Get("direction"), Enabled: action == "enable", ExpectedRevision: revision, ExpectedIssueState: r.PostForm.Get("source_state"), LinearOpenStateID: r.PostForm.Get("linear_open_state_id"), LinearResolvedStateID: r.PostForm.Get("linear_resolved_state_id")}
	if action == "disable" {
		return json.Marshal(in)
	}
	var expected api.IssueTrackerProviderStatus
	switch r.PostForm.Get("provider") {
	case "linear":
		var snapshot api.NativeLinearIssueTrackerStatus
		if err := json.Unmarshal([]byte(r.PostForm.Get("expected")), &snapshot); err != nil {
			return nil, api.ErrValidation("reload the provider preview")
		}
		in.ExpectedLinearStatus = &snapshot
		expected = snapshot.Status
		if action == "push" {
			return json.Marshal(api.UpdateNativeLinearIssueTrackerStatusRequest{DesiredState: in.ExpectedIssueState, Expected: expected, ExpectedConnectionRevision: snapshot.ConnectionRevision, ExpectedGroupFingerprint: snapshot.GroupFingerprint, LinearStateID: r.PostForm.Get("linear_state_id")})
		}
	case "github":
		var snapshot api.NativeIssueTrackerStatus
		if err := json.Unmarshal([]byte(r.PostForm.Get("expected")), &snapshot); err != nil {
			return nil, api.ErrValidation("reload the provider preview")
		}
		in.ExpectedStatus = &snapshot
		expected = snapshot.Status
		if action == "push" {
			return json.Marshal(api.UpdateNativeIssueTrackerStatusRequest{DesiredState: in.ExpectedIssueState, Expected: expected})
		}
	default:
		return nil, api.ErrValidation("provider must be GitHub or Linear")
	}
	return json.Marshal(in)
}
func (s *server) dashboardLinearConnectionForm(w http.ResponseWriter, r *http.Request, acct state.Account) {
	revision, err := strconv.ParseInt(r.PostForm.Get("expected_revision"), 10, 64)
	if err != nil || revision < 0 {
		api.WriteProblem(w, api.ErrValidation("reload the connection before continuing"))
		return
	}
	in := api.UpdateIssueTrackerLinearConnectionRequest{Connected: r.PostForm.Get("action") == "connect", ExpectedRevision: revision}
	if in.Connected {
		in.TeamID = r.PostForm.Get("team_id")
		in.APIKey = r.PostForm.Get("api_key")
		in.WebhookSecret = r.PostForm.Get("webhook_secret")
	}
	body, err := json.Marshal(in)
	if err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid connection form"))
		return
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.ContentLength = int64(len(body))
	target := "/dashboard/apps/" + url.PathEscape(r.PathValue("slug")) + "/issues?issue=" + url.QueryEscape(r.PathValue("issue_id"))
	s.updateIssueTrackerLinearConnection(&dashboardTrackerRedirect{ResponseWriter: w, r: r, target: target}, r, acct)
}
func (s *server) dashboardTrackerSyncPreview(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, app, ok := s.issueTrackerAutoSyncAccess(w, r, acct)
	if !ok {
		return
	}
	if app.AccountID != acct.ID {
		api.WriteProblem(w, api.NewProblem(403, api.CodeForbidden, "Owner required", "only the owner may inspect provider sync"))
		return
	}
	guard, ok := s.dashboardTrackerInboundGuard(w, r, app)
	if !ok {
		return
	}
	row, err := store.GetIssueTrackerAutoSync(r.Context(), app.ID, app.AccountID, r.PathValue("issue_id"), r.PathValue("link_id"))
	if err != nil {
		writeNativeIssueTrackerError(w, err)
		return
	}
	view := dashboard.IssueTrackerSyncPreview{AppSlug: app.Slug, IssueID: r.PathValue("issue_id"), LinkID: r.PathValue("link_id"), CSRF: r.PostForm.Get("csrf_token"), Provider: r.PostForm.Get("provider"), Policy: row.Policy}
	snapshot, catalog, inbound, ok := s.observeDashboardTrackerPreview(w, r, acct, app, row, guard, &view)
	if !ok {
		return
	}
	if !s.finishDashboardInboundPreview(w, r, app, row, guard, inbound, &view) {
		return
	}
	view.SourceState = inbound.SourceState
	view.InboundOutcome = inbound.Outcome
	view.InboundReason = dashboard.IssueTrackerInboundReason(inbound.Reason)
	if !s.finishDashboardTrackerPreview(w, &view, snapshot) {
		return
	}
	s.renderDashboardTrackerPreview(w, r, acct, view, catalog)
}
func (s *server) observeDashboardTrackerPreview(w http.ResponseWriter, r *http.Request, acct state.Account, app state.App, row state.IssueTrackerAutoSyncRecord, guard int64, view *dashboard.IssueTrackerSyncPreview) (any, []issuetracker.LinearWorkflowState, issuetrackers.InboundPreview, bool) {
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(api.IssueTrackerRequestTimeoutSeconds)*time.Second)
	defer cancel()
	r = r.WithContext(ctx)
	var snapshot any
	var catalog []issuetracker.LinearWorkflowState
	var inbound issuetrackers.InboundPreview
	var err error
	switch view.Provider {
	case "linear":
		service, ok := s.nativeLinearStatusService(w, r, acct)
		if !ok {
			return nil, nil, issuetrackers.InboundPreview{}, false
		}
		out, choices, assessment, e := service.ReadInboundPreviewWithWorkflows(ctx, row)
		inbound = assessment
		catalog = choices
		err = e
		snapshot = out
		view.Status = out.Status
	case "github":
		out, assessment, e := s.issueTrackerServiceFor(app, acct, view.IssueID, view.LinkID).ReadInboundPreview(ctx, row)
		inbound = assessment
		err = e
		snapshot = out
		view.Status = out.Status
	default:
		api.WriteProblem(w, api.ErrValidation("provider must be GitHub or Linear"))
		return nil, nil, issuetrackers.InboundPreview{}, false
	}
	if err != nil {
		s.recordDashboardTrackerInspectionFailure(r, row, guard, err)
		writeNativeIssueTrackerError(w, err)
		return nil, nil, issuetrackers.InboundPreview{}, false
	}
	if err = s.recordDashboardTrackerInspection(r, row, guard, view.Status, inbound.Reason, false); err != nil {
		writeNativeIssueTrackerError(w, err)
		return nil, nil, issuetrackers.InboundPreview{}, false
	}
	return snapshot, catalog, inbound, true
}
func (s *server) finishDashboardTrackerPreview(w http.ResponseWriter, view *dashboard.IssueTrackerSyncPreview, snapshot any) bool {
	view.Aligned = view.SourceState == view.Status.State && (view.SourceState == "open" || view.SourceState == "resolved")
	view.CanPush = !view.Policy.Enabled && !view.Aligned && (view.SourceState == "open" || view.SourceState == "resolved")
	data, err := json.Marshal(snapshot)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("preview unavailable"))
		return false
	}
	view.ExpectedJSON = string(data)
	return true
}

// Successful API mutations keep their authorization and audit behavior, then
// redirect browser forms. Error responses pass through without exposing a key.
type dashboardTrackerRedirect struct {
	http.ResponseWriter
	r          *http.Request
	target     string
	redirected bool
}

func (w *dashboardTrackerRedirect) WriteHeader(status int) {
	if status >= 200 && status < 300 {
		w.redirected = true
		w.Header().Del("Content-Type")
		http.Redirect(w.ResponseWriter, w.r, w.target, http.StatusSeeOther)
		return
	}
	w.ResponseWriter.WriteHeader(status)
}
func (w *dashboardTrackerRedirect) Write(data []byte) (int, error) {
	if w.redirected {
		return len(data), nil
	}
	return w.ResponseWriter.Write(data)
}

func (s *server) dashboardTrackerPushAllowed(w http.ResponseWriter, r *http.Request, acct state.Account) bool {
	store, app, ok := s.issueTrackerAutoSyncAccess(w, r, acct)
	if !ok {
		return false
	}
	if app.AccountID != acct.ID {
		api.WriteProblem(w, api.NewProblem(403, api.CodeForbidden, "Owner required", "only the owner may synchronize a ticket"))
		return false
	}
	row, err := store.GetIssueTrackerAutoSync(r.Context(), app.ID, app.AccountID, r.PathValue("issue_id"), r.PathValue("link_id"))
	if err != nil {
		writeNativeIssueTrackerError(w, err)
		return false
	}
	revision, err := strconv.ParseInt(r.PostForm.Get("expected_revision"), 10, 64)
	if err != nil || row.Policy.Enabled || row.Policy.Revision != revision {
		api.WriteProblem(w, api.NewProblem(409, "issue_tracker_auto_sync_changed", "Review required", "disable automatic sync and preview again before pushing manually"))
		return false
	}
	return true
}

func (s *server) renderDashboardTrackerPreview(w http.ResponseWriter, r *http.Request, acct state.Account, view dashboard.IssueTrackerSyncPreview, catalog []issuetracker.LinearWorkflowState) {
	view.CanEnable = view.Aligned
	if view.Provider == "linear" {
		view.SetLinearWorkflowChoices(catalog)
	}
	if err := dashboard.Render(w, s.log, httpsec.NonceFromContext(r.Context()), dashboard.Page{Title: "Review tracker sync", Body: "issue_tracker_sync", Account: dashboardAccountView(acct, 0), Data: view}); err != nil {
		renderProblem(w, s.log, err)
	}
}
