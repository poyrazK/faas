// adr:1355 — wiring for durable single-app verification receipts.
package apidserver

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/bindings"
	"github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
	"strconv"
	"time"
)

func (s *server) verificationRunBackend(w http.ResponseWriter, r *http.Request, acct state.Account) (bindings.VerificationRunBackend, bool) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return bindings.VerificationRunBackend{}, false
	}
	_, ok = s.store.(state.BindingVerificationRunStore)
	_, queueOK := s.store.(state.QueueBindingCanaryStore)
	_, taskOK := s.store.(state.AppTaskStore)
	if !ok || !queueOK || !taskOK {
		api.WriteProblem(w, api.ErrCapacity("verification receipts are unavailable"))
		return bindings.VerificationRunBackend{}, false
	}
	return s.bindingVerificationBackendForApp(r, acct, app), true
}
func (s *server) bindingVerificationBackendForApp(r *http.Request, acct state.Account, app state.App) bindings.VerificationRunBackend {
	store := s.store.(state.BindingVerificationRunStore)
	canaries := s.store.(state.QueueBindingCanaryStore)
	tasks := s.store.(state.AppTaskStore)
	return bindings.VerificationRunBackend{DiagnosticsAccess: func(run api.BindingVerificationRun) error { return verificationDiagnosticsAccess(r, run) }, TaskProbesEnabled: &s.appTaskAPIEnabled, Store: store, Tasks: tasks, Canaries: canaries, AccountID: acct.ID, AppID: app.ID, App: app.Slug,
		Inventory: func(ctx context.Context, dep string) (api.AppBindingInventory, api.BindingReleasePolicy, error) {
			return s.verificationRunInventory(ctx, r, acct, app, dep, nil)
		},
		InventoryTargets: func(ctx context.Context, dep string, targets map[string]string) (api.AppBindingInventory, api.BindingReleasePolicy, error) {
			return s.verificationRunInventory(ctx, r, acct, app, dep, targets)
		},
		CaptureTask: func(ctx context.Context, dep string, step api.BindingVerificationPlanStep, at time.Time) (*state.CreateAppTaskParams, error) {
			return s.captureVerificationRunTask(ctx, r, acct, app, dep, step, at)
		}}
}
func (s *server) createBindingVerificationRun(w http.ResponseWriter, r *http.Request, acct state.Account) {
	backend, ok := s.verificationRunBackend(w, r, acct)
	if !ok {
		return
	}
	var in api.CreateBindingVerificationRunRequest
	if err := decodeJSONSized(r, &in, api.BindingVerificationRunRequestMaxBytes); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid verification run request"))
		return
	}
	result, err := backend.Create(r.Context(), in)
	if problem := bindings.VerificationRunProblem(err); problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	s.audit.Emit(r.Context(), "bindings.verification_run_created", &acct.ID, map[string]any{"run_id": result.ID, "app_id": backend.AppID})
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, s.redactVerificationRun(r, result))
}
func (s *server) getBindingVerificationRun(w http.ResponseWriter, r *http.Request, acct state.Account) {
	s.handleBindingVerificationRun(w, r, acct, "status")
}
func (s *server) resumeBindingVerificationRun(w http.ResponseWriter, r *http.Request, acct state.Account) {
	s.handleBindingVerificationRun(w, r, acct, "resume")
}
func (s *server) cancelBindingVerificationRun(w http.ResponseWriter, r *http.Request, acct state.Account) {
	s.handleBindingVerificationRun(w, r, acct, "cancel")
}
func (s *server) completeBindingVerificationRun(w http.ResponseWriter, r *http.Request, acct state.Account) {
	s.handleBindingVerificationRun(w, r, acct, "complete")
}
func (s *server) admitBindingVerificationRunStep(w http.ResponseWriter, r *http.Request, acct state.Account) {
	s.handleBindingVerificationRun(w, r, acct, "admit")
}
func (s *server) handleBindingVerificationRun(w http.ResponseWriter, r *http.Request, acct state.Account, action string) {
	backend, ok := s.verificationRunBackend(w, r, acct)
	if !ok {
		return
	}
	var result any
	var err error
	switch action {
	case "status":
		result, err = backend.Status(r.Context(), r.PathValue("run"))
	case "resume":
		result, err = backend.Resume(r.Context(), r.PathValue("run"))
	case "cancel":
		result, err = backend.Cancel(r.Context(), r.PathValue("run"))
	case "complete":
		result, err = backend.Complete(r.Context(), r.PathValue("run"))
	case "admit":
		var index int
		index, err = strconv.Atoi(r.PathValue("step"))
		if err == nil && index >= 0 && index < api.BindingVerificationRunMaxSteps {
			result, err = backend.Admit(r.Context(), r.PathValue("run"), index)
		} else {
			err = state.ErrInvalidArgument
		}
	}
	if problem := bindings.VerificationRunProblem(err); problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	if action != "status" {
		s.audit.Emit(r.Context(), "bindings.verification_run_"+action, &acct.ID, map[string]any{"run_id": r.PathValue("run"), "app_id": backend.AppID})
	}
	w.Header().Set("Cache-Control", "no-store")
	if run, ok := result.(api.BindingVerificationRun); ok {
		result = s.redactVerificationRun(r, run)
	}
	status := http.StatusOK
	if run, ok := result.(api.BindingVerificationRun); ok && action == "complete" && run.Status == "pending" && run.Qualification != nil {
		status = http.StatusAccepted
	}
	writeJSON(w, status, result)
}
func (s *server) verificationRunInventory(ctx context.Context, r *http.Request, acct state.Account, app state.App, id string, targets map[string]string) (api.AppBindingInventory, api.BindingReleasePolicy, error) {
	if id == "" {
		live, err := s.store.LiveDeployment(ctx, app.ID)
		if err != nil {
			return api.AppBindingInventory{}, api.BindingReleasePolicy{}, err
		}
		id = live.ID
	}
	dep, problem := s.selectBindingVerificationDeployment(ctx, app, id)
	if problem != nil {
		return api.AppBindingInventory{}, api.BindingReleasePolicy{}, problem
	}
	request := r.Clone(ctx)
	request.URL.RawQuery = ""
	query := request.URL.Query()
	query.Set("deployment_id", dep.ID)
	if problem := s.selectBindingServiceTargets(request, app, dep, targets); problem != nil {
		return api.AppBindingInventory{}, api.BindingReleasePolicy{}, problem
	}
	if len(targets) > 0 {
		encoded, err := json.Marshal(targets)
		if err != nil {
			return api.AppBindingInventory{}, api.BindingReleasePolicy{}, err
		}
		query.Set("service_targets", string(encoded))
	}
	request.URL.RawQuery = query.Encode()
	inventory := s.collectBindingInventory(ctx, request, acct, app, "")
	for _, issue := range inventory.Issues {
		if issue.Code == "forbidden" {
			return inventory, api.BindingReleasePolicy{}, api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Binding review unavailable", "Status and resume require read access to every reviewed binding domain.")
		}
	}
	if !inventory.Complete {
		return inventory, api.BindingReleasePolicy{}, api.ErrCapacity("complete binding inventory is unavailable")
	}
	store, ok := s.store.(state.BindingReleasePolicyStore)
	if !ok {
		return inventory, api.BindingReleasePolicy{}, fmt.Errorf("policy store unavailable")
	}
	policy, err := store.GetBindingReleasePolicy(ctx, acct.ID, app.ID, inventory.VerificationScope)
	inventory.Scope = policy.Scope
	return inventory, policy, err
}
func (s *server) captureVerificationRunTask(ctx context.Context, r *http.Request, acct state.Account, app state.App, id string, step api.BindingVerificationPlanStep, at time.Time) (*state.CreateAppTaskParams, error) {
	if !s.appTaskAPIEnabled {
		return nil, appTaskAPIDisabledProblem()
	}
	command, err := s.verificationRunTaskCommand(ctx, acct, app, step)
	if err != nil {
		return nil, err
	}
	dep, problem := s.selectBindingVerificationDeployment(ctx, app, id)
	if problem != nil {
		return nil, problem
	}
	request := api.ResolvedCreateAppTaskRequest{VerificationDeploymentID: dep.ID, Command: command, TimeoutSeconds: api.BindingVerificationRunProbeTimeoutSeconds, MaxOutputBytes: api.BindingVerificationRunProbeOutputBytes}
	pin, err := s.captureBindingVerificationPin(r.WithContext(ctx), acct, app, dep, request)
	if err != nil {
		return nil, err
	}
	if pin == nil {
		return nil, api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Binding probe unavailable", "The reviewed binding is not authorized for probing.")
	}
	command, err = bindingVerificationTaskCommand(command, pin)
	if err != nil {
		return nil, err
	}
	return &state.CreateAppTaskParams{AccountID: acct.ID, AppID: app.ID, DeploymentID: dep.ID, RequireLiveDeployment: true, BindingVerification: pin, Kind: state.AppTaskKindManual, Command: command, TimeoutSeconds: api.BindingVerificationRunProbeTimeoutSeconds, MaxOutputBytes: api.BindingVerificationRunProbeOutputBytes, CreatedAt: at}, nil
}
func (s *server) verificationRunTaskCommand(ctx context.Context, acct state.Account, app state.App, step api.BindingVerificationPlanStep) ([]string, error) {
	switch step.Type {
	case api.BindingTypeService:
		if step.TargetDeploymentID != "" {
			return []string{api.AppTaskServiceBindingTargetProbeCommand, step.Name, step.TargetDeploymentID}, nil
		}
		return []string{api.AppTaskServiceBindingProbeCommand, step.Name}, nil
	case api.BindingTypePostgres:
		return []string{api.AppTaskPostgresBindingProbeCommand, step.Binding}, nil
	case api.BindingTypeObjectStorage:
		if step.ProbeMode != "read" {
			return []string{api.AppTaskObjectStorageWriteProbeCommand, step.Prefix, step.ProbeMode}, nil
		}
		return []string{api.AppTaskObjectStorageBindingProbeCommand, step.Prefix}, nil
	case api.BindingTypeOutbound:
		store, ok := s.store.(state.OutboundBindingStore)
		if !ok {
			return nil, state.ErrConflict
		}
		rows, err := store.ListOutboundAppBindings(ctx, acct.ID, app.ID)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			if row.Name == step.Name {
				return []string{api.AppTaskOutboundBindingProbeCommand, row.ID}, nil
			}
		}
	}
	return nil, state.ErrConflict
}

func (s *server) redactVerificationRun(r *http.Request, run api.BindingVerificationRun) api.BindingVerificationRun {
	return bindings.RedactVerificationRun(run, func(kind string) bool {
		switch kind {
		case api.BindingTypePostgres:
			return middleware.HasScope(r, api.ScopesManagedPostgresReadSurface...)
		case api.BindingTypeObjectStorage:
			return middleware.HasScope(r, api.ScopesStorageManageSurface...)
		default:
			return true
		}
	})
}
