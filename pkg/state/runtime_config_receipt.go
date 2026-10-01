package state

import (
	"context"
	"encoding/json"
	"maps"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// RuntimeConfigInputs contains the non-secret values actually sent to vmmd
// and the versions of sealed values, never secret plaintext or ciphertext.
// Boundary is observed before reading the boot inputs, not inferred from the
// instance's admission/readiness time. Restores inherit the captured inputs.
type RuntimeConfigInputs struct {
	Scope          string            `json:"scope"`
	Boundary       time.Time         `json:"boundary"`
	Variables      map[string]string `json:"variables"`
	SecretVersions map[string]int64  `json:"secret_versions"`
	AllSecrets     bool              `json:"all_secrets"`
}

type RuntimeConfigReceiptStore interface {
	RecordInstanceRuntimeConfigReceipt(context.Context, string, string, RuntimeConfigInputs) error
	InstanceRuntimeConfigReceipt(context.Context, string) (RuntimeConfigInputs, bool, error)
	SnapshotRuntimeConfigReceipt(context.Context, string) (RuntimeConfigInputs, bool, error)
	RuntimeConfigInputsFresh(context.Context, string, RuntimeConfigInputs) (bool, error)
	RuntimeConfigReceiptRequired(context.Context, string, string) (bool, error)
}

// RuntimeConfigReceiptPublisher makes readiness and its input evidence visible
// in one transaction, so a concurrent refresh cannot see a ready VM without
// its acknowledgement and retire a candidate that has just booted.
type RuntimeConfigReceiptPublisher interface {
	PublishInstanceRuntimeWithConfig(context.Context, string, string, string, string, int, string, RuntimeConfigInputs) (Instance, error)
}

func cloneRuntimeConfigInputs(inputs RuntimeConfigInputs) RuntimeConfigInputs {
	inputs.Variables, inputs.SecretVersions = maps.Clone(inputs.Variables), maps.Clone(inputs.SecretVersions)
	return inputs
}

func validateRuntimeConfigInputs(inputs RuntimeConfigInputs) error {
	if api.ValidateScope(inputs.Scope) != nil || inputs.Boundary.Before(time.Unix(0, 0)) {
		return ErrInvalidArgument
	}
	for key := range inputs.Variables {
		if api.ValidateEnvKey(key) != nil {
			return ErrInvalidArgument
		}
	}
	for ref, version := range inputs.SecretVersions {
		scope, key, ok := strings.Cut(ref, "/")
		if !ok || api.ValidateScope(scope) != nil || api.ValidateEnvKey(key) != nil || version < 1 {
			return ErrInvalidArgument
		}
	}
	for _, value := range []any{inputs.Variables, inputs.SecretVersions} {
		raw, err := json.Marshal(value)
		if err != nil || len(raw) > api.EnvironmentGitOpsMaxDefinitionBytes {
			return ErrInvalidArgument
		}
	}
	return nil
}

type instanceRuntimeConfigReceipt struct {
	WakeID string
	Inputs RuntimeConfigInputs
}

func (m *MemStore) runtimeConfigReceiptRequiredLocked(appID, scope string) bool {
	for _, memory := range m.environmentGitOps {
		if memory.source.EnvironmentSlug != normalizedDeploymentScope(scope) {
			continue
		}
		for resource, id := range memory.resources {
			if id != appID {
				continue
			}
			for _, owner := range memory.owners {
				if owner.Resource == resource && owner.Manager == memory.source.ID && strings.HasPrefix(owner.Path, "variables/") {
					return true
				}
			}
		}
		for _, effect := range memory.runtime {
			if effect.AppID == appID && effect.CompletedAt == nil {
				return true
			}
		}
	}
	return false
}

func (m *MemStore) RuntimeConfigReceiptRequired(_ context.Context, appID, scope string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.runtimeConfigReceiptRequiredLocked(appID, scope), nil
}

func (m *MemStore) runtimeConfigInputsFreshLocked(appID string, inputs RuntimeConfigInputs) bool {
	boundary, _ := m.environmentRuntimeChangedAtLocked(appID, inputs.Scope)
	if boundary.After(inputs.Boundary) {
		return false
	}
	variables := map[string]string{}
	for _, row := range m.envs {
		if row.AppID == appID && row.Scope == inputs.Scope {
			variables[row.Key] = row.Value
		}
	}
	if !maps.Equal(inputs.Variables, variables) {
		return false
	}
	current := map[string]int64{}
	for _, row := range m.secrets {
		if row.AppID == appID && row.Scope == inputs.Scope {
			current[row.Scope+"/"+row.Key] = row.DeliveryVersion
		}
	}
	if inputs.AllSecrets && !maps.Equal(inputs.SecretVersions, current) {
		return false
	}
	for ref, version := range inputs.SecretVersions {
		if current[ref] != version {
			return false
		}
	}
	return true
}

func (m *MemStore) RuntimeConfigInputsFresh(_ context.Context, appID string, inputs RuntimeConfigInputs) (bool, error) {
	if err := validateRuntimeConfigInputs(inputs); err != nil {
		return false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.runtimeConfigInputsFreshLocked(appID, inputs), nil
}

func (m *MemStore) RecordInstanceRuntimeConfigReceipt(_ context.Context, instanceID, wakeID string, inputs RuntimeConfigInputs) error {
	if err := validateRuntimeConfigInputs(inputs); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.recordInstanceRuntimeConfigReceiptLocked(instanceID, wakeID, inputs)
}

func (m *MemStore) recordInstanceRuntimeConfigReceiptLocked(instanceID, wakeID string, inputs RuntimeConfigInputs) error {
	instance, exists := m.instances[instanceID]
	deployment := m.deployments[instance.DeploymentID]
	if !exists || instance.WakeID != wakeID || instance.State != string(StateRunning) || normalizedDeploymentScope(deployment.Scope) != inputs.Scope {
		return ErrConflict
	}
	if m.instanceRuntimeConfigReceipts == nil {
		m.instanceRuntimeConfigReceipts = map[string]instanceRuntimeConfigReceipt{}
	}
	if prior, exists := m.instanceRuntimeConfigReceipts[instanceID]; exists && prior.WakeID == wakeID {
		if prior.Inputs.Scope != inputs.Scope || !prior.Inputs.Boundary.Equal(inputs.Boundary) || prior.Inputs.AllSecrets != inputs.AllSecrets ||
			!maps.Equal(prior.Inputs.Variables, inputs.Variables) || !maps.Equal(prior.Inputs.SecretVersions, inputs.SecretVersions) {
			return ErrConflict
		}
		return nil
	}
	m.instanceRuntimeConfigReceipts[instanceID] = instanceRuntimeConfigReceipt{WakeID: wakeID, Inputs: cloneRuntimeConfigInputs(inputs)}
	return nil
}

func (m *MemStore) PublishInstanceRuntimeWithConfig(_ context.Context, id, expectedState, netns, hostIP string, uid int, wakeID string, inputs RuntimeConfigInputs) (Instance, error) {
	if err := validateRuntimeConfigInputs(inputs); err != nil {
		return Instance{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	instance, exists := m.instances[id]
	deployment := m.deployments[instance.DeploymentID]
	if !exists || instance.State != expectedState || instance.WakeID != wakeID || normalizedDeploymentScope(deployment.Scope) != inputs.Scope {
		return Instance{}, ErrConflict
	}
	if err := validateWorkerInstanceMutation(instance, string(StateRunning), instance.Mode); err != nil {
		return Instance{}, err
	}
	prior := instance
	instance.State, instance.Netns, instance.HostIP, instance.GuestUID, instance.StartedAt = string(StateRunning), netns, hostIP, uid, time.Now().UTC()
	m.instances[id] = instance
	if err := m.recordInstanceRuntimeConfigReceiptLocked(id, wakeID, inputs); err != nil {
		m.instances[id] = prior
		return Instance{}, err
	}
	return instance, nil
}

func (m *MemStore) instanceRuntimeConfigInputsLocked(id string) (RuntimeConfigInputs, bool) {
	proof, exists := m.instanceRuntimeConfigReceipts[id]
	instance := m.instances[id]
	return proof.Inputs, exists && instance.WakeID == proof.WakeID
}

func (m *MemStore) InstanceRuntimeConfigReceipt(_ context.Context, id string) (RuntimeConfigInputs, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	inputs, exists := m.instanceRuntimeConfigInputsLocked(id)
	return cloneRuntimeConfigInputs(inputs), exists, nil
}

func (m *MemStore) SnapshotRuntimeConfigReceipt(_ context.Context, id string) (RuntimeConfigInputs, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	inputs, exists := m.snapshotRuntimeConfigReceipts[id]
	return cloneRuntimeConfigInputs(inputs), exists, nil
}
