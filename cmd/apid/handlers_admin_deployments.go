// Provider-side deployment incident controls.
//
// The operator surface is deliberately a thin policy layer over the existing
// deployment queue primitives. It gives an allowlisted, MFA-stepped operator
// a bounded fleet view and audited retry/cancel actions without exposing the
// source or runtime material that lives on a deployment row.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	operatorDeploymentDefaultLimit = 50
	operatorDeploymentMaxLimit     = 200
	operatorDeploymentStageHistory = 32
)

var operatorDeploymentIncidentStatuses = []state.DeploymentStatus{
	state.DeployPending,
	state.DeployBuilding,
	state.DeployImaging,
	state.DeploySnapshotting,
	state.DeployFailed,
}

func (s *server) getOperatorDeployments(w http.ResponseWriter, r *http.Request, caller state.Account) {
	if allowed, prob := s.adminAllows(caller); !allowed {
		api.WriteProblem(w, prob)
		return
	}
	accountID, appID, prob := parseOperatorDeploymentScope(r)
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	if accountID != "" {
		if _, err := s.store.AccountByID(r.Context(), accountID); err != nil {
			writeOperatorJobLookupError(w, err, "account")
			return
		}
	}
	if appID != "" {
		app, err := s.store.AppByID(r.Context(), appID)
		if err != nil {
			writeOperatorJobLookupError(w, err, "app")
			return
		}
		if accountID != "" && app.AccountID != accountID {
			api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
				"app/account mismatch", "app_id does not belong to account_id"))
			return
		}
	}
	statuses, prob := parseOperatorDeploymentStatuses(r.URL.Query().Get("status"))
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	limitProb, limit := api.ParseLimit(r.URL.Query().Get("limit"), operatorDeploymentDefaultLimit, operatorDeploymentMaxLimit, "operator deployments")
	if limitProb != nil {
		api.WriteProblem(w, limitProb)
		return
	}
	offset, prob := parseOperatorJobOffset(r.URL.Query().Get("offset"))
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	rows, err := s.store.ListDeploymentsForOperator(r.Context(), state.OperatorDeploymentFilter{
		AccountID: accountID,
		AppID:     appID,
		Statuses:  statuses,
		Limit:     limit,
		Offset:    offset,
	})
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list operator deployments"))
		return
	}
	items, err := s.projectOperatorDeployments(r.Context(), rows)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not resolve operator deployments"))
		return
	}
	nextOffset := -1
	if len(items) == limit {
		nextOffset = offset + len(items)
	}
	emitOperatorDeploymentView(r, s, caller, accountID, appID, "deployments.active")
	statusStrings := make([]string, 0, len(statuses))
	for _, status := range statuses {
		statusStrings = append(statusStrings, string(status))
	}
	writeJSON(w, http.StatusOK, api.OperatorDeploymentListResponse{
		Deployments: items,
		AccountID:   accountID,
		AppID:       appID,
		Statuses:    statusStrings,
		Limit:       limit,
		Offset:      offset,
		NextOffset:  nextOffset,
	})
}

func (s *server) getOperatorDeployment(w http.ResponseWriter, r *http.Request, caller state.Account) {
	if allowed, prob := s.adminAllows(caller); !allowed {
		api.WriteProblem(w, prob)
		return
	}
	id, prob := parseOperatorJobUUID(r.PathValue("id"), "deployment id")
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	deployment, app, prob := s.resolveOperatorDeployment(r, id)
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	response := api.OperatorDeploymentDetailResponse{
		Deployment: projectOperatorDeployment(deployment, app),
		Stage:      operatorDeploymentStage(deployment),
	}
	if deployment.BuildID != "" {
		build, err := s.store.BuildByID(r.Context(), deployment.BuildID)
		if err != nil && !errors.Is(err, state.ErrNotFound) {
			api.WriteProblem(w, api.ErrCapacity("could not inspect deployment build"))
			return
		}
		if err == nil {
			response.Build = projectOperatorDeploymentBuild(build)
		}
	}
	emitOperatorDeploymentView(r, s, caller, app.AccountID, app.ID, "deployments.inspect")
	writeJSON(w, http.StatusOK, response)
}

