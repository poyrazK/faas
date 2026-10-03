// adr: 099 — capacity backoff must not erase a competing scheduler's job claim.
package sched

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type claimAfterJobBatchStore struct {
	state.Store
	claim func() error
}

func (s claimAfterJobBatchStore) JobTaskClaimBatch(ctx context.Context, limit int) ([]state.JobTask, error) {
	tasks, err := s.Store.JobTaskClaimBatch(ctx, limit)
	if err != nil {
		return nil, err
	}
	if err := s.claim(); err != nil {
		return nil, err
	}
	return tasks, nil
}

func TestDispatchJobsCapacityDeferralPreservesConcurrentClaim(t *testing.T) {
	ctx := t.Context()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "capacity-race@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	job, err := store.JobCreate(ctx, account.ID, "capacity-race", "batch",
		"ghcr.io/onebox-faas/conformance:latest", []string{"/bin/true"},
		128, 60, 3, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.JobSetImageMaterialization(ctx, job.ID, job.ImageRef, "ready",
		"sha256:"+strings.Repeat("a", 64), "jobs/"+job.ID+".ext4", ""); err != nil {
		t.Fatal(err)
	}
	run, _, err := store.JobRunCreate(ctx, job.ID, account.ID, "manual", nil, nil, nil, nil, 3)
	if err != nil {
		t.Fatal(err)
	}
	claim := func(index int) (string, string, error) {
		instanceID, lease := "instance-"+string(rune('0'+index)), "lease-"+string(rune('0'+index))
		_, err := store.CreateAndClaimJobInstance(ctx, instanceID, job.ID, run.ID, index,
			"cold_booting", 128, state.DefaultLocalNodeName, instanceID, lease,
			time.Now().Add(time.Minute), state.DefaultLocalNodeName)
		return instanceID, lease, err
	}
	for index := 0; index < 2; index++ {
		if _, _, err := claim(index); err != nil {
			t.Fatal(err)
		}
	}
	var winnerID, winnerLease string
	raced := claimAfterJobBatchStore{Store: store, claim: func() error {
		var err error
		winnerID, winnerLease, err = claim(2)
		return err
	}}
	engine := &Engine{store: raced, log: testLog()}
	if err := engine.DispatchJobsTick(ctx); err != nil {
		t.Fatal(err)
	}
	task, err := store.JobTaskGet(ctx, run.ID, 2)
	if err != nil || task.Status != "claimed" || task.InstanceID == nil || *task.InstanceID != winnerID ||
		task.LeaseToken == nil || *task.LeaseToken != winnerLease || task.NextAttemptAt != nil {
		t.Fatalf("concurrent winner was requeued: %+v, %v", task, err)
	}
}
