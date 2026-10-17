package promotion

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func HandleControl(w http.ResponseWriter, r *http.Request, account state.Account, store state.Store, record func(*api.ProjectPromotionControl)) {
	var req api.ProjectPromotionControlRequest
	action := r.PathValue("action")
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, api.ProjectReleaseControlMaxBytes))
	decoder.DisallowUnknownFields()
	if action != "abort" && action != "recover" || decoder.Decode(&req) != nil || decoder.Decode(new(any)) != io.EOF || !req.Valid() {
		api.WriteProblem(w, api.ErrValidation("require abort or recover, an inspected generation, and a stable request ID"))
		return
	}
	if _, _, err := store.ProjectEnvironmentPromotionByID(r.Context(), account.ID, r.PathValue("slug"), r.PathValue("environment"), r.PathValue("promotion")); err != nil {
		writeControlError(w, err)
		return
	}
	controls, ok := store.(state.ProjectPromotionControlStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("promotion controls unavailable"))
		return
	}
	receipt, err := controls.RequestProjectPromotionControl(r.Context(), account.ID, r.PathValue("promotion"), action, req)
	if err != nil {
		writeControlError(w, err)
		return
	}
	if record != nil {
		record(receipt)
	}
	status := http.StatusOK
	if receipt.Status == "pending" {
		status = http.StatusAccepted
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(receipt)
}

func writeControlError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, state.ErrNotFound):
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeValidation, "Promotion not found", "the requested promotion is unavailable"))
	case errors.Is(err, state.ErrInvalidArgument):
		api.WriteProblem(w, api.ErrValidation("invalid promotion control request"))
	case errors.Is(err, state.ErrConflict):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeValidation, "Promotion control cannot proceed", "inspect the current generation, health phase and destination intent; migration incidents use recover-migration"))
	default:
		api.WriteProblem(w, api.ErrCapacity("could not record promotion control"))
	}
}

func HealthAudit(p state.ProjectEnvironmentPromotion, h *api.ProjectPromotionHealth) (string, map[string]any) {
	event := "project.environment.promotion_health_failed"
	if h.Control != nil && h.Control.Status != "pending" {
		event = "project.environment.promotion_control_completed"
	} else if h.Phase == "succeeded" {
		event = "project.environment.promoted"
	} else if h.Phase != "failed" && h.Phase != "conflicted" && h.Phase != "rollback_failed" {
		return "", nil
	}
	fields := map[string]any{"promotion_id": p.ID, "project_slug": p.ProjectSlug, "from_environment": p.FromEnvironment, "to_environment": p.ToEnvironment, "promotion_hash": p.PromotionHash, "health_phase": h.Phase, "health_verdict": h.Verdict}
	if h.Control != nil {
		fields["request_id"], fields["action"], fields["control_status"] = h.Control.Request.RequestID, h.Control.Action, h.Control.Status
	}
	return event, fields
}

func BaselineProbeInputs(policy api.ProjectPromotionHealthPolicy, graph state.ProjectReleaseSet) api.ProjectSubmissionReceipt {
	receipt := api.ProjectSubmissionReceipt{Apply: api.ApplyResponse{PlanResponse: api.PlanResponse{SafeRelease: &api.ProjectSafeRelease{Verification: &api.ProjectReleaseVerification{Probes: policy.Probes}}}}}
	for _, member := range graph.Members {
		if api.ProjectPromotionHasWorker(policy, member.AppID) {
			continue
		}
		receipt.Apply.Builds = append(receipt.Apply.Builds, api.AppliedBuild{AppID: member.AppID, DeploymentID: member.DeploymentID})
	}
	return receipt
}
