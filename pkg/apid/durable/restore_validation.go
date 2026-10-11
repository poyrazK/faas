// adr: 943
package durable

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/durableentity"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *Service) ValidateRestore(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "private, no-store")
	app, ok := s.Host.LoadApp(w, r, acct, r.PathValue("slug"))
	if !ok || !s.durableEntityRestoreValidationAvailable(w) || !s.RetryAvailable(w, acct, app) {
		return
	}
	var request api.DurableEntityRestoreRequest
	if !httpkit.DecodeJSONLimit(w, r, &request, int64(api.MaxDurableEntityInvocationBytes)) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.DurableEntityInvokeTimeout)
	defer cancel()
	id, scope, problem := s.Identity(r.WithContext(ctx), acct, app, api.DurableEntityInvokeRequest{Namespace: request.Namespace, Key: request.Key, Environment: request.Environment, PlatformTenantID: request.PlatformTenantID})
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	exported := durableEntityExportFromAPI(request.Export)
	preview, err := s.Engine.PreviewRestore(ctx, id, request.ExpectedVersion, exported)
	if err == nil && !preview.ExpectedVersionMatches {
		err = durableentity.ErrRestoreObsolete
	}
	if err != nil {
		writeDurableEntityRestoreProblem(w, err)
		return
	}
	out, err := s.dispatchDurableEntityRestoreValidation(ctx, acct, app, scope, id, request, exported.Data)
	if err != nil {
		writeDurableEntityRestoreProblem(w, err)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, out)
}

func (s *Service) durableEntityRestoreValidationAvailable(w http.ResponseWriter) bool {
	if !s.RestoreValidationEnabled {
		api.WriteProblem(w, api.NewProblem(http.StatusServiceUnavailable, "durable_entity_restore_validation_unavailable", "Restore validation unavailable", "the operator has not enabled application restore validation"))
		return false
	}
	return true
}

func (s *Service) dispatchDurableEntityRestoreValidation(ctx context.Context, acct state.Account, app state.App, scope string, id durableentity.ID, request api.DurableEntityRestoreRequest, candidate json.RawMessage) (api.DurableEntityRestoreValidationResponse, error) {
	started := time.Now()
	out, err := s.executeDurableEntityRestoreValidation(ctx, acct, app, scope, id, request, candidate)
	s.Metrics.observeDuration("validate_restore", started)
	outcomeErr := err
	if err == nil && !out.Valid {
		outcomeErr = durableentity.ErrRestoreRejected
	}
	s.Metrics.observeResult("validate_restore", durableentity.Result{}, outcomeErr)
	return out, err
}

