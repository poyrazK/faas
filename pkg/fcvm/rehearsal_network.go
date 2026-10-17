// adr: 1349
package fcvm

import (
	"context"
	"errors"
	"fmt"

	"github.com/onebox-faas/faas/pkg/rehearsalcommand"
	"github.com/onebox-faas/faas/pkg/rehearsalrecorder"
)

type RehearsalNetworkPolicyStore interface {
	ScenarioRehearsalNetworkRun(context.Context, string) (string, error)
}

type RehearsalCommandPolicyStore interface {
	ScenarioRehearsalCommand(context.Context, rehearsalrecorder.Identity) (rehearsalcommand.Spec, error)
}

var ErrRehearsalExternalActionDenied = errors.New("rehearsal external action adapter is unavailable")

// InstanceExternalActionIdentity is the identity boundary for host-mediated
// actions. IP firewall policy cannot constrain a vsock relay, event publisher
// or federated credential. A contained instance gets none of those capabilities.
// Read-only metadata and lifecycle telemetry retain their ordinary identity path.
func (m *Manager) InstanceExternalActionIdentity(ctx context.Context, instance string) (string, string, error) {
	m.mu.Lock()
	inst := m.live[instance]
	if inst == nil {
		m.mu.Unlock()
		return "", "", errors.New("external action instance is not live")
	}
	appID, accountID, contained := inst.AppID, inst.AccountID, inst.Net.RehearsalRunID != ""
	m.mu.Unlock()
	if contained {
		return "", "", ErrRehearsalExternalActionDenied
	}
	run, err := m.rehearsalNetworkRun(ctx, appID)
	if err != nil {
		return "", "", err
	}
	if run != "" {
		return "", "", ErrRehearsalExternalActionDenied
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.live[instance] != inst {
		return "", "", errors.New("external action instance changed during policy read")
	}
	return appID, accountID, nil
}

// WithRehearsalNetworkPolicies is daemon wiring, installed before any admission.
// Every application wake consults durable policy, including legacy RPC callers.
func (m *Manager) WithRehearsalNetworkPolicies(store RehearsalNetworkPolicyStore) *Manager {
	m.rehearsalNetworkPolicies = store
	return m
}

func (m *Manager) RehearsalNetworkPolicyReady() bool {
	if m == nil || m.rehearsalNetworkPolicies == nil {
		return false
	}
	if m.nativeVMM() != nil {
		return true
	} // owned native runner always implements InputRunner
	_, atomic := m.run.(InputRunner)
	return atomic
}

func (m *Manager) rehearsalNetworkRun(ctx context.Context, appID string) (string, error) {
	if m.rehearsalNetworkPolicies == nil {
		return "", nil
	}
	if appID == "" { // builders have no application identity
		return "", nil
	}
	return m.rehearsalNetworkPolicies.ScenarioRehearsalNetworkRun(ctx, appID)
}

func (m *Manager) applyRehearsalNetwork(ctx context.Context, req *WakeRequest) error {
	if m.rehearsalNetworkPolicies != nil && req.AppID == "" && req.ExportDir == "" && !req.ExecutionOnly {
		return errors.New("rehearsal network admission requires an application identity")
	}
	runID, err := m.rehearsalNetworkRun(ctx, req.AppID)
	if err != nil {
		return fmt.Errorf("read rehearsal network policy: %w", err)
	}
	if req.RehearsalRunID != "" && (m.rehearsalNetworkPolicies == nil || runID != req.RehearsalRunID) {
		return errors.New("rehearsal network policy is unavailable or differs from the requested run")
	}
	if runID != "" {
		// Non-serving execution paths and native migrations need separate
		// containment qualification. Refuse them before any guest can run.
		if req.ExecutionOnly || req.ExportDir != "" || req.admission != nil {
			return errors.New("rehearsal network containment does not support this execution mode")
		}
		if req.AppTaskOnly {
			store, ok := m.rehearsalNetworkPolicies.(RehearsalCommandPolicyStore)
			_, canRetire := m.rehearsalNetworkPolicies.(rehearsalrecorder.CommandStore)
			if !ok || !canRetire || req.RehearsalRunID != runID {
				return errors.New("contained commands require an explicit supported rehearsal request")
			}
			identity := rehearsalrecorder.Identity{AccountID: req.AccountID, AppID: req.AppID, RunID: runID, InstanceID: req.Instance, DeploymentID: req.DeploymentID}
			if _, err := store.ScenarioRehearsalCommand(ctx, identity); err != nil {
				return fmt.Errorf("contained command identity: %w", err)
			}
		}
		if !m.RehearsalNetworkPolicyReady() {
			return errors.New("rehearsal network containment requires atomic firewall transactions")
		}
		req.RehearsalRunID = runID
	}
	return nil
}

// Recheck the immutable binding and atomically restore its guard before a
// resident VM resumes. Native daemon recovery quarantines, rather than adopts,
// surviving processes; a subsequent wake creates a fresh guarded namespace.
func (m *Manager) ensureRehearsalNetwork(ctx context.Context, inst *Instance) error {
	m.mu.Lock()
	nc, appID, generation := inst.Net, inst.AppID, inst.nativeGeneration
	m.mu.Unlock()
	runID, err := m.rehearsalNetworkRun(ctx, appID)
	if err != nil {
		return err
	}
	if runID != nc.RehearsalRunID {
		return errors.New("resident rehearsal network policy differs from durable enrollment")
	}
	if runID != "" {
		if !m.RehearsalNetworkPolicyReady() {
			return errors.New("rehearsal network containment requires atomic firewall transactions")
		}
		scope, err := m.nativeInstanceNetworkContext(ctx, inst.Lease.Instance, generation)
		if err != nil {
			return err
		}
		return m.runNftCommands(scope, nc.Netns, nc.RehearsalNftCommands())
	}
	return nil
}

func (m *Manager) resumeWithRehearsalNetwork(ctx context.Context, inst *Instance) error {
	if err := m.ensureRehearsalNetwork(ctx, inst); err != nil {
		return err
	}
	return m.vmm.ResumeVM(ctx, inst.Lease)
}

func (m *Manager) rejectRehearsalExecution(ctx context.Context, appID string) error {
	run, err := m.rehearsalNetworkRun(ctx, appID)
	if err != nil {
		return err
	}
	if run != "" {
		return errors.New("rehearsal containment does not support qualification execution")
	}
	return nil
}
