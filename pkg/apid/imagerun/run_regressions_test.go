package imagerun_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type interleavedRunStore struct {
	store        *state.MemStore
	beforeCreate func()
}

func (s *interleavedRunStore) JobEnsureImageRun(ctx context.Context, in state.ImageRunEnsure) (state.ImageRunEnsureResult, error) {
	return s.store.JobEnsureImageRun(ctx, in)
}

func (s *interleavedRunStore) JobRunCreate(ctx context.Context, jobID, accountID, trigger string, parallelism, retry, timeout *int, env json.RawMessage, tasks int, options ...state.JobRunOptions) (state.JobRun, []state.JobTask, error) {
	s.beforeCreate()
	return s.store.JobRunCreate(ctx, jobID, accountID, trigger, parallelism, retry, timeout, env, tasks, options...)
}

func TestRunRegressionConcurrentImageRunRAM(t *testing.T) {
	hs := newHarness(t, api.PlanPro)
	req := api.CreateImageRunRequest{Image: "alpine", RAMMB: 128, TimeoutSec: 60, Args: strs("id")}
	status, initial, body := hs.post(req)
	if status != http.StatusAccepted {
		t.Fatalf("prepare: %d %s", status, body)
	}
	hs.materialize(initial.Job, nil, []string{"id"})
	job, err := hs.store.JobGetByName(context.Background(), hs.acct.ID, initial.Job)
	if err != nil {
		t.Fatal(err)
	}
	hs.h.Store = &interleavedRunStore{store: hs.store, beforeCreate: func() {
		// Another caller updates the shared runner between this request's
		// ensure and run-creation transactions.
		_, err := hs.store.JobEnsureImageRun(context.Background(), state.ImageRunEnsure{
			AccountID: hs.acct.ID, Name: job.Name, ImageRef: job.ImageRef,
			RAMMB: 512, TaskTimeoutSec: 60, MaxCached: 10,
		})
		if err != nil {
			t.Fatal(err)
		}
	}}
	status, resp, body := hs.post(req)
	if status != http.StatusCreated {
		t.Fatalf("create: %d %s", status, body)
	}
	run, err := hs.store.JobRunGetByID(context.Background(), resp.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if run.RAMMBSnapshot == nil || *run.RAMMBSnapshot != 128 {
		t.Errorf("run did not retain requested RAM: requested=128 snapshot=%v", run.RAMMBSnapshot)
		if run.RAMMBSnapshot != nil {
			t.Logf("actual RAM snapshot: %d", *run.RAMMBSnapshot)
		}
	}
}

func TestRunRegressionRefreshBetweenLookupAndAdmission(t *testing.T) {
	hs := newHarness(t, api.PlanPro)
	req := api.CreateImageRunRequest{Image: "alpine", Args: strs("id")}
	_, initial, _ := hs.post(req)
	hs.materialize(initial.Job, nil, []string{"id"})
	job, err := hs.store.JobGetByName(t.Context(), hs.acct.ID, initial.Job)
	if err != nil {
		t.Fatal(err)
	}
	hs.h.Store = &interleavedRunStore{store: hs.store, beforeCreate: func() {
		_, err := hs.store.JobEnsureImageRun(t.Context(), state.ImageRunEnsure{AccountID: hs.acct.ID,
			Name: job.Name, ImageRef: job.ImageRef, RAMMB: 128, TaskTimeoutSec: 60, PullAlways: true, MaxCached: 10})
		if err != nil {
			t.Fatal(err)
		}
	}}
	response := hs.postRaw(req)
	if response.Code != http.StatusConflict || response.Header().Get("Retry-After") == "" {
		t.Fatalf("raced refresh admission=%d %s headers=%v", response.Code, response.Body, response.Header())
	}
	hs.h.Store = hs.store
	hs.materialize(initial.Job, []string{"/new-entrypoint"}, nil)
	status, queued, body := hs.post(req)
	if status != http.StatusCreated || len(queued.Command) != 2 || queued.Command[0] != "/new-entrypoint" {
		t.Fatalf("fresh admission=%d %s", status, body)
	}
}
