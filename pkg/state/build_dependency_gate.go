package state

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// BuildDependencyGate freezes a migration's selected deployments. It is checked
// before cache lookup and slot allocation, independently of activation gates.
type BuildDependencyGate struct {
	Dependencies []DeploymentDependencyPin `json:"dependencies"`
	Status       string                    `json:"status"`
	Code         string                    `json:"code,omitempty"`
	Blocker      string                    `json:"blocker,omitempty"`
	DeadlineAt   time.Time                 `json:"deadline_at"`
	CheckAfter   time.Time                 `json:"check_after"`
}

type BuildDependencyGateStore interface {
	// CheckBuildDependencies also defers a pending, matching running claim back to
	// the queue in the same transaction. It never sleeps or occupies a VM slot.
	CheckBuildDependencies(context.Context, Build, time.Time) (*BuildDependencyGate, error)
}

func newBuildDependencyGate(candidate Deployment, pins []DeploymentDependencyPin, now time.Time) (*BuildDependencyGate, error) {
	if len(pins) == 0 {
		return nil, nil
	}
	if len(pins) > api.ProjectMigrationMaxServices || candidate.EnvironmentWorkloadHeld() || normalizedDeploymentScope(candidate.Scope) != api.DefaultEnvScope ||
		(candidate.Kind != DeploymentKindTarball && candidate.Kind != DeploymentKindDockerfile && candidate.Kind != DeploymentKindGitHub) {
		return nil, ErrInvalidArgument
	}
	seen := map[string]bool{}
	for _, p := range pins {
		if p.Service == "" || !validStandardResourceRead(p.AppID, p.DeploymentID) || p.AppID == candidate.AppID || p.DeploymentID == candidate.ID || seen[p.Service] {
			return nil, ErrInvalidArgument
		}
		seen[p.Service] = true
	}
	pins = slices.Clone(pins)
	slices.SortFunc(pins, func(a, b DeploymentDependencyPin) int {
		if a.Service < b.Service {
			return -1
		}
		if a.Service > b.Service {
			return 1
		}
		return 0
	})
	gate := &BuildDependencyGate{Dependencies: pins, Status: "waiting", Code: "build_dependency_not_ready", Blocker: "Waiting for the selected API deployments before building.", DeadlineAt: now.UTC().Add(api.ProjectMigrationBuildWaitTimeout)}
	raw, err := json.Marshal(gate)
	if err != nil || len(raw) > api.ProjectMigrationBuildGateMaxBytes {
		return nil, ErrInvalidArgument
	}
	return gate, nil
}

// BuildDependencyDigest partitions artifacts by the exact upstream deployment
// set. Empty gates preserve legacy cache identities.
func BuildDependencyDigest(gate *BuildDependencyGate) string {
	if gate == nil || len(gate.Dependencies) == 0 {
		return ""
	}
	raw, _ := json.Marshal(gate.Dependencies)
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func evaluateBuildDependencies(gate BuildDependencyGate, now time.Time, lookup func(DeploymentDependencyPin) (Deployment, bool, error)) (BuildDependencyGate, error) {
	if gate.Status == "failed" {
		return gate, nil
	}
	if gate.Status == "waiting" && !now.Before(gate.DeadlineAt) {
		gate.Status, gate.Code, gate.Blocker = "failed", "build_dependency_timeout", "The API wait expired. Check the connected APIs, then retry the affected migration services."
		gate.CheckAfter = time.Time{}
		return gate, nil
	}
	gate.Status, gate.Code, gate.Blocker = "ready", "", ""
	for _, pin := range gate.Dependencies {
		dep, latest, err := lookup(pin)
		if err != nil {
			return gate, err
		}
		if dep.ID == "" {
			gate.Status, gate.Code, gate.Blocker = "failed", "build_dependency_failed", fmt.Sprintf("API %q is unavailable. Fix it, then retry the affected migration services.", pin.Service)
			return gate, nil
		}
		if !latest {
			gate.Status, gate.Code, gate.Blocker = "failed", "build_dependency_changed", fmt.Sprintf("API %q changed outside this migration. Review its deployment before starting a new migration.", pin.Service)
			return gate, nil
		}
		if dep.Status.IsTerminal() || dep.ParkedReason != "" {
			gate.Status, gate.Code, gate.Blocker = "failed", "build_dependency_failed", fmt.Sprintf("API %q is unavailable. Fix it, then retry the affected migration services.", pin.Service)
			return gate, nil
		}
		if dep.Status != DeployLive || dep.TrafficPercent != 100 {
			gate.Status, gate.Code, gate.Blocker = "waiting", "build_dependency_not_ready", fmt.Sprintf("Waiting for API %q to be live with all traffic before building.", pin.Service)
		}
	}
	if gate.Status == "waiting" {
		if !now.Before(gate.DeadlineAt) {
			gate.Status, gate.Code, gate.Blocker = "failed", "build_dependency_timeout", "The API wait expired. Check the connected APIs, then retry the affected migration services."
		} else {
			gate.CheckAfter = now.UTC().Add(api.ProjectMigrationBuildWaitPoll)
		}
	} else {
		gate.CheckAfter = time.Time{}
	}
	return gate, nil
}

func buildGateFromStages(raw json.RawMessage) (*BuildDependencyGate, error) {
	var stages StageState
	if len(raw) == 0 {
		return nil, nil
	}
	err := json.Unmarshal(raw, &stages)
	return stages.BuildDependencyGate, err
}

func projectBuildGate(raw json.RawMessage, gate *BuildDependencyGate) (json.RawMessage, error) {
	// Preserve all stage keys, including projections from newer workers.
	stages := map[string]json.RawMessage{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &stages); err != nil {
			return nil, err
		}
	}
	encoded, err := json.Marshal(gate)
	if err != nil {
		return nil, err
	}
	if stages == nil {
		stages = map[string]json.RawMessage{}
	}
	stages["build_dependency_gate"] = encoded
	return json.Marshal(stages)
}

