package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// DeploymentDependencyPin is captured by admission. It never follows a later
// deployment, an environment fallback, or a changed Compose declaration.
type DeploymentDependencyPin struct {
	Service      string `json:"service"`
	AppID        string `json:"app_id"`
	DeploymentID string `json:"deployment_id"`
}

// DeploymentDependencyGate is also projected under stage_state.dependency_gate.
// Its deadline starts when the candidate first presents its own readiness proof.
type DeploymentDependencyGate struct {
	Dependencies []DeploymentDependencyPin `json:"dependencies"`
	StartedAt    *time.Time                `json:"started_at,omitempty"`
	DeadlineAt   *time.Time                `json:"deadline_at,omitempty"`
	Status       string                    `json:"status"`
	Blocker      string                    `json:"blocker,omitempty"`
}

type DeploymentDependencyGateStore interface {
	CheckDeploymentDependencies(context.Context, string, time.Time) (*DeploymentDependencyGate, error)
}

// DependencyGateError exposes only source service names and public deployment
// IDs. Pending work is deferred by imaged; terminal blockers fail the candidate.
type DependencyGateError struct {
	Code   string
	Detail string
}

func (e *DependencyGateError) Error() string { return e.Detail }
func (e *DependencyGateError) Pending() bool { return e.Code == CodeDependencyNotReady }

const (
	CodeDependencyNotReady = "dependency_not_ready"
	CodeDependencyFailed   = "dependency_failed"
	CodeDependencyTimeout  = "dependency_readiness_timeout"
)