func (s *server) postOperatorDeploymentCancel(w http.ResponseWriter, r *http.Request, caller state.Account) {
	if allowed, prob := s.adminAllows(caller); !allowed {
		api.WriteProblem(w, prob)
		return
	}
	if r.URL.Query().Get("confirm") != "true" {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"confirm required", "?confirm=true is required to cancel a deployment"))
		return
	}
	reason, prob := parseRequiredOperatorJobReason(r.URL.Query().Get("reason"))
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	id, prob := parseOperatorJobUUID(r.PathValue("id"), "deployment id")
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	prior, app, prob := s.resolveOperatorDeployment(r, id)
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	if !prior.Status.IsCancelEligible() {
		emitOperatorDeploymentMutation(r, s, caller, app, prior, "cancel", reason, "rejected")
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict,
			"deployment is not cancellable", "only pending, building, imaging, or snapshotting deployments can be cancelled"))
		return
	}
	deployment, cancelledBuilds, err := s.store.CancelDeploymentTx(r.Context(), id, "operator:"+caller.ID, state.CancelReasonSystem)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			s.notFound(w, "no such deployment")
			return
		}
		if errors.Is(err, state.ErrCancelLiveForbidden) || errors.Is(err, state.ErrInvalidStateTransition) {
			emitOperatorDeploymentMutation(r, s, caller, app, prior, "cancel", reason, "lost_race")
			api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict,
				"deployment is no longer cancellable", "the deployment changed state before cancellation was applied"))
			return
		}
		api.WriteProblem(w, api.ErrCapacity("could not cancel deployment"))
		return
	}
	for _, buildID := range cancelledBuilds {
		payload, _ := json.Marshal(buildChangedPayload{
			BuildID: buildID, DeploymentID: id, Status: "cancelled", Reason: string(state.CancelReasonSystem), Cascade: true,
		})
		if s.notif != nil {
			_ = s.notif.Notify(r.Context(), db.NotifyBuildChanged, string(payload))
		}
	}
	emitOperatorDeploymentMutation(r, s, caller, app, prior, "cancel", reason, "cancelled")
	writeJSON(w, http.StatusOK, api.OperatorDeploymentMutationResponse{
		Deployment: projectOperatorDeployment(deployment, app),
		Action:     "cancel",
		Reason:     reason,
	})
}

func (s *server) postOperatorDeploymentRetry(w http.ResponseWriter, r *http.Request, caller state.Account) {
	if allowed, prob := s.adminAllows(caller); !allowed {
		api.WriteProblem(w, prob)
		return
	}
	if r.URL.Query().Get("confirm") != "true" {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"confirm required", "?confirm=true is required to retry a deployment"))
		return
	}
	reason, prob := parseRequiredOperatorJobReason(r.URL.Query().Get("reason"))
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	fromStage := state.StageName(strings.TrimSpace(r.URL.Query().Get("from_stage")))
	if !state.IsStageName(fromStage) {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"invalid from_stage", fmt.Sprintf("from_stage must be one of %v", state.AllStageNames)))
		return
	}
	id, prob := parseOperatorJobUUID(r.PathValue("id"), "deployment id")
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	prior, app, prob := s.resolveOperatorDeployment(r, id)
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	if prior.Status != state.DeployFailed {
		emitOperatorDeploymentMutation(r, s, caller, app, prior, "retry", reason, "rejected")
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict,
			"deployment is not failed", "only failed deployments can be retried"))
		return
	}
	deployment, err := s.enqueueRetry(r.Context(), app, prior, fromStage)
	if err != nil {
		var problem *api.Problem
		if errors.As(err, &problem) {
			api.WriteProblem(w, problem)
			return
		}
		if errors.Is(err, state.ErrInvalidArgument) {
			api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
				"invalid from_stage", "from_stage is not in the closed stage vocabulary"))
			return
		}
		api.WriteProblem(w, api.ErrCapacity("could not enqueue deployment retry"))
		return
	}
	emitOperatorDeploymentMutation(r, s, caller, app, prior, "retry", reason, "enqueued")
	writeJSON(w, http.StatusAccepted, api.OperatorDeploymentMutationResponse{
		Deployment: projectOperatorDeployment(deployment, app),
		Action:     "retry",
		Reason:     reason,
	})
}

