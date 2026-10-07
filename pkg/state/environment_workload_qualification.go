package state

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentsync"
)

// EnvironmentWorkloadQualificationRequest binds qualification work to one
// prepared graph member and its exact artifact. Claimed is not qualified.
type EnvironmentWorkloadQualificationRequest struct {
	ID                 string                      `json:"id"`
	GraphID            string                      `json:"graph_id"`
	DeploymentID       string                      `json:"deployment_id"`
	AppID              string                      `json:"app_id"`
	Resource           string                      `json:"resource"`
	Artifact           EnvironmentWorkloadArtifact `json:"artifact"`
	FrozenInputs       EnvironmentWorkloadRuntime  `json:"-"`
	ExecutionMode      string                      `json:"execution_mode"`
	Phase              string                      `json:"phase"`
	CreatedAt          time.Time                   `json:"created_at"`
	WorkerID           string                      `json:"worker_id,omitempty"`
	LeaseUntil         *time.Time                  `json:"lease_until,omitempty"`
	Attempt            int64                       `json:"attempt"`
	ReservedInstanceID string                      `json:"reserved_instance_id,omitempty"`
	LeaseToken         string                      `json:"-"`
}

type EnvironmentWorkloadArtifact struct {
	RootfsPath  string         `json:"rootfs_path"`
	RootfsKey   string         `json:"rootfs_key"`
	RootfsBytes int64          `json:"rootfs_bytes"`
	ImageDigest string         `json:"image_digest"`
	BuildID     string         `json:"build_id"`
	Kind        DeploymentKind `json:"kind"`
	CommitSHA   string         `json:"commit_sha"`
}

// apid publishes the complete cohort under its intent lease. Execution owners
// claim a separate token and revalidate it before each side effect. The
// ordinary deployment/instance paths keep their existing candidate hold.
type EnvironmentGitOpsQualificationStore interface {
	QueueEnvironmentGitOpsQualification(context.Context, EnvironmentGitOpsLease, environmentsync.Plan) ([]EnvironmentWorkloadQualificationRequest, error)
	ClaimEnvironmentWorkloadQualification(context.Context, string, string, time.Duration) (EnvironmentWorkloadQualificationRequest, error)
	ValidateEnvironmentWorkloadQualification(context.Context, EnvironmentWorkloadQualificationRequest) error
	RenewEnvironmentWorkloadQualification(context.Context, EnvironmentWorkloadQualificationRequest, time.Duration) (EnvironmentWorkloadQualificationRequest, error)
}

func environmentWorkloadArtifact(dep Deployment) EnvironmentWorkloadArtifact {
	return EnvironmentWorkloadArtifact{RootfsPath: dep.RootfsPath, RootfsKey: dep.RootfsKey, RootfsBytes: dep.RootfsBytes,
		ImageDigest: dep.ImageDigest, BuildID: dep.BuildID, Kind: dep.Kind, CommitSHA: dep.CommitSHA}
}

func qualificationRequest(graph EnvironmentWorkloadGraph, member EnvironmentWorkloadGraphMember, dep Deployment) (EnvironmentWorkloadQualificationRequest, error) {
	frozen, err := dep.ScopedWorkloadRuntime()
	if err != nil || frozen == nil || graph.Phase != "prepared" || dep.Status != DeploySnapshotting || dep.RootfsBytes <= 0 || dep.RootfsKey == "" && dep.RootfsPath == "" ||
		dep.ID != member.CandidateDeploymentID || dep.AppID != member.AppID || frozen.Resource != member.Resource || frozen.SourceID != graph.SourceID ||
		frozen.EnvironmentID != graph.EnvironmentID || frozen.RevisionID != graph.RevisionID || frozen.Generation != graph.Generation || frozen.IntentVersion != graph.IntentVersion || frozen.PlanHash != graph.PlanHash {
		return EnvironmentWorkloadQualificationRequest{}, ErrConflict
	}
	app, err := AppForDeploymentRuntime(App{ID: dep.AppID}, dep)
	if err != nil {
		return EnvironmentWorkloadQualificationRequest{}, err
	}
	mode := app.Manifest.ExecutionMode
	if mode == "" {
		mode = api.ExecutionModeRequest
	}
	return EnvironmentWorkloadQualificationRequest{GraphID: graph.ID, DeploymentID: dep.ID, AppID: dep.AppID, Resource: member.Resource,
		Artifact: environmentWorkloadArtifact(dep), FrozenInputs: *frozen, ExecutionMode: mode, Phase: "queued"}, nil
}

func cloneQualificationRequest(request EnvironmentWorkloadQualificationRequest) EnvironmentWorkloadQualificationRequest {
	// Frozen inputs and the execution token intentionally have no public JSON
	// projection, so copy those explicitly with the ordinary JSON fields.
	raw, _ := json.Marshal(request)
	var copy EnvironmentWorkloadQualificationRequest
	_ = json.Unmarshal(raw, &copy)
	raw, _ = json.Marshal(request.FrozenInputs)
	_ = json.Unmarshal(raw, &copy.FrozenInputs)
	copy.LeaseToken = request.LeaseToken
	return copy
}

func qualificationLeaseDurationValid(duration time.Duration) bool {
	return duration >= time.Microsecond && duration <= api.EnvironmentGitOpsQualificationMaxLeaseDuration
}

func qualificationClaimArgumentsValid(id, workerID string, duration time.Duration) bool {
	_, err := uuid.Parse(id)
	return err == nil && workerID != "" && workerID == strings.TrimSpace(workerID) && len(workerID) <= api.EnvironmentGitOpsQualificationWorkerIDMaxBytes && qualificationLeaseDurationValid(duration)
}

func qualificationLeaseMatches(current, claimed EnvironmentWorkloadQualificationRequest, now time.Time) bool {
	return current.ID == claimed.ID && current.GraphID == claimed.GraphID && current.DeploymentID == claimed.DeploymentID && current.AppID == claimed.AppID &&
		current.Resource == claimed.Resource && current.Artifact == claimed.Artifact && current.ExecutionMode == claimed.ExecutionMode && reflect.DeepEqual(current.FrozenInputs, claimed.FrozenInputs) &&
		current.ReservedInstanceID == claimed.ReservedInstanceID &&
		current.Phase == "claimed" && current.Phase == claimed.Phase && current.Attempt == claimed.Attempt && current.LeaseToken != "" && current.LeaseToken == claimed.LeaseToken &&
		current.WorkerID == claimed.WorkerID && current.LeaseUntil != nil && now.Before(*current.LeaseUntil)
}
