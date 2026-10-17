package promotion

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func HandleMigrationRecovery(w http.ResponseWriter, r *http.Request, account state.Account, store state.Store, record func(*api.ProjectPromotionMigrationRecovery)) {
	var req api.ProjectPromotionMigrationRecoveryRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, api.ProjectReleaseControlMaxBytes))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&req) != nil || decoder.Decode(new(any)) != io.EOF || !req.Valid() {
		api.WriteProblem(w, api.ErrValidation("require task/generation, resume or resolve action, database inspection and schema attestations, evidence hash and request ID"))
		return
	}
	project, environment, id := r.PathValue("slug"), r.PathValue("environment"), r.PathValue("promotion")
	if _, _, err := store.ProjectEnvironmentPromotionByID(r.Context(), account.ID, project, environment, id); err != nil {
		writeRecoveryError(w, err)
		return
	}
	recoveries, ok := store.(state.ProjectPromotionMigrationRecoveryStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("promotion recovery unavailable"))
		return
	}
	receipt, err := recoveries.RequestProjectPromotionMigrationRecovery(r.Context(), account.ID, id, req)
	if err != nil {
		writeRecoveryError(w, err)
		return
	}
	if record != nil {
		record(receipt)
	}
	status := http.StatusAccepted
	if receipt.Status == "resumed" || receipt.Status == "resolved" || receipt.Status == "blocked" {
		status = http.StatusOK
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(receipt)
}
func writeRecoveryError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, state.ErrNotFound):
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeValidation, "Promotion not found", "the requested promotion recovery is unavailable"))
	case errors.Is(err, state.ErrInvalidArgument):
		api.WriteProblem(w, api.ErrValidation("invalid promotion migration recovery request"))
	case errors.Is(err, state.ErrConflict):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeValidation, "Recovery cannot proceed", "task, generation, runtime or destination intent changed, or a verified repair promotion is required; inspect current status"))
	default:
		api.WriteProblem(w, api.ErrCapacity("could not record promotion migration recovery"))
	}
}

func reconcileMigrationRecovery(ctx context.Context, store state.Store, job state.PromotionHealthJob, a HealthActions, r *api.ProjectPromotionMigrationRecovery) error {
	recoveries := store.(state.ProjectPromotionMigrationRecoveryStore)
	if !time.Now().Before(r.Deadline) {
		return recoveries.BlockProjectPromotionMigrationRecovery(ctx, job.AccountID, job.PromotionID, r.Request.RequestID, "recovery_deadline_expired")
	}
	if r.Status == "quiescing" {
		return nil
	}
	evidence := state.ProjectMigrationRecoveryEvidence{}
	if r.Request.Action == "resolve" {
		original, _, err := store.ProjectEnvironmentPromotionByID(ctx, job.AccountID, job.ProjectSlug, job.ToEnvironment, job.PromotionID)
		if err != nil {
			return err
		}
		other, _, err := store.ProjectEnvironmentPromotionByID(ctx, job.AccountID, job.ProjectSlug, job.ToEnvironment, r.Request.ReplacementPromotionID)
		if err != nil {
			return err
		}
		if other.ProjectID != original.ProjectID || other.Status != "succeeded" || a.RecoveryProbe == nil {
			return recoveries.BlockProjectPromotionMigrationRecovery(ctx, job.AccountID, job.PromotionID, r.Request.RequestID, "replacement_changed")
		}
		if a.Verify != nil {
			if err = a.Verify(ctx, other); err != nil {
				return recoveries.BlockProjectPromotionMigrationRecovery(ctx, job.AccountID, job.PromotionID, r.Request.RequestID, "replacement_verification_failed")
			}
		}
		graph, err := store.(state.ProjectReleaseSetReader).ActiveProjectReleaseSet(ctx, job.AccountID, other.ProjectID, job.ToEnvironment)
		if err != nil {
			return err
		}
		if graph.ID != other.TargetReleaseSetID {
			return recoveries.BlockProjectPromotionMigrationRecovery(ctx, job.AccountID, job.PromotionID, r.Request.RequestID, "replacement_changed")
		}
		h, err := store.(state.ProjectPromotionHealthStore).ProjectEnvironmentPromotionHealth(ctx, job.AccountID, other.ID)
		if err != nil || h == nil {
			return err
		}
		evidence.StartedAt = time.Now().UTC()
		evidence.RuntimeFingerprints = map[string]string{}
		for _, member := range graph.Members {
			snapshot, err := store.RuntimeAppValuesForDeployment(ctx, job.AccountID, member.AppID, member.DeploymentID)
			if err != nil {
				return err
			}
			fence, err := state.NewRuntimeAppConfigFence(snapshot)
			if err != nil {
				return err
			}
			evidence.RuntimeFingerprints[member.AppID] = fence.Fingerprint
		}
		probeCtx, cancel := context.WithTimeout(ctx, api.ProjectPromotionHealthRoundTimeout)
		evidence.Probes, err = a.RecoveryProbe(probeCtx, other, h.Policy, graph)
		cancel()
		if err != nil {
			return err
		}
		if state.ProjectReleaseProbeVerdict(h.Policy.Probes, evidence.Probes) != "healthy" {
			return nil
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	result, err := recoveries.FinalizeProjectPromotionMigrationRecovery(ctx, job.AccountID, job.PromotionID, r.Request.RequestID, evidence)
	if errors.Is(err, state.ErrPromotionWorkerNotReady) {
		return nil // Wait for current startup and sufficient healthy delivery evidence.
	}
	if errors.Is(err, state.ErrConflict) {
		return recoveries.BlockProjectPromotionMigrationRecovery(ctx, job.AccountID, job.PromotionID, r.Request.RequestID, "runtime_or_destination_changed")
	}
	if err == nil && result != nil && (result.Status == "resumed" || result.Status == "resolved") && a.RecoveryRecord != nil {
		a.RecoveryRecord(ctx, job, result)
	}
	return err
}
