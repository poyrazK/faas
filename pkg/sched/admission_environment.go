package sched

import (
	"context"
	"errors"

	"github.com/onebox-faas/faas/pkg/state"
)

// runtimeEnvironmentAdmissionKey retains the original owner, independently of
// the deployment generation. Unpinned production aliases share legacy entries.
func runtimeEnvironmentAdmissionKey(scope, environmentID string) string {
	if environmentID != "" {
		return "environment:" + environmentID
	}
	if reaperProductionScope(scope) {
		return ""
	}
	return "scope:" + scope
}

// ConcurrencyForEnvironment counts only serving reservations in the original
// lifetime. Resident paused/primes/tasks still consume node capacity separately.
func (l *NodeLedger) ConcurrencyForEnvironment(appID, environmentKey string) int {
	if l == nil || appID == "" {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.ensureEnvironmentConcurrencyLocked()
	return l.perAppEnvironment[appID+"\x00"+environmentKey]
}

// Legacy callers may construct a ledger literal. Initialize the new index
// from existing reservations before the first count read or mutation.
func (l *NodeLedger) ensureEnvironmentConcurrencyLocked() {
	if l.perAppEnvironment != nil {
		return
	}
	l.perAppEnvironment = make(map[string]int)
	for _, entry := range l.entries {
		if entry.countsConc {
			l.perAppEnvironment[entry.appID+"\x00"+entry.environmentKey]++
		}
	}
}

func (l *NodeLedger) releaseEnvironmentConcurrencyLocked(entry *reservation) {
	key := entry.appID + "\x00" + entry.environmentKey
	l.perAppEnvironment[key]--
	if l.perAppEnvironment[key] <= 0 {
		delete(l.perAppEnvironment, key)
	}
}

type ledgerDeploymentPolicy struct {
	app            state.App
	environmentKey string
}

// Recovery accounts for already-resident orphan VMs, without adopting a new
// environment with the same slug. A transient ownership read aborts startup.
func (e *Engine) seedLedgerDeploymentPolicy(ctx context.Context, app state.App, deploymentID string) (ledgerDeploymentPolicy, error) {
	policy := ledgerDeploymentPolicy{app: app}
	if deploymentID == "" {
		return policy, nil
	}
	policy.environmentKey = "deployment:" + deploymentID
	deployment, err := e.store.DeploymentByID(ctx, deploymentID)
	if errors.Is(err, state.ErrNotFound) {
		return policy, nil
	}
	if err != nil {
		return policy, err
	}
	policy.app, err = state.ResolveAppForDeployment(ctx, e.store, app, deployment)
	if err != nil {
		return policy, err
	}
	owner, err := e.runtimeScalingStateForDeployment(ctx, policy.app, deployment)
	if errors.Is(err, state.ErrNotFound) {
		return policy, nil
	}
	if err != nil {
		return policy, err
	}
	policy.environmentKey = runtimeEnvironmentAdmissionKey(owner.Scope, owner.EnvironmentID)
	return policy, nil
}
