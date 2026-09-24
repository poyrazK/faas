package state

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestMemStoreJobsAtomicAccountAndRunCapacity(t *testing.T) {
	ctx := context.Background()
	store := NewMemStore()
	job, run, _ := newJobAndRun(t, store, "acct-capacity", "capacity")
	claim := func(runID string, index int) (string, error) {
		id := newUUIDString()
		_, err := store.CreateAndClaimJobInstance(ctx, id, job.ID, runID, index,
			"cold_booting", 128, DefaultLocalNodeName, id, newUUIDString(), time.Now().Add(time.Minute), DefaultLocalNodeName)
		return id, err
	}
	for index := 0; index < 2; index++ {
		if _, err := claim(run.ID, index); err != nil {
			t.Fatalf("claim run task %d: %v", index, err)
		}
	}
	rejectedID, err := claim(run.ID, 2)
	var quota *JobQuotaError
	if !errors.As(err, &quota) || quota.Scope != JobQuotaScopeParallelism || quota.Limit != 2 {
		t.Fatalf("third run claim = %v, want parallelism cap 2", err)
	}
	if _, err := store.InstanceByID(ctx, rejectedID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("rejected run instance exists: %v", err)
	}

	other, _, err := store.JobRunCreate(ctx, job.ID, job.AccountID, "manual", nil, nil, nil, nil, 3)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := claim(other.ID, 0); err != nil {
		t.Fatalf("claim third account slot: %v", err)
	}
	rejectedID, err = claim(other.ID, 1)
	if !errors.As(err, &quota) || quota.Scope != JobQuotaScopeConcurrent || quota.Limit != 3 {
		t.Fatalf("fourth account claim = %v, want account cap 3", err)
	}
	if _, err := store.InstanceByID(ctx, rejectedID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("rejected account instance exists: %v", err)
	}
	queued, err := store.JobTaskGet(ctx, other.ID, 1)
	if err != nil || queued.Status != "queued" {
		t.Fatalf("rejected task = %+v, %v, want queued", queued, err)
	}
}

func TestMemStoreJobsRunParallelismOverrideCanExceedTemplateDefault(t *testing.T) {
	ctx := context.Background()
	store := NewMemStore()
	account, err := store.CreateAccount(ctx, "job-override@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	job, _, _ := newJobAndRun(t, store, account.ID, "override") // template default is 4
	parallelism := 5
	run, _, err := store.JobRunCreate(ctx, job.ID, account.ID, "manual", &parallelism, nil, nil, nil, 5)
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 5; index++ {
		id := newUUIDString()
		if _, err := store.CreateAndClaimJobInstance(ctx, id, job.ID, run.ID, index,
			"cold_booting", 128, DefaultLocalNodeName, id, newUUIDString(), time.Now().Add(time.Minute), DefaultLocalNodeName); err != nil {
			t.Fatalf("claim override task %d: %v", index, err)
		}
	}
}
