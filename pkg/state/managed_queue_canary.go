// adr:1545 — managed worker canaries use active reviewed graph authority.
package state

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/onebox-faas/faas/pkg/api"
)

// Eligibility observes the same snapshot as publication without creating work.
type QueueBindingCanaryEligibilityStore interface {
	QueueBindingCanaryEligibility(context.Context, string, string, string, string) (bool, string, error)
}

func queueCanaryEligibility(s queueCanarySnapshot, err error) (bool, string, error) {
	if errors.Is(err, ErrNotFound) || errors.Is(err, ErrConflict) {
		return false, "queue_canary_context_unavailable", nil
	}
	if err != nil {
		return false, "", err
	}
	if !queueCanarySupported(s) {
		return false, "queue_handler_unsupported", nil
	}
	return true, "", nil
}

// A reviewed variable takes precedence over inherited deployment defaults in
// the worker's actual runtime. Consent is never inferred from mutable app vars.
func managedQueueCanarySnapshot(s *queueCanarySnapshot, dep Deployment) error {
	frozen, err := dep.ScopedWorkloadRuntime()
	if err != nil || frozen == nil || frozen.WorkloadClass != WorkloadClassWorker || dep.EnvironmentWorkloadHeld() {
		return ErrNotFound
	}
	manifest, err := EffectiveEnvironmentWorkloadManifest(*frozen)
	if err != nil || manifest.ExecutionMode != api.ExecutionModeWorker || s.ConsumerClass != string(WorkloadClassWorker) || s.BindingScope != s.Scope || s.EnvironmentID != frozen.EnvironmentID {
		return ErrNotFound
	}
	binding, ok := frozen.QueueBindings[s.Name]
	current := gitOpsQueueContract(QueueBinding{QueueName: s.QueueName, Mode: "push", WorkloadClass: WorkloadClassWorker, Enabled: true, MaxConcurrency: s.Concurrency, RetryPolicyJSON: s.Retry})
	want, _ := canonicalGitOpsValue(mustGitOpsJSON(binding.Contract))
	got, _ := canonicalGitOpsValue(mustGitOpsJSON(current))
	if !ok || binding.BindingID != s.BindingID || binding.TriggerID != s.TriggerID || string(want) != string(got) {
		return ErrNotFound
	}
	// Snapshot only the effective, non-secret capability declaration. All other
	// reviewed runtime inputs remain covered by ManagedRuntime.
	var env map[string]string
	if json.Unmarshal(dep.OverrideEnv, &env) != nil || env == nil {
		env = map[string]string{}
	}
	if value, exists := frozen.Variables[api.QueueCanaryCapabilityEnv]; exists {
		env[api.QueueCanaryCapabilityEnv] = value
	}
	if _, secret := frozen.SecretRefs[api.QueueCanaryCapabilityEnv]; secret {
		return ErrNotFound
	}
	s.OverrideEnv, _ = json.Marshal(env)
	// HTTP revision-pin settings have no authority over managed worker work.
	s.RevisionTTLSeconds = 0
	return nil
}

func (s *PgStore) QueueBindingCanaryEligibility(ctx context.Context, account, app, deployment, binding string) (bool, string, error) {
	snapshot, err := queueCanarySnapshotDB(ctx, s.pool, account, app, deployment, binding)
	return queueCanaryEligibility(snapshot, err)
}

func (m *MemStore) QueueBindingCanaryEligibility(_ context.Context, account, app, deployment, binding string) (bool, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	snapshot, err := m.queueCanarySnapshotLocked(account, app, deployment, binding)
	return queueCanaryEligibility(snapshot, err)
}