func parseOperatorDeploymentScope(r *http.Request) (string, string, *api.Problem) {
	accountID := strings.TrimSpace(r.URL.Query().Get("account_id"))
	if accountID != "" {
		var prob *api.Problem
		accountID, prob = parseOperatorJobUUID(accountID, "account_id")
		if prob != nil {
			return "", "", prob
		}
	}
	appID := strings.TrimSpace(r.URL.Query().Get("app_id"))
	if appID != "" {
		var prob *api.Problem
		appID, prob = parseOperatorJobUUID(appID, "app_id")
		if prob != nil {
			return "", "", prob
		}
	}
	return accountID, appID, nil
}

func parseOperatorDeploymentStatuses(raw string) ([]state.DeploymentStatus, *api.Problem) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return append([]state.DeploymentStatus(nil), operatorDeploymentIncidentStatuses...), nil
	}
	if strings.EqualFold(raw, "all") {
		return nil, nil
	}
	seen := make(map[state.DeploymentStatus]struct{})
	statuses := make([]state.DeploymentStatus, 0, 5)
	for _, part := range strings.Split(raw, ",") {
		status := state.DeploymentStatus(strings.TrimSpace(part))
		if status == "" {
			continue
		}
		switch status {
		case state.DeployPending, state.DeployBuilding, state.DeployImaging, state.DeploySnapshotting,
			state.DeployLive, state.DeployFailed, state.DeploySuperseded, state.DeployCancelled:
		default:
			return nil, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
				"invalid status", "status must be a comma-separated deployment status or all")
		}
		if _, ok := seen[status]; !ok {
			seen[status] = struct{}{}
			statuses = append(statuses, status)
		}
	}
	if len(statuses) == 0 {
		return nil, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"invalid status", "status must include at least one deployment status")
	}
	return statuses, nil
}

func (s *server) resolveOperatorDeployment(r *http.Request, id string) (state.Deployment, state.App, *api.Problem) {
	deployment, err := s.store.DeploymentByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return state.Deployment{}, state.App{}, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", "no such deployment")
		}
		return state.Deployment{}, state.App{}, api.ErrCapacity("could not resolve deployment")
	}
	app, err := s.store.AppByID(r.Context(), deployment.AppID)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return state.Deployment{}, state.App{}, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", "no such deployment")
		}
		return state.Deployment{}, state.App{}, api.ErrCapacity("could not resolve deployment app")
	}
	return deployment, app, nil
}

func (s *server) projectOperatorDeployments(ctx context.Context, rows []state.Deployment) ([]api.OperatorDeployment, error) {
	items := make([]api.OperatorDeployment, 0, len(rows))
	apps := make(map[string]state.App)
	for _, row := range rows {
		app, ok := apps[row.AppID]
		if !ok {
			var err error
			app, err = s.store.AppByID(ctx, row.AppID)
			if err != nil {
				return nil, err
			}
			apps[row.AppID] = app
		}
		if app.AccountID == "" || app.Status == state.AppDeleted {
			return nil, errors.New("operator deployment ownership mismatch")
		}
		items = append(items, projectOperatorDeployment(row, app))
	}
	return items, nil
}

