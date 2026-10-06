package state

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

type ProjectEnvironmentClonePublicationStore interface {
	ProjectEnvironmentCloneTargetOperation(context.Context, string, string, string) (ProjectEnvironmentCloneOperation, error)
	PublishProjectEnvironmentCloneReleaseSet(context.Context, string, string, string, int64, int) (ProjectReleaseSet, error)
}

type projectCloneWorkloadProof struct {
	view                            ProjectEnvironmentCloneWorkload
	live                            bool
	headHash, pinHash, artifactHash string
}

func validateCloneWorkloadProofs(resources []ProjectEnvironmentCloneResource, proofs []projectCloneWorkloadProof) error {
	workloads := map[string]ProjectEnvironmentCloneResource{}
	for _, r := range resources {
		if r.Kind != "workload" {
			continue
		}
		if _, duplicate := workloads[r.Name]; duplicate {
			return ErrConflict
		}
		workloads[r.Name] = r
	}
	if len(workloads) != len(proofs) {
		return ErrConflict
	}
	for _, p := range proofs {
		v := p.view
		r, ok := workloads[v.WorkloadSlug]
		if !ok || r.SourceID != v.SourceDeploymentID || r.SourceVersion != v.SourceHash || r.TargetID != v.TargetDeploymentID ||
			v.TargetDeploymentID == "" || !p.live {
			return ErrConflict
		}
		if p.artifactHash != v.SourceHash {
			return fmt.Errorf("clone workload %q artifact differs from its capture: %w", v.WorkloadSlug, ErrConflict)
		}
		if p.headHash != v.TargetSettingsHash || p.pinHash != v.TargetSettingsHash {
			return fmt.Errorf("clone workload %q settings changed during preparation: %w", v.WorkloadSlug, ErrConflict)
		}
	}
	return nil
}

func cloneWorkloadTargetArtifactHash(record projectCloneWorkloadRecord, deployment Deployment, layers []DeploymentSidecarLayer, signals map[string]string, artifacts ...projectCloneArtifact) (string, error) {
	snapshot := record.snapshot
	snapshot.Artifact = projectCloneArtifactFromDeployment(deployment)
	if len(artifacts) > 0 {
		snapshot.Artifact = artifacts[0]
	}
	snapshot.Artifact.ID, snapshot.Artifact.Scope = record.SourceDeploymentID, record.SourceScope
	snapshot.Layers, snapshot.SidecarSignals = layers, signals
	for i := range snapshot.Layers {
		snapshot.Layers[i].DeploymentID = record.SourceDeploymentID
	}
	raw, hash, err := encodeCloneWorkloadSnapshot(snapshot)
	if err == nil && hash != record.SourceHash {
		original, _, encodeErr := encodeCloneWorkloadSnapshot(record.snapshot)
		if encodeErr != nil {
			return "", encodeErr
		}
		var before, after map[string]json.RawMessage
		_ = json.Unmarshal(original, &before)
		_ = json.Unmarshal(raw, &after)
		var fields []string
		for key, value := range before {
			if bytes.Equal(value, after[key]) {
				continue
			}
			if key != "artifact" {
				fields = append(fields, key)
				continue
			}
			var left, right map[string]json.RawMessage
			_ = json.Unmarshal(value, &left)
			_ = json.Unmarshal(after[key], &right)
			for field, v := range left {
				if !bytes.Equal(v, right[field]) {
					fields = append(fields, "artifact."+field)
				}
			}
		}
		sort.Strings(fields)
		// Names only: the snapshot includes sealed configuration and customer
		// variables, whose values must never appear in diagnostics.
		return "", fmt.Errorf("clone workload %q changed captured fields (%s): %w", record.WorkloadSlug, strings.Join(fields, ", "), ErrConflict)
	}
	return hash, err
}
