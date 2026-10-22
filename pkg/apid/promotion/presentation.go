package promotion

import (
	"encoding/json"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func Response(promotion state.ProjectEnvironmentPromotion, workloads []state.ProjectEnvironmentPromotionWorkload) api.ProjectEnvironmentPromotionResponse {
	results := make([]api.ProjectEnvironmentPromotionWorkloadResponse, 0, len(workloads))
	for _, workload := range workloads {
		results = append(results, api.ProjectEnvironmentPromotionWorkloadResponse{
			WorkloadSlug: workload.WorkloadSlug, WorkloadName: workload.WorkloadName,
			Status: workload.Status, SourceDeploymentID: workload.SourceDeploymentID,
			TargetDeploymentID: workload.TargetDeploymentID,
		})
	}
	return api.ProjectEnvironmentPromotionResponse{
		Status: promotion.Status, PromotionID: promotion.ID, ProjectSlug: promotion.ProjectSlug,
		FromEnvironment: promotion.FromEnvironment, ToEnvironment: promotion.ToEnvironment,
		SyncConfig: promotion.SyncConfig, PromotionHash: promotion.PromotionHash,
		ReleaseGraph: ReleaseGraph(promotion), Workloads: results,
	}
}

func ReleaseGraph(promotion state.ProjectEnvironmentPromotion) *api.ProjectEnvironmentPromotionReleaseGraphResponse {
	if !promotion.ReleaseGraphMode {
		return nil
	}
	return &api.ProjectEnvironmentPromotionReleaseGraphResponse{
		SourceReleaseSetID:         promotion.SourceReleaseSetID,
		PreviousTargetReleaseSetID: promotion.PreviousTargetReleaseSetID,
		TargetReleaseSetID:         promotion.TargetReleaseSetID,
		RestoredTargetReleaseSetID: promotion.RestoredTargetReleaseSetID,
		TTLSeconds:                 promotion.ReleaseTTLSeconds,
	}
}

func StatusResponse(promotion state.ProjectEnvironmentPromotion, workloads []state.ProjectEnvironmentPromotionWorkload) api.ProjectEnvironmentPromotionStatusResponse {
	items := make([]api.ProjectEnvironmentPromotionStatusWorkloadResponse, 0, len(workloads))
	for _, workload := range workloads {
		items = append(items, api.ProjectEnvironmentPromotionStatusWorkloadResponse{
			WorkloadSlug: workload.WorkloadSlug, WorkloadName: workload.WorkloadName,
			Status: workload.Status, SourceDeploymentID: workload.SourceDeploymentID,
			PreviousTargetDeploymentID: workload.PreviousTargetDeploymentID,
			TargetDeploymentID:         workload.TargetDeploymentID, Error: workload.Error,
			RollbackStatus: workload.RollbackStatus, RestoredTargetDeploymentID: workload.RestoredTargetDeploymentID,
			RollbackError: workload.RollbackError, VerificationStatus: workload.VerificationStatus,
			VerificationError: workload.VerificationError,
		})
	}
	return api.ProjectEnvironmentPromotionStatusResponse{
		PromotionID: promotion.ID, ProjectSlug: promotion.ProjectSlug,
		FromEnvironment: promotion.FromEnvironment, ToEnvironment: promotion.ToEnvironment,
		SyncConfig:    promotion.SyncConfig,
		PromotionHash: promotion.PromotionHash, Status: promotion.Status, Error: promotion.Error,
		RollbackStatus: promotion.RollbackStatus, RollbackError: promotion.RollbackError,
		RollbackStartedAt:   OptionalTime(promotion.RollbackStartedAt),
		RollbackCompletedAt: OptionalTime(promotion.RollbackCompletedAt),
		VerificationStatus:  promotion.VerificationStatus, VerificationError: promotion.VerificationError,
		VerificationStartedAt:   OptionalTime(promotion.VerificationStartedAt),
		VerificationCompletedAt: OptionalTime(promotion.VerificationCompletedAt),
		ReleaseGraph:            ReleaseGraph(promotion),
		CreatedAt:               promotion.CreatedAt.UTC().Format(time.RFC3339Nano),
		UpdatedAt:               promotion.UpdatedAt.UTC().Format(time.RFC3339Nano),
		CompletedAt: func() string {
			if promotion.CompletedAt == nil {
				return ""
			}
			return promotion.CompletedAt.UTC().Format(time.RFC3339Nano)
		}(),
		Workloads: items,
	}
}

func OptionalTime(value *time.Time) string {
	if value == nil {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func Workloads(changes []api.ProjectEnvironmentPromotionChange, targets map[string]state.Deployment, stageAll bool) []state.ProjectEnvironmentPromotionWorkload {
	workloads := make([]state.ProjectEnvironmentPromotionWorkload, 0, len(changes))
	for _, change := range changes {
		status := "pending"
		if change.Kind == "unchanged" && !stageAll {
			status = "unchanged"
		}
		previousTrafficPercent := 0
		if target, ok := targets[change.WorkloadSlug]; ok {
			previousTrafficPercent = target.TrafficPercent
		}
		workloads = append(workloads, state.ProjectEnvironmentPromotionWorkload{
			WorkloadSlug: change.WorkloadSlug, WorkloadName: change.WorkloadName,
			SourceDeploymentID:           change.SourceDeploymentID,
			PreviousTargetDeploymentID:   change.TargetDeploymentID,
			PreviousTargetTrafficPercent: previousTrafficPercent,
			TargetDeploymentID:           change.TargetDeploymentID, Status: status,
			VerificationStatus: "pending",
		})
	}
	return workloads
}

func ConfigSnapshot(config state.ProjectEnvironmentConfig) json.RawMessage {
	if len(config.Values) == 0 {
		return json.RawMessage(`{}`)
	}
	return append(json.RawMessage(nil), config.Values...)
}