func healthyProjectDependencies(manifest AppManifest) ([]string, error) {
	declared := make([]string, 0, len(manifest.ServiceBindings))
	for _, binding := range manifest.ServiceBindings {
		declared = append(declared, binding.Service)
	}
	for name, condition := range manifest.ProjectDependencyConditions {
		if condition == api.ComposeDependencyStarted {
			// Managed external names may retain admission-order declarations
			// without becoming a private project service binding.
			declared = append(declared, name)
		}
	}
	if err := api.ValidateComposeDependencyConditions(manifest.ProjectDependencyConditions, declared); err != nil {
		return nil, fmt.Errorf("state: invalid project dependency conditions: %w", err)
	}
	var names []string
	for name, condition := range manifest.ProjectDependencyConditions {
		if condition == api.ComposeDependencyHealthy {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names, nil
}

func evaluateDependencyGate(gate DeploymentDependencyGate, now time.Time, lookup func(DeploymentDependencyPin) (Deployment, error)) (DeploymentDependencyGate, error) {
	if gate.StartedAt == nil {
		started := now.UTC().Truncate(time.Microsecond)
		deadline := started.Add(api.ProjectDependencyGateTimeout)
		gate.StartedAt, gate.DeadlineAt = &started, &deadline
	}
	if gate.Status == "failed" {
		return gate, &DependencyGateError{Code: CodeDependencyFailed, Detail: gate.Blocker}
	}
	var pending string
	for _, pin := range gate.Dependencies {
		dependency, err := lookup(pin)
		if errors.Is(err, ErrNotFound) || err == nil && (dependency.Status.IsTerminal() || dependency.ParkedReason != "") {
			gate.Status, gate.Blocker = "failed", fmt.Sprintf("dependency %q deployment %s is unavailable; redeploy after fixing the dependency", pin.Service, pin.DeploymentID)
			return gate, &DependencyGateError{Code: CodeDependencyFailed, Detail: gate.Blocker}
		}
		if err != nil {
			return gate, fmt.Errorf("state: read dependency readiness: %w", err)
		}
		if dependency.Status != DeployLive || dependency.TrafficPercent <= 0 {
			if pending == "" {
				pending = fmt.Sprintf("waiting for dependency %q deployment %s to become live with traffic", pin.Service, pin.DeploymentID)
			}
		}
	}
	if (gate.Status != "ready" || pending != "") && !now.Before(*gate.DeadlineAt) {
		gate.Status, gate.Blocker = "failed", "dependency readiness deadline expired"
		return gate, &DependencyGateError{Code: CodeDependencyTimeout, Detail: gate.Blocker}
	}
	if pending != "" {
		gate.Status, gate.Blocker = "waiting", pending
		return gate, &DependencyGateError{Code: CodeDependencyNotReady, Detail: pending}
	}
	gate.Status, gate.Blocker = "ready", ""
	return gate, nil
}

func dependencyAdmissionStatusAllowed(status DeploymentStatus) bool {
	switch status {
	case DeployPending, DeployBuilding, DeployImaging, DeploySnapshotting:
		return true
	default:
		// Fresh gated admissions must begin in a preparation state.
		return false
	}
}

func (m *MemStore) captureDeploymentDependenciesLocked(app App, candidate Deployment) (*DeploymentDependencyGate, error) {
	names, err := healthyProjectDependencies(app.Manifest)
	if err != nil || len(names) == 0 {
		return nil, err
	}
	if app.ProjectID == "" || candidate.EnvironmentWorkloadHeld() {
		return nil, fmt.Errorf("state: healthy dependency gates require an ordinary project deployment: %w", ErrInvalidArgument)
	}
	if candidate.Status != "" && !dependencyAdmissionStatusAllowed(candidate.Status) {
		return nil, ErrInvalidArgument
	}
	gate := &DeploymentDependencyGate{Status: "waiting"}
	for _, name := range names {
		var target App
		for _, other := range m.apps {
			if other.AccountID == app.AccountID && other.ProjectID == app.ProjectID &&
				strings.EqualFold(other.WorkloadName, name) && other.Status != AppDeleted &&
				other.PreviewPrNumber == app.PreviewPrNumber && (other.PreviewOfSlug == "") == (app.PreviewOfSlug == "") {
				if target.ID != "" || other.ID == app.ID {
					return nil, fmt.Errorf("state: ambiguous or self dependency %q: %w", name, ErrInvalidArgument)
				}
				target = other
			}
		}
		var latest Deployment
		for _, other := range m.deployments {
			if other.AppID == target.ID && normalizedDeploymentScope(other.Scope) == normalizedDeploymentScope(candidate.Scope) &&
				!other.EnvironmentWorkloadHeld() && other.Revision > latest.Revision {
				latest = other
			}
		}
		if latest.ID == "" {
			return nil, fmt.Errorf("state: dependency %q has no accepted deployment in this environment: %w", name, ErrInvalidArgument)
		}
		gate.Dependencies = append(gate.Dependencies, DeploymentDependencyPin{Service: name, AppID: target.ID, DeploymentID: latest.ID})
	}
	return gate, nil
}

func (m *MemStore) CheckDeploymentDependencies(_ context.Context, id string, now time.Time) (*DeploymentDependencyGate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.checkDeploymentDependenciesLocked(id, now, true)
}

func (m *MemStore) checkDeploymentDependenciesLocked(id string, now time.Time, persist bool) (*DeploymentDependencyGate, error) {
	candidate, ok := m.deployments[id]
	if !ok {
		return nil, ErrNotFound
	}
	gate, ok := m.deploymentDependencyGates[id]
	if !ok || candidate.Status == DeployLive || m.deploymentServedLocked(id) {
		return nil, nil
	}
	owner := m.apps[candidate.AppID]
	updated, err := evaluateDependencyGate(gate, now, func(pin DeploymentDependencyPin) (Deployment, error) {
		dependency, ok := m.deployments[pin.DeploymentID]
		target := m.apps[pin.AppID]
		if !ok || dependency.AppID != pin.AppID || target.Status == AppDeleted || target.Manifest.ExecutionMode == api.ExecutionModeJob || target.WorkloadClass == WorkloadClassJob || target.AccountID != owner.AccountID || target.ProjectID != owner.ProjectID ||
			target.PreviewPrNumber != owner.PreviewPrNumber || (target.PreviewOfSlug == "") != (owner.PreviewOfSlug == "") ||
			normalizedDeploymentScope(dependency.Scope) != normalizedDeploymentScope(candidate.Scope) || dependency.EnvironmentWorkloadHeld() {
			return Deployment{}, ErrNotFound
		}
		return dependency, nil
	})
	if persist {
		var stages StageState
		if decodeErr := json.Unmarshal(candidate.StageState, &stages); decodeErr != nil {
			return nil, decodeErr
		}
		stages.DependencyGate = &updated
		encoded, encodeErr := json.Marshal(stages)
		if encodeErr != nil {
			return nil, encodeErr
		}
		candidate.StageState = encoded
		m.deploymentDependencyGates[id] = updated
		m.putDeploymentLocked(id, candidate)
	}
	result := copyDeploymentDependencyGate(updated)
	return &result, err
}

func copyDeploymentDependencyGate(gate DeploymentDependencyGate) DeploymentDependencyGate {
	gate.Dependencies = append([]DeploymentDependencyPin(nil), gate.Dependencies...)
	if gate.StartedAt != nil {
		started, deadline := *gate.StartedAt, *gate.DeadlineAt
		gate.StartedAt, gate.DeadlineAt = &started, &deadline
	}
	return gate
}

func cloneDeploymentDependencyGate(gate DeploymentDependencyGate) DeploymentDependencyGate {
	gate = copyDeploymentDependencyGate(gate)
	gate.StartedAt, gate.DeadlineAt = nil, nil
	gate.Status, gate.Blocker = "waiting", ""
	return gate
}
