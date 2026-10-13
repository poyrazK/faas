package state

import (
	"reflect"
	"sort"
)

// A complete PostgreSQL projection of the frozen workload catalogue. Object
// storage, workload materialization and release publication require their own
// authenticated graph; this projection grants no serving authority.
type ProjectEnvironmentCloneCheckpointPgBindingGraph struct {
	OperationID, TargetEnvironment, SourceRevisionHash, ActivationPlanSHA256, PublicationPlanSHA256 string
	Bindings                                                                                        []ProjectEnvironmentCloneCheckpointPgBindingEdge
	SHA256                                                                                          string `json:"-"`
}

type ProjectEnvironmentCloneCheckpointPgBindingEdge struct {
	AppID, SourceScope, SourceBindingsSHA256, SourceBindingID, SourceDatabaseID string
	Target                                                                      ProjectEnvironmentClonePostgresBinding
}

// Provider identities are opaque within their placement and resource kind.
// Equal strings across two backends do not identify the same native resource.
type checkpointPgResourceKey struct {
	BackendID, BackendFingerprint, Kind, ID string
}

func checkpointPgBindingGraph(op ProjectEnvironmentCloneOperation, views []ProjectEnvironmentCloneBindings, activation ProjectEnvironmentCloneCheckpointPgActivation, publication ProjectEnvironmentCloneCheckpointPgPublication, preparations map[string]ProjectEnvironmentClonePostgresBindingPreparation) (ProjectEnvironmentCloneCheckpointPgBindingGraph, error) {
	zero := ProjectEnvironmentCloneCheckpointPgBindingGraph{}
	// Recheck provenance even though the enclosing transaction authenticates the
	// durable journals and locks the complete original graph and live owned rows.
	original, err := checkpointPgWorkloadPreparations(op, activation, publication)
	if err != nil || !reflect.DeepEqual(original, preparations) {
		return zero, ErrProjectEnvironmentCloneManagedValueProof
	}
	byID := map[string]ProjectEnvironmentCloneCheckpointPostgresCredentialPlan{}
	for _, member := range activation.Plan.Bindings {
		c := member.Connection.Credential
		byID[c.SourceBindingID] = c
	}
	sources, sourceDatabases := map[string]bool{}, map[string]bool{}
	sourceResources := map[checkpointPgResourceKey]bool{}
	apps := map[string]bool{}
	sourceDefinitions := map[string]ProjectEnvironmentClonePostgresBinding{}
	for _, view := range views {
		hash, err := cloneBindingDefinitionsHash(view.ProjectEnvironmentCloneBindingDefinitions)
		if err != nil || hash != view.Hash || view.AppID == "" || view.SourceScope == "" || apps[view.AppID] {
			return zero, ErrProjectEnvironmentCloneManagedValueProof
		}
		apps[view.AppID] = true
		for _, source := range view.Postgres {
			if !validClonePostgresBinding(source) || source.DataResourceID == "" || sources[source.ID] {
				return zero, ErrProjectEnvironmentCloneManagedValueProof
			}
			if previous, exists := sourceDefinitions[source.DatabaseID]; exists && !sameClonePostgresDatabase(previous, source) {
				return zero, ErrProjectEnvironmentCloneManagedValueProof
			}
			sourceDefinitions[source.DatabaseID] = source
			sources[source.ID], sourceDatabases[source.DatabaseID] = true, true
			sourceResources[checkpointPgResourceKey{source.BackendID, source.BackendFingerprint, "native", source.ProviderResourceID}] = true
			sourceResources[checkpointPgResourceKey{source.BackendID, source.BackendFingerprint, "data", source.DataResourceID}] = true
		}
	}
	if len(sources) != len(byID) {
		return zero, ErrProjectEnvironmentCloneManagedValueProof
	}
	graph := ProjectEnvironmentCloneCheckpointPgBindingGraph{OperationID: op.ID, TargetEnvironment: op.TargetEnvironment, SourceRevisionHash: op.SourceRevisionHash, ActivationPlanSHA256: cloneCheckpointSHA(activation.Plan), PublicationPlanSHA256: publication.PlanSHA256}
	targets := map[string]bool{}
	databases := map[string]ProjectEnvironmentClonePostgresBinding{}
	owners := map[string]string{}
	resources := map[checkpointPgResourceKey]string{}
	for _, view := range views {
		for _, source := range view.Postgres {
			c, ok := byID[source.ID]
			prepared, exists := preparations[source.ID]
			p := c.Runtime.Verification.Isolation
			if !ok || !exists || validateClonePreparedPostgresIdentity(op, source.ID, view.AppID, prepared) != nil ||
				c.AppID != view.AppID || c.EnvironmentKey != source.EnvironmentKey || c.Access != source.Access || p.SourceDatabaseID != source.DatabaseID ||
				p.BackendID != source.BackendID || p.BackendFingerprint != source.BackendFingerprint || p.SourceDataResourceID != source.DataResourceID || c.Runtime.Spec != checkpointPostgresRuntimeSpec(source) ||
				prepared.Binding.ID != c.TargetBindingID || prepared.Binding.DatabaseID != p.OwnerID || prepared.Binding.ProviderIdentityID != c.ProviderIdentityID || prepared.Binding.Access != c.Access ||
				prepared.Binding.BackendID != p.BackendID || prepared.Binding.BackendFingerprint != p.BackendFingerprint || prepared.Binding.DatabaseProviderResourceID != p.NativeResourceID ||
				prepared.Binding.CredentialRef == source.CredentialRef || sources[c.TargetBindingID] || targets[c.TargetBindingID] || sourceDatabases[p.OwnerID] ||
				sourceResources[checkpointPgResourceKey{p.BackendID, p.BackendFingerprint, "native", p.NativeResourceID}] || sourceResources[checkpointPgResourceKey{p.BackendID, p.BackendFingerprint, "data", c.DataResourceID}] {
				return zero, ErrProjectEnvironmentCloneManagedValueProof
			}
			target := source
			target.ID, target.DatabaseID = c.TargetBindingID, p.OwnerID
			target.DatabaseName = ProjectEnvironmentClonePostgresSnapshotRestoreDatabaseName(op, source.DatabaseID)
			target.ProviderResourceID, target.DataResourceID = p.NativeResourceID, c.DataResourceID
			target.CredentialRef, target.CredentialGeneration = prepared.Binding.CredentialRef, c.CredentialGeneration
			if !validClonePostgresBinding(target) || target.DataResourceID == "" {
				return zero, ErrProjectEnvironmentCloneManagedValueProof
			}
			if previous, exists := databases[source.DatabaseID]; exists && !sameClonePostgresDatabase(previous, target) {
				return zero, ErrProjectEnvironmentCloneManagedValueProof
			}
			if owner, exists := owners[target.DatabaseID]; exists && owner != source.DatabaseID {
				return zero, ErrProjectEnvironmentCloneManagedValueProof
			}
			for _, key := range []checkpointPgResourceKey{{target.BackendID, target.BackendFingerprint, "native", target.ProviderResourceID}, {target.BackendID, target.BackendFingerprint, "data", target.DataResourceID}} {
				if owner, exists := resources[key]; exists && owner != source.DatabaseID {
					return zero, ErrProjectEnvironmentCloneManagedValueProof
				}
				resources[key] = source.DatabaseID
			}
			targets[target.ID], owners[target.DatabaseID], databases[source.DatabaseID] = true, source.DatabaseID, target
			graph.Bindings = append(graph.Bindings, ProjectEnvironmentCloneCheckpointPgBindingEdge{AppID: view.AppID, SourceScope: view.SourceScope, SourceBindingsSHA256: view.Hash, SourceBindingID: source.ID, SourceDatabaseID: source.DatabaseID, Target: target})
		}
	}
	sort.Slice(graph.Bindings, func(i, j int) bool { return graph.Bindings[i].SourceBindingID < graph.Bindings[j].SourceBindingID })
	graph.SHA256 = cloneCheckpointSHA(graph)
	return graph, nil
}
