package state

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentsync"
)

// EnvironmentWorkloadGraph records one complete reviewed preparation cohort.
// Prepared means artifacts exist; it does not authorize workload execution.
type EnvironmentWorkloadGraph struct {
	ID               string                           `json:"id"`
	SourceID         string                           `json:"source_id"`
	EnvironmentID    string                           `json:"environment_id"`
	RevisionID       string                           `json:"revision_id"`
	Generation       int64                            `json:"generation"`
	IntentVersion    int64                            `json:"intent_version"`
	PlanHash         string                           `json:"plan_hash"`
	DefinitionDigest string                           `json:"definition_digest"`
	Phase            string                           `json:"phase"`
	ErrorCode        string                           `json:"error_code,omitempty"`
	Members          []EnvironmentWorkloadGraphMember `json:"members"`
	ResourceIDs      map[string]string                `json:"resource_ids"`
	CreatedAt        time.Time                        `json:"created_at"`
	PreparedAt       *time.Time                       `json:"prepared_at,omitempty"`
}

type EnvironmentWorkloadGraphMember struct {
	Resource                  string                                     `json:"resource"`
	AppID                     string                                     `json:"app_id"`
	Variables                 map[string]string                          `json:"variables,omitempty"`
	JobID                     string                                     `json:"job_id,omitempty"`
	Function                  bool                                       `json:"function,omitempty"`
	CandidateDeploymentID     string                                     `json:"candidate_deployment_id,omitempty"`
	ExecutionMode             string                                     `json:"execution_mode,omitempty"`
	QueueBindings             map[string]EnvironmentScopedQueueBinding   `json:"queue_bindings,omitempty"`
	QueueModes                map[string]string                          `json:"queue_modes,omitempty"`
	QueueBindingsConfigured   bool                                       `json:"queue_bindings_configured,omitempty"`
	JobSmokeConfigured        bool                                       `json:"job_smoke_configured,omitempty"`
	ScheduleConfigured        bool                                       `json:"schedule_configured,omitempty"`
	ServiceBindingsConfigured bool                                       `json:"service_bindings_configured,omitempty"`
	ServiceBindings           map[string]EnvironmentScopedServiceBinding `json:"service_bindings,omitempty"`
	RetainedDeployments       []string                                   `json:"retained_deployments"`
}

func qualificationGraphJobQueueBindingsSupported(graph EnvironmentWorkloadGraph, resource string) bool {
	for _, member := range graph.Members {
		if member.Resource == resource {
			return member.ExecutionMode == api.ExecutionModeJob && member.JobSmokeConfigured &&
				!member.QueueBindingsConfigured && len(member.QueueBindings) == 0 && len(member.QueueModes) == 0
		}
	}
	return false
}

type EnvironmentGitOpsGraphPreparationStore interface {
	ReconcileEnvironmentGitOpsPreparation(context.Context, EnvironmentGitOpsLease, environmentsync.Plan) (EnvironmentWorkloadGraph, error)
}

// EnvironmentGitOpsGraphActivationStore publishes a qualified workload cohort
// as one release-set transition. activated=false means the graph is not yet
// qualified or is missing a release prerequisite; it is a normal wait state.
type EnvironmentGitOpsGraphActivationStore interface {
	ActivateEnvironmentGitOpsWorkloadGraph(context.Context, EnvironmentGitOpsLease, environmentsync.Plan) (ProjectReleaseSet, bool, error)
}