func (s *Service) executeDurableEntityRestoreValidation(ctx context.Context, acct state.Account, app state.App, scope string, id durableentity.ID, request api.DurableEntityRestoreRequest, candidate json.RawMessage) (api.DurableEntityRestoreValidationResponse, error) {
	var out api.DurableEntityRestoreValidationResponse
	if request.RequestID == "" {
		return out, durableentity.ErrInvalid
	}
	inv, version, err := s.durableEntityInvocationVersion(ctx, app, scope, id)
	if err != nil {
		return out, err
	}
	if request.ValidationDeploymentID != "" && version.DeploymentID != request.ValidationDeploymentID {
		return out, durableEntityValidationDeploymentProblem()
	}
	inv.Path = api.DurableEntityRestoreValidationPath
	envelope := durableentity.RestoreValidationRequest{ProtocolVersion: api.DurableEntityRestoreValidationProtocolVersion, Event: "validate_restore", Entity: id, RequestID: request.RequestID, DeploymentID: version.DeploymentID, ExpectedVersion: request.ExpectedVersion, SourceVersion: request.Export.Version, Candidate: candidate}
	if err := envelope.Validate(); err != nil {
		return out, err
	}
	body, err := json.Marshal(envelope)
	if err != nil || len(body) > api.MaxDurableEntityInvocationBytes {
		return out, durableentity.ErrLimit
	}
	var result json.RawMessage
	var bundleHash string
	if s.RestoreIsolationEnabled {
		result, bundleHash, err = s.enqueueIsolatedRestoreValidator(ctx, acct, app, version.DeploymentID, request.ValidationBundleSHA256, body)
	} else {
		if request.ValidationBundleSHA256 != "" {
			return out, restoreIsolationUnavailable()
		}
		var final state.Invocation
		final, err = s.enqueueDurableEntityGuest(ctx, acct, inv, body)
		result = final.Result
	}
	if err != nil {
		return out, err
	}
	valid, err := durableentity.DecodeRestoreValidation(result)
	if err != nil {
		return out, api.NewProblem(http.StatusBadGateway, "durable_entity_restore_validation_invalid", "Invalid validator response", "the validation handler must return only a versioned boolean verdict")
	}
	_, latest, err := s.durableEntityInvocationVersion(ctx, app, scope, id)
	if err != nil {
		return out, err
	}
	if latest.DeploymentID != version.DeploymentID {
		return out, durableEntityValidationDeploymentProblem()
	}
	isolation := ""
	if s.RestoreIsolationEnabled {
		isolation = "networkless"
	}
	return api.DurableEntityRestoreValidationResponse{BundleSHA256: bundleHash, Isolation: isolation, Valid: valid, DeploymentID: version.DeploymentID, ExpectedVersion: request.ExpectedVersion, SourceVersion: request.Export.Version}, nil
}

func durableEntityValidationDeploymentProblem() *api.Problem {
	return api.NewProblem(http.StatusConflict, "durable_entity_restore_validation_deployment_changed", "Validation deployment changed", "validate against the selected live deployment before starting a new restore operation")
}

func (s *Service) PerformRestore(ctx context.Context, acct state.Account, app state.App, scope string, id durableentity.ID, request api.DurableEntityRestoreRequest) (durableentity.Result, error) {
	if s.TenantQuotas != nil {
		if err := s.TenantQuotas.AdmitInvocation(ctx, id); err != nil {
			return durableentity.Result{}, err
		}
	}
	exported := durableEntityExportFromAPI(request.Export)
	if request.ValidationDeploymentID == "" {
		if request.ValidationBundleSHA256 != "" {
			return durableentity.Result{}, api.ErrValidation("validation bundle requires a deployment pin")
		}
		if s.RestoreValidationEnabled {
			return durableentity.Result{}, api.ErrValidation("application-validated restore requires validation_deployment_id")
		}
		return s.Engine.InvokeRestoreState(ctx, id, s.Owner, request.RequestID, request.ExpectedVersion, exported)
	}
	if !s.RestoreValidationEnabled {
		return durableentity.Result{}, api.NewProblem(http.StatusServiceUnavailable, "durable_entity_restore_validation_unavailable", "Restore validation unavailable", "the operator has not enabled application restore validation")
	}
	validate := func(ctx context.Context, candidate json.RawMessage) error {
		verdict, err := s.dispatchDurableEntityRestoreValidation(ctx, acct, app, scope, id, request, candidate)
		if err != nil {
			return err
		}
		if !verdict.Valid {
			return durableentity.ErrRestoreRejected
		}
		return nil
	}
	if s.RestoreIsolationEnabled {
		return s.Engine.InvokeIsolatedValidatedRestoreState(ctx, id, s.Owner, request.RequestID, request.ExpectedVersion, exported, request.ValidationDeploymentID, request.ValidationBundleSHA256, validate)
	}
	if request.ValidationBundleSHA256 != "" {
		return durableentity.Result{}, restoreIsolationUnavailable()
	}
	return s.Engine.InvokeValidatedRestoreState(ctx, id, s.Owner, request.RequestID, request.ExpectedVersion, exported, request.ValidationDeploymentID, validate)
}
