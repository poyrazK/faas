package state

import (
	"context"
	"encoding/json"
	"maps"
	"slices"
	"strconv"
	"time"

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
	Resource              string   `json:"resource"`
	AppID                 string   `json:"app_id"`
	CandidateDeploymentID string   `json:"candidate_deployment_id,omitempty"`
	RetainedDeployments   []string `json:"retained_deployments"`
}

type EnvironmentGitOpsGraphPreparationStore interface {
	ReconcileEnvironmentGitOpsPreparation(context.Context, EnvironmentGitOpsLease, environmentsync.Plan) (EnvironmentWorkloadGraph, error)
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
			return EnvironmentWorkloadGraph{}, ErrEnvironmentWorkloadPreparationUnavailable
		}
		member := EnvironmentWorkloadGraphMember{Resource: resource, AppID: app.ID, RetainedDeployments: []string{}}
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