func projectOperatorDeployment(d state.Deployment, app state.App) api.OperatorDeployment {
	out := api.OperatorDeployment{
		ID:           d.ID,
		AppID:        d.AppID,
		AppSlug:      app.Slug,
		AccountID:    app.AccountID,
		BuildID:      d.BuildID,
		Kind:         string(d.Kind),
		Status:       string(d.Status),
		SourceBytes:  d.SourceBytes,
		Priority:     d.Priority,
		Error:        d.Error,
		ErrorCode:    d.ErrorCode,
		ErrorHint:    d.ErrorHint,
		ErrorWhy:     d.ErrorWhy,
		ErrorFix:     d.ErrorFix,
		RolloutState: d.RolloutState,
		CreatedAt:    d.CreatedAt.UTC().Format(time.RFC3339),
		CancelReason: d.CancelReason,
	}
	if d.CancelledAt != nil {
		out.CancelledAt = d.CancelledAt.UTC().Format(time.RFC3339)
	}
	return out
}

func operatorDeploymentStage(d state.Deployment) *api.OperatorDeploymentStage {
	if len(d.StageState) == 0 {
		return nil
	}
	var stage state.StageState
	if err := json.Unmarshal(d.StageState, &stage); err != nil {
		return nil
	}
	out := &api.OperatorDeploymentStage{
		Current:             string(stage.Current),
		RetryRequestedStage: string(stage.RetryRequestedStage),
		RetryRestartReason:  stage.RetryRestartReason,
	}
	if stage.CurrentStartedAt != nil {
		out.CurrentStartedAt = stage.CurrentStartedAt.UTC().Format(time.RFC3339)
	}
	start := 0
	if len(stage.History) > operatorDeploymentStageHistory {
		start = len(stage.History) - operatorDeploymentStageHistory
	}
	out.History = make([]api.OperatorDeploymentStageItem, 0, len(stage.History)-start)
	for _, item := range stage.History[start:] {
		projected := api.OperatorDeploymentStageItem{
			Name:       string(item.Name),
			DurationMS: item.DurationMs,
			Status:     item.Status,
			Reason:     item.Reason,
		}
		if item.StartedAt != nil {
			projected.StartedAt = item.StartedAt.UTC().Format(time.RFC3339)
		}
		if item.EndedAt != nil {
			projected.EndedAt = item.EndedAt.UTC().Format(time.RFC3339)
		}
		out.History = append(out.History, projected)
	}
	return out
}

func projectOperatorDeploymentBuild(b state.Build) *api.OperatorDeploymentBuild {
	out := &api.OperatorDeploymentBuild{
		ID:           b.ID,
		Status:       string(b.Status),
		FailureClass: string(b.FailureClass),
		CacheStatus:  b.CacheStatus,
		EnqueuedAt:   b.EnqueuedAt.UTC().Format(time.RFC3339),
	}
	if !b.StartedAt.IsZero() {
		out.StartedAt = b.StartedAt.UTC().Format(time.RFC3339)
	}
	if !b.FinishedAt.IsZero() {
		out.FinishedAt = b.FinishedAt.UTC().Format(time.RFC3339)
	}
	return out
}

func emitOperatorDeploymentView(r *http.Request, s *server, caller state.Account, accountID, appID, endpoint string) {
	if s.audit == nil {
		return
	}
	var subject *string
	if accountID != "" {
		subject = &accountID
	}
	s.audit.Emit(r.Context(), "operator.action.view", subject, map[string]any{
		"actor": caller.ID, "endpoint": endpoint, "target_account_id": accountID, "target_app_id": appID,
	})
}

func emitOperatorDeploymentMutation(r *http.Request, s *server, caller state.Account, app state.App, prior state.Deployment, action, reason, result string) {
	if s.audit == nil {
		return
	}
	s.audit.Emit(r.Context(), "operator.action."+action+"_deployment", &app.AccountID, map[string]any{
		"actor": caller.ID, "target_account_id": app.AccountID, "target_app_id": app.ID,
		"deployment_id": prior.ID, "previous_status": string(prior.Status), "action": action,
		"reason": reason, "result": result,
	})
}
