package state

import (
	"context"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// Only schedd uses this capability. Admission records a reservation, not a
// readiness receipt, qualified snapshot or serving deployment.
type EnvironmentGitOpsQualificationInstanceStore interface {
	CreateEnvironmentWorkloadQualificationInstance(context.Context, EnvironmentWorkloadQualificationRequest, EnvironmentWorkloadQualificationPlacement) (EnvironmentWorkloadQualificationAdmission, error)
}

type EnvironmentWorkloadQualificationPlacement struct {
	NodeID string
	WakeID string
	RAMMB  int
}

type EnvironmentWorkloadQualificationAdmission struct {
	Instance Instance
	Created  bool
}

func qualificationPlacementValid(placement EnvironmentWorkloadQualificationPlacement) bool {
	_, nodeErr := uuid.Parse(placement.NodeID)
	_, wakeErr := uuid.Parse(placement.WakeID)
	return nodeErr == nil && wakeErr == nil && placement.RAMMB > 0
}

func qualificationInstanceMode(request EnvironmentWorkloadQualificationRequest) string {
	switch request.ExecutionMode {
	case api.ExecutionModeWorker:
		return string(InstanceModeWorker)
	case api.ExecutionModeService:
		return string(InstanceModeService)
	default:
		return string(InstanceModeNormal)
	}
}

func qualificationAdmissionMatches(ins Instance, request EnvironmentWorkloadQualificationRequest, placement EnvironmentWorkloadQualificationPlacement) bool {
	return ins.ID == request.ReservedInstanceID && ins.AppID == request.AppID && ins.DeploymentID == request.DeploymentID &&
		ins.NodeID == placement.NodeID && ins.WakeID == placement.WakeID && ins.RAMMB == placement.RAMMB && ins.Mode == qualificationInstanceMode(request) &&
		ins.Kind == "wake" && ins.JobID == "" && !qualificationInstanceRetired(ins) && State(ins.State) != StateEvictingAccountDeleting
}