func (m *MemStore) buildDependencyEligibleLocked(build Build, now time.Time) bool {
	gate, err := buildGateFromStages(m.deployments[build.DeploymentID].StageState)
	return err == nil && (gate == nil || !gate.CheckAfter.After(now))
}

func (m *MemStore) CheckBuildDependencies(_ context.Context, claim Build, now time.Time) (*BuildDependencyGate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	build, ok := m.builds[claim.ID]
	if !ok || build.Status != BuildRunning || build.DeploymentID != claim.DeploymentID || !build.StartedAt.Equal(claim.StartedAt) {
		return nil, ErrConflict
	}
	candidate := m.deployments[claim.DeploymentID]
	gate, err := buildGateFromStages(candidate.StageState)
	if err != nil || gate == nil {
		return gate, err
	}
	if candidate.Status.IsTerminal() {
		return nil, ErrConflict
	}
	owner := m.apps[candidate.AppID]
	updated, err := evaluateBuildDependencies(*gate, now, func(pin DeploymentDependencyPin) (Deployment, bool, error) {
		target, ok := m.apps[pin.AppID]
		dep, found := m.deployments[pin.DeploymentID]
		if !ok || !found || !buildDependencyTargetAllowed(owner, target, candidate, dep, pin) {
			return Deployment{}, false, nil
		}
		latest := true
		for _, other := range m.deployments {
			if other.AppID == pin.AppID && normalizedDeploymentScope(other.Scope) == normalizedDeploymentScope(candidate.Scope) && other.Revision > dep.Revision {
				latest = false
			}
		}
		return dep, latest, nil
	})
	if err != nil {
		return nil, err
	}
	candidate.StageState, err = projectBuildGate(candidate.StageState, &updated)
	if err != nil {
		return nil, err
	}
	m.putDeploymentLocked(candidate.ID, candidate)
	if updated.Status == "waiting" {
		build.Status = BuildQueued
		build.StartedAt = time.Time{}
		m.builds[build.ID] = build
	}
	return &updated, nil
}

func buildDependencyTargetAllowed(owner, target App, candidate, dep Deployment, pin DeploymentDependencyPin) bool {
	return owner.ProjectID != "" && target.AccountID == owner.AccountID && target.ProjectID == owner.ProjectID && target.Status != AppDeleted && target.WorkloadName == pin.Service && target.ID != owner.ID && target.PreviewOfSlug == "" && owner.PreviewOfSlug == "" && target.WorkloadClass != WorkloadClassJob && target.Manifest.ExecutionMode != api.ExecutionModeJob && dep.AppID == target.ID && normalizedDeploymentScope(dep.Scope) == normalizedDeploymentScope(candidate.Scope) && !dep.EnvironmentWorkloadHeld()
}
