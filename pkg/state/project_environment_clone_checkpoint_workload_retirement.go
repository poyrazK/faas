package state

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// Shape validation supplies no native dispatch or configuration deletion
// authority. Durable journals must authenticate these ORIGINAL frozen records,
// materialization receipt and deployments under the compensation lease.
type ProjectEnvironmentCloneCheckpointWorkloadRetirementPlan struct {
	OperationID, AccountID, ProjectID                  string
	SourceEnvironment, TargetEnvironment               string
	SourceRevisionHash, SourceCompensationSHA256       string
	EnvironmentID, MaterializationSHA256, ReleaseSetID string
	SourceCompensatedAt                                time.Time
	Workloads                                          []ProjectEnvironmentCloneCheckpointRetirementWorkload
}

// Hashes retain ownership without copying settings, policies or secret values.
// An absent target deployment remains absent; native observations cannot invent
// a deployment that was not committed in the original catalogue.
type ProjectEnvironmentCloneCheckpointRetirementWorkload struct {
	AppID, SourceDeploymentID, SourceHash  string
	TargetDeploymentID, TargetSettingsHash string
	SpecID, SpecHash                       string
}

type ProjectEnvironmentCloneCheckpointWorkloadRetirement struct {
	Plan                     ProjectEnvironmentCloneCheckpointWorkloadRetirementPlan
	State, ObservationSHA256 string
	RequestedAt              time.Time
	Observation              ProjectEnvironmentCloneCheckpointWorkloadRetirementObservation
}

// Closure covers the complete original operation namespace, including queued
// scheduling and unknown instance creation outcomes. Process/instance not-found
// and local deployment labels cannot prove permanent closure. This native proof
// cannot assert that control-plane settings, values or policies were deleted;
// a future atomic catalogue transaction must independently authenticate those.
type ProjectEnvironmentCloneCheckpointWorkloadRetirementObservation struct {
	RequestID, PlanSHA256, OwnerToken                                    string
	AllDeploymentRequestsPermanentlyFenced, AllSchedulerOperationsClosed bool
	NoOwnedInstances, NoOwnedCgroups, NoOwnedNetworkResources            bool
	NoOwnedRoutes, NoNativePublication, SourceUnchanged                  bool
	Workloads                                                            []ProjectEnvironmentCloneCheckpointRetiredWorkload
	RetiredAt                                                            time.Time
}

type ProjectEnvironmentCloneCheckpointRetiredWorkload struct {
	AppID, TargetDeploymentID                               string
	DeploymentPermanentlyFenced, AllRuntimeOperationsClosed bool
}

func ProjectEnvironmentCloneCheckpointWorkloadRetirementRequestID(p ProjectEnvironmentCloneCheckpointWorkloadRetirementPlan) string {
	return "retire-checkpoint-workloads-" + p.OperationID
}

func ProjectEnvironmentCloneCheckpointWorkloadRetirementPlanSHA256(p ProjectEnvironmentCloneCheckpointWorkloadRetirementPlan) string {
	return cloneCheckpointSHA(p)
}

func checkpointWorkloadRetirementID(id string) bool {
	parsed, err := uuid.Parse(id)
	return err == nil && parsed != uuid.Nil && parsed.String() == id
}

func validateCheckpointWorkloadRetirementPlan(p ProjectEnvironmentCloneCheckpointWorkloadRetirementPlan) error {
	raw, err := json.Marshal(p)
	if err != nil {
		return ErrConflict
	}
	if len(raw) > api.CheckpointWorkloadRetirementMaxBytes {
		return ErrQuotaExceeded
	}
	if !checkpointWorkloadRetirementID(p.OperationID) || !checkpointWorkloadRetirementID(p.AccountID) || !checkpointWorkloadRetirementID(p.ProjectID) || !api.ValidProjectEnvironmentSlug(p.SourceEnvironment) || !api.ValidProjectEnvironmentSlug(p.TargetEnvironment) || p.TargetEnvironment == "production" || p.SourceEnvironment == p.TargetEnvironment || !checkpointObjectRetirementTime(p.SourceCompensatedAt) || p.SourceCompensatedAt.After(time.Now()) || !validCloneObjectSHA256(p.SourceRevisionHash) || !validCloneObjectSHA256(p.SourceCompensationSHA256) {
		return ErrConflict
	}
	if p.EnvironmentID == "" {
		if p.MaterializationSHA256 != "" || p.ReleaseSetID != "" {
			return ErrConflict
		}
	} else if len(p.Workloads) == 0 || !checkpointWorkloadRetirementID(p.EnvironmentID) || !validCloneObjectSHA256(p.MaterializationSHA256) || p.ReleaseSetID != "" && !checkpointWorkloadRetirementID(p.ReleaseSetID) {
		return ErrConflict
	}
	return validateCheckpointRetirementWorkloads(p)
}

