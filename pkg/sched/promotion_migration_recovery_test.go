package sched

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/apptaskproto"

	"github.com/onebox-faas/faas/pkg/state"
)

type recoveryRuntimeTestStore struct {
	state.AppTaskStore
	jobs         []state.PromotionMigrationQuiescenceJob
	acknowledged []state.PromotionMigrationQuiescenceJob
}

func (s *recoveryRuntimeTestStore) RegisterPromotionMigrationRuntime(context.Context, state.AppTask, string) (string, error) {
	return "", nil
}
func (s *recoveryRuntimeTestStore) PendingPromotionMigrationQuiescence(context.Context) ([]state.PromotionMigrationQuiescenceJob, error) {
	return s.jobs, nil
}
func (s *recoveryRuntimeTestStore) AcknowledgePromotionMigrationQuiescence(_ context.Context, j state.PromotionMigrationQuiescenceJob) error {
	s.acknowledged = append(s.acknowledged, j)
	return nil
}

type recoveryRuntimeTestBackend struct {
	err       error
	destroyed []state.PromotionMigrationQuiescenceJob
}

func (b *recoveryRuntimeTestBackend) Restore(context.Context, AppTaskRestoreRequest) (AppTaskSession, error) {
	return nil, errors.New("recovery must never restore a VM")
}
func (b *recoveryRuntimeTestBackend) QuiescePromotionMigration(_ context.Context, j state.PromotionMigrationQuiescenceJob) error {
	b.destroyed = append(b.destroyed, j)
	return b.err
}
func TestPromotionRecoveryAcknowledgesOnlyConfirmedTeardown(t *testing.T) {
	for _, failed := range []bool{false, true} {
		job := state.PromotionMigrationQuiescenceJob{AccountID: "account", PromotionID: "promotion", RequestID: "request", TaskID: "task", NodeID: "node", InstanceID: "lease-runtime", LeaseToken: "token"}
		store := &recoveryRuntimeTestStore{jobs: []state.PromotionMigrationQuiescenceJob{job}}
		backend := &recoveryRuntimeTestBackend{}
		if failed {
			backend.err = errors.New("vmmd unavailable")
		}
		c := NewAppTaskCoordinator(store, backend, AppTaskCoordinatorConfig{}, nil)
		err := c.quiescePromotionMigrations(t.Context())
		if failed != (err != nil) || len(backend.destroyed) != 1 || backend.destroyed[0] != job || !failed && (len(store.acknowledged) != 1 || store.acknowledged[0] != job) || failed && len(store.acknowledged) != 0 {
			t.Fatalf("failed=%v err=%v acknowledgements=%v", failed, err, store.acknowledged)
		}
	}
}
func TestRoutedPromotionRecoveryUsesPersistedRuntimeIdentity(t *testing.T) {
	router := &recoveryRoutingTestVMM{}
	backend := NewRoutedVmmdAppTaskBackend(router, func(_ context.Context, r AppTaskRestoreRequest) (ResolvedAppTaskRuntime, error) {
		return ResolvedAppTaskRuntime{NodeID: "node", RuntimeID: "lease-runtime", Spec: AppTaskRestoreSpec{Instance: "lease-runtime", DeploymentID: r.DeploymentID}}, nil
	})
	session, err := backend.Restore(t.Context(), AppTaskRestoreRequest{ID: "logical-task", DeploymentID: "deployment"})
	if err != nil {
		t.Fatal(err)
	}
	if router.instance != "lease-runtime" {
		t.Fatal("task ID reused as runtime ID")
	}
	if err = session.Destroy(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err = backend.QuiescePromotionMigration(t.Context(), state.PromotionMigrationQuiescenceJob{NodeID: "registered-node", InstanceID: "lease-runtime"}); err != nil || router.node != "registered-node" {
		t.Fatal("quiescence did not target registered runtime", err)
	}
}

type recoveryRoutingTestVMM struct{ node, instance string }

func (r *recoveryRoutingTestVMM) RestoreAppTask(_ context.Context, node string, spec AppTaskRestoreSpec) (*AppTaskRestoreOutcome, error) {
	r.node, r.instance = node, spec.Instance
	return &AppTaskRestoreOutcome{Instance: spec.Instance}, nil
}
func (r *recoveryRoutingTestVMM) ExecuteAppTask(context.Context, string, string, apptaskproto.Request) (apptaskproto.Result, error) {
	return apptaskproto.Result{}, errors.New("recovery must not execute a command")
}
func (r *recoveryRoutingTestVMM) Destroy(_ context.Context, node, instance string) error {
	r.node, r.instance = node, instance
	return nil
}
