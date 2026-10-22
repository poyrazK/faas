package conformance

import (
	"errors"
	"math"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 1163
// testJobTaskLogTarget pins the lookup behind live job task output: the
// task is found only through its own account and job name, and carries the
// node of its current instance once claimed.
func testJobTaskLogTarget(t *testing.T, fx *Fixture) {
	targets, ok := fx.Store.(state.JobTaskLogTargetStore)
	if !ok {
		t.Fatalf("%T does not implement state.JobTaskLogTargetStore", fx.Store)
	}
	name := "logs-" + uuid.NewString()[:8]
	job, err := fx.Store.JobCreate(fx.Ctx, fx.Account.ID, name, "batch",
		"ghcr.io/onebox-faas/conformance:latest", []string{"/bin/true"}, 128, 60, 1, 0, nil)
	if err != nil {
		t.Fatalf("JobCreate: %v", err)
	}
	parallelism := 1
	run, tasks, err := fx.Store.JobRunCreate(fx.Ctx, job.ID, fx.Account.ID, "manual", &parallelism, nil, nil, nil, 1)
	if err != nil || len(tasks) != 1 {
		t.Fatalf("JobRunCreate: tasks=%d err=%v", len(tasks), err)
	}
	got, err := targets.JobTaskLogTarget(fx.Ctx, fx.Account.ID, name, run.ID, 0)
	if err != nil || got.Status != "queued" || got.Attempt != 1 || got.InstanceID != "" || got.NodeID != "" || got.Terminal() {
		t.Fatalf("queued target = %+v, %v", got, err)
	}
	for label, args := range map[string]struct {
		account, job, run string
		index             int
	}{
		"other account":    {uuid.NewString(), name, run.ID, 0},
		"other job name":   {fx.Account.ID, name + "-x", run.ID, 0},
		"unknown run":      {fx.Account.ID, name, uuid.NewString(), 0},
		"unknown task":     {fx.Account.ID, name, run.ID, 1},
		"index past int32": {fx.Account.ID, name, run.ID, math.MaxInt32 + 1},
		"malformed run":    {fx.Account.ID, name, "run-1", 0},
	} {
		if _, err := targets.JobTaskLogTarget(fx.Ctx, args.account, args.job, args.run, args.index); !errors.Is(err, state.ErrNotFound) {
			t.Errorf("%s: err = %v, want ErrNotFound", label, err)
		}
	}
	instanceID := uuid.NewString()
	if _, err := fx.Store.CreateAndClaimJobInstance(fx.Ctx, instanceID, job.ID, run.ID, 0,
		"cold_booting", 128, fx.Node.ID, instanceID, uuid.NewString(), time.Now().Add(5*time.Minute), fx.Node.ID); err != nil {
		t.Fatalf("CreateAndClaimJobInstance: %v", err)
	}
	got, err = targets.JobTaskLogTarget(fx.Ctx, fx.Account.ID, name, run.ID, 0)
	if err != nil || got.Status != "claimed" || got.InstanceID != instanceID || got.NodeID != fx.Node.ID || got.Terminal() {
		t.Fatalf("claimed target = %+v, %v", got, err)
	}
}