func preparationGraph(snapshot gitOpsIntentSnapshot, revision EnvironmentDesiredRevision, generation int64, planHash string, candidates []EnvironmentWorkloadCandidate) (EnvironmentWorkloadGraph, error) {
	desired, err := desiredEnvironmentRevision(revision)
	if err != nil {
		return EnvironmentWorkloadGraph{}, err
	}
	observed, err := compileGitOpsObservation(snapshot, desired)
	if err != nil {
		return EnvironmentWorkloadGraph{}, err
	}
	graph := EnvironmentWorkloadGraph{SourceID: snapshot.SourceID, EnvironmentID: snapshot.EnvironmentID, RevisionID: revision.ID,
		Generation: generation, IntentVersion: snapshot.Version, PlanHash: planHash, DefinitionDigest: revision.Digest,
		Phase: "preparing", Members: []EnvironmentWorkloadGraphMember{}, ResourceIDs: maps.Clone(observed.State.ResourceIDs)}
	apps, prepared := map[string]gitOpsIntentApp{}, map[string]EnvironmentWorkloadCandidate{}
	for _, app := range snapshot.Apps {
		apps[app.ID] = app
	}
	for _, candidate := range candidates {
		prepared[candidate.Resource] = candidate
	}
	names := slices.Sorted(maps.Keys(desired.Definition.Workloads))
	for _, name := range names {
		resource := "workload/" + name
		app := apps[graph.ResourceIDs[resource]]
		if app.ID == "" {
			return EnvironmentWorkloadGraph{}, fmt.Errorf("%w: graph member %s has no mapped app", ErrEnvironmentWorkloadPreparationUnavailable, resource)
		}
		executionMode := app.Manifest.ExecutionMode
		if executionMode == "" {
			executionMode = api.ExecutionModeRequest
		}
		var runtimeFields map[string]json.RawMessage
		if json.Unmarshal(desired.Definition.Workloads[name].Runtime, &runtimeFields) == nil {
			if rawMode, exists := runtimeFields["execution_mode"]; exists {
				var reviewedMode string
				if json.Unmarshal(rawMode, &reviewedMode) == nil && reviewedMode != "" {
					executionMode = reviewedMode
				}
			}
		}
		queueBindings, err := reviewedEnvironmentQueueBindings(EnvironmentGitSource{ID: snapshot.SourceID}, snapshot,
			resource, desired.Definition.Workloads[name], app)
		if err != nil {
			return EnvironmentWorkloadGraph{}, err
		}
		serviceBindings := map[string]EnvironmentScopedServiceBinding{}
		for bindingName, binding := range desired.Definition.Workloads[name].ServiceBindings {
			targetAppID := graph.ResourceIDs["workload/"+binding.Workload]
			if !api.ValidAppSlug(bindingName) || !api.ValidAppSlug(binding.Workload) || api.ValidateEnvKey(binding.EnvKey) != nil ||
				!canonicalStateUUID(targetAppID) || targetAppID == app.ID {
				return EnvironmentWorkloadGraph{}, fmt.Errorf("%w: graph binding %s/%s has no distinct mapped target (caller=%q target=%q target_workload=%q)", ErrEnvironmentWorkloadPreparationUnavailable, resource, bindingName, app.ID, targetAppID, binding.Workload)
			}
			serviceBindings[bindingName] = EnvironmentScopedServiceBinding{Workload: binding.Workload, EnvKey: binding.EnvKey, TargetAppID: targetAppID}
		}
		member := EnvironmentWorkloadGraphMember{Resource: resource, AppID: app.ID, Variables: cloneStringMap(desired.Definition.Workloads[name].Variables), Function: app.Type == AppTypeFunction, ExecutionMode: executionMode,
			QueueBindings: queueBindings, QueueModes: map[string]string{}, QueueBindingsConfigured: len(desired.Definition.Workloads[name].QueueBindings) != 0,
			JobSmokeConfigured:        desired.Definition.Workloads[name].JobSmoke != nil,
			ScheduleConfigured:        desired.Definition.Workloads[name].Schedule != nil,
			ServiceBindingsConfigured: len(serviceBindings) != 0, ServiceBindings: serviceBindings,
			RetainedDeployments: []string{}}
		if app.WorkloadIntent != nil {
			member.JobID = app.WorkloadIntent.JobID
		}
		for bindingName, binding := range queueBindings {
			if binding.Contract.Enabled != nil && *binding.Contract.Enabled {
				member.QueueModes[bindingName] = binding.Contract.Mode
			}
		}
		if len(member.QueueModes) == 0 {
			member.QueueModes = nil
		}
		for _, source := range app.Sources {
			member.RetainedDeployments = append(member.RetainedDeployments, source.ID)
		}
		slices.Sort(member.RetainedDeployments)
		if candidate, exists := prepared[resource]; exists {
			if candidate.AppID != app.ID {
				return EnvironmentWorkloadGraph{}, ErrConflict
			}
			member.CandidateDeploymentID = candidate.DeploymentID
		}
		graph.Members = append(graph.Members, member)
	}
	return graph, nil
}

func clonePreparationGraph(graph EnvironmentWorkloadGraph) EnvironmentWorkloadGraph {
	raw, _ := json.Marshal(graph)
	var copy EnvironmentWorkloadGraph
	_ = json.Unmarshal(raw, &copy)
	return copy
}

func preparationGraphPhase(candidates []EnvironmentWorkloadCandidate) (string, string) {
	ready := true
	for _, candidate := range candidates {
		if candidate.Status.IsTerminal() || candidate.Status == DeploySuperseded {
			return "failed", "environment_workload_artifact_failed"
		}
		ready = ready && candidate.Status == DeploySnapshotting && candidate.HasRootfs
	}
	if ready {
		return "prepared", ""
	}
	return "preparing", ""
}

func preparationGraphKey(generation int64, planHash string) string {
	return strconv.FormatInt(generation, 10) + "/" + planHash
}