func validateCheckpointRetirementWorkloads(p ProjectEnvironmentCloneCheckpointWorkloadRetirementPlan) error {
	protected := map[string]bool{p.OperationID: true, p.AccountID: true, p.ProjectID: true}
	for _, w := range p.Workloads {
		protected[w.AppID] = true
		if w.SourceDeploymentID != "" {
			protected[w.SourceDeploymentID] = true
		}
	}
	owned := map[string]bool{}
	for _, id := range []string{p.EnvironmentID, p.ReleaseSetID} {
		if id != "" {
			if protected[id] || owned[id] {
				return ErrConflict
			}
			owned[id] = true
		}
	}
	for i, w := range p.Workloads {
		if !checkpointWorkloadRetirementID(w.AppID) || w.AppID == p.OperationID || w.AppID == p.AccountID || w.AppID == p.ProjectID || i > 0 && p.Workloads[i-1].AppID >= w.AppID || !validCloneObjectSHA256(w.SourceHash) || w.SourceDeploymentID != "" && !checkpointWorkloadRetirementID(w.SourceDeploymentID) {
			return ErrConflict
		}
		if (w.TargetDeploymentID == "") != (w.TargetSettingsHash == "") || w.TargetDeploymentID != "" && (p.EnvironmentID == "" || !validCloneObjectSHA256(w.TargetSettingsHash)) || (w.SpecID == "") != (w.SpecHash == "") || (w.SpecID != "") != (p.EnvironmentID != "") || w.SpecID != "" && !validCloneObjectSHA256(w.SpecHash) {
			return ErrConflict
		}
		for _, id := range []string{w.TargetDeploymentID, w.SpecID} {
			if id == "" {
				continue
			}
			if !checkpointWorkloadRetirementID(id) || protected[id] || owned[id] {
				return ErrConflict
			}
			owned[id] = true
		}
	}
	return nil
}

func validateCheckpointWorkloadRetirementObservation(in ProjectEnvironmentCloneCheckpointWorkloadRetirement, o ProjectEnvironmentCloneCheckpointWorkloadRetirementObservation) error {
	if err := validateCheckpointWorkloadRetirementPlan(in.Plan); err != nil {
		return err
	}
	raw, err := json.Marshal(o)
	if err != nil {
		return ErrConflict
	}
	if len(raw) > api.CheckpointWorkloadRetirementMaxBytes {
		return ErrQuotaExceeded
	}
	if !checkpointObjectRetirementJournalShape(in.State, "retired", in.RequestedAt, in.ObservationSHA256) || in.State == "planned" || in.RequestedAt.Before(in.Plan.SourceCompensatedAt) || o.RequestID != ProjectEnvironmentCloneCheckpointWorkloadRetirementRequestID(in.Plan) || o.PlanSHA256 != cloneCheckpointSHA(in.Plan) || o.OwnerToken != in.Plan.OperationID || !checkpointObjectRetirementTime(o.RetiredAt) || o.RetiredAt.Before(in.RequestedAt) || o.RetiredAt.After(time.Now()) || !o.AllDeploymentRequestsPermanentlyFenced || !o.AllSchedulerOperationsClosed || !o.NoOwnedInstances || !o.NoOwnedCgroups || !o.NoOwnedNetworkResources || !o.NoOwnedRoutes || !o.NoNativePublication || !o.SourceUnchanged || len(o.Workloads) != len(in.Plan.Workloads) {
		return ErrConflict
	}
	for i, w := range in.Plan.Workloads {
		actual := o.Workloads[i]
		if actual.AppID != w.AppID || actual.TargetDeploymentID != w.TargetDeploymentID || actual.DeploymentPermanentlyFenced != (w.TargetDeploymentID != "") || actual.AllRuntimeOperationsClosed != (w.TargetDeploymentID != "") {
			return ErrConflict
		}
	}
	if in.State == "retired" && in.ObservationSHA256 != cloneCheckpointSHA(o) {
		return ErrConflict
	}
	return nil
}

// This private store saves native closure evidence only. It does not delete
// configuration or provide whole-stage terminal compensation authority.
type ProjectEnvironmentCloneCheckpointWorkloadRetirementStore interface {
	ReserveProjectEnvironmentCloneCheckpointWorkloadRetirement(context.Context, ProjectEnvironmentCloneLease) (ProjectEnvironmentCloneCheckpointWorkloadRetirement, error)
	ProjectEnvironmentCloneCheckpointWorkloadRetirementForLease(context.Context, ProjectEnvironmentCloneLease) (ProjectEnvironmentCloneCheckpointWorkloadRetirement, error)
	ClaimProjectEnvironmentCloneCheckpointWorkloadRetirement(context.Context, ProjectEnvironmentCloneLease) (ProjectEnvironmentCloneCheckpointWorkloadRetirement, bool, error)
	FinishProjectEnvironmentCloneCheckpointWorkloadRetirement(context.Context, ProjectEnvironmentCloneLease, ProjectEnvironmentCloneCheckpointWorkloadRetirementObservation) error
}
