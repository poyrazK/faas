package state

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"sync"
	"testing"
	"time"
)

func TestWorkflowRunRecoveryJobs(t *testing.T) {
	for _, scenario := range []string{"queue", "changed", "cancel", "unsafe", "scope"} {
		t.Run(scenario, func(t *testing.T) {
			workflowScheduleStores(t, func(t *testing.T, store Store) {
				ctx := context.Background()
				run, app := seedResumeRun(t, store, simpleResumeSpec())
				failResumeRun(t, store, run, "send")
				recovery := store.(EventRecoveryStore)
				req := api.EventRecoveryRequest{RequestID: uuid.NewString(), RatePerSecond: 1, WorkflowRuns: &api.WorkflowRunRecoveryPlan{Name: run.WorkflowName, Runs: []api.WorkflowRunRecoveryTarget{{RunID: run.ID, ExpectedResumeCount: 0}}}}
				if scenario == "scope" {
					req.WorkflowRuns.Name = "other"
					if _, err := recovery.CreateEventRecovery(ctx, app.AccountID, app.ID, req); !errors.Is(err, ErrEventRecoveryChanged) {
						t.Fatalf("scope=%v", err)
					}
					return
				}
				preview, err := recovery.PreviewEventRecovery(ctx, app.AccountID, app.ID, req)
				if err != nil || preview.MatchedCount != 1 || preview.Sample[0].WorkflowRunID != run.ID || preview.Coverage != "reviewed_workflow_runs" {
					t.Fatalf("preview=%+v err=%v", preview, err)
				}
				job, err := recovery.CreateEventRecovery(ctx, app.AccountID, app.ID, req)
				if err != nil {
					t.Fatal(err)
				}
				retry, err := recovery.CreateEventRecovery(ctx, app.AccountID, app.ID, req)
				if err != nil || retry.ID != job.ID {
					t.Fatalf("retry=%+v %v", retry, err)
				}
				changed := req
				changed.RatePerSecond = 2
				if _, err := recovery.CreateEventRecovery(ctx, app.AccountID, app.ID, changed); !errors.Is(err, ErrEventRecoveryRequestConflict) {
					t.Fatalf("conflict=%v", err)
				}
				if pg, ok := store.(*PgStore); ok {
					recovery = NewPgStore(pg.pool.Pool)
				}
				if _, err := recovery.GetEventRecovery(ctx, uuid.NewString(), job.ID); !errors.Is(err, ErrNotFound) {
					t.Fatalf("tenant=%v", err)
				}
				// A caller cannot mutate the stored fence through response pointers.
				job.Selection.WorkflowRuns.Runs[0].ExpectedResumeCount = 9
				items, err := recovery.ListEventRecoveryItems(ctx, app.AccountID, job.ID, 0, 25)
				if err != nil {
					t.Fatal(err)
				}
				*items.Items[0].ExpectedResumeCount = 9
				if _, err := recovery.PauseEventRecovery(ctx, app.AccountID, job.ID); err != nil {
					t.Fatal(err)
				}
				if did, err := recovery.ProcessNextEventRecovery(ctx, time.Now().UTC()); err != nil || did {
					t.Fatalf("pause=%v %v", did, err)
				}
				if _, err := recovery.ResumeEventRecovery(ctx, app.AccountID, job.ID); err != nil {
					t.Fatal(err)
				}
				switch scenario {
				case "changed":
					if _, _, _, err := store.(WorkflowResumeStore).ResumeWorkflowRun(ctx, resumeOptions(run, app, 0)); err != nil {
						t.Fatal(err)
					}
				case "cancel":
					if _, err := recovery.CancelEventRecovery(ctx, app.AccountID, job.ID); err != nil {
						t.Fatal(err)
					}
				case "unsafe":
					message := "cancelled by operator"
					if err := store.MarkWorkflowRunStatus(ctx, run.ID, WorkflowRunStatusDead, nil, &message); err != nil {
						t.Fatal(err)
					}
				}
				did, err := recovery.ProcessNextEventRecovery(ctx, time.Now().UTC())
				if err != nil || did != (scenario != "cancel") {
					t.Fatalf("worker=%v %v", did, err)
				}
				items, err = recovery.ListEventRecoveryItems(ctx, app.AccountID, job.ID, 0, 25)
				if err != nil {
					t.Fatal(err)
				}
				want := map[string]string{"queue": "queued", "changed": "skipped", "cancel": "cancelled", "unsafe": "skipped"}[scenario]
				if items.Items[0].State != want || items.Items[0].WorkflowRunID != run.ID || *items.Items[0].ExpectedResumeCount != 0 {
					t.Fatalf("item=%+v", items.Items[0])
				}
				resumes, err := store.(WorkflowResumeStore).ListWorkflowResumes(ctx, run.ID)
				if err != nil {
					t.Fatal(err)
				}
				n := 0
				if scenario == "queue" || scenario == "changed" {
					n = 1
				}
				if len(resumes) != n {
					t.Fatalf("resumes=%d want=%d", len(resumes), n)
				}
				retry, err = recovery.CreateEventRecovery(ctx, app.AccountID, app.ID, req)
				if err != nil || retry.ID != job.ID {
					t.Fatalf("terminal retry=%+v %v", retry, err)
				}
			})
		})
	}
}

func TestWorkflowRunRecoveryCapacityWait(t *testing.T) {
	workflowScheduleStores(t, func(t *testing.T, store Store) {
		ctx := context.Background()
		run, app := seedResumeRun(t, store, simpleResumeSpec())
		failResumeRun(t, store, run, "send")
		active := []*WorkflowRun{}
		for i := 0; i < api.PlanHobby.WorkflowMaxConcurrentRuns(); i++ {
			other := &WorkflowRun{AppID: app.ID, WorkflowName: run.WorkflowName, DefinitionSnapshot: run.DefinitionSnapshot, Input: run.Input}
			if err := store.CreateWorkflowRun(ctx, other); err != nil {
				t.Fatal(err)
			}
			active = append(active, other)
		}
		recovery := store.(EventRecoveryStore)
		req := api.EventRecoveryRequest{RequestID: uuid.NewString(), WorkflowRuns: &api.WorkflowRunRecoveryPlan{Name: run.WorkflowName, Runs: []api.WorkflowRunRecoveryTarget{{RunID: run.ID}}}}
		job, err := recovery.CreateEventRecovery(ctx, app.AccountID, app.ID, req)
		if err != nil {
			t.Fatal(err)
		}
		now := time.Now().UTC()
		if did, err := recovery.ProcessNextEventRecovery(ctx, now); err != nil || !did {
			t.Fatalf("capacity worker=%v %v", did, err)
		}
		saved, err := recovery.GetEventRecovery(ctx, app.AccountID, job.ID)
		if err != nil || saved.PendingCount != 1 || saved.QueuedCount != 0 {
			t.Fatalf("capacity job=%+v %v", saved, err)
		}
		records, err := store.(WorkflowResumeStore).ListWorkflowResumes(ctx, run.ID)
		if err != nil || len(records) != 0 {
			t.Fatalf("capacity resumed=%+v %v", records, err)
		}
		if did, err := recovery.ProcessNextEventRecovery(ctx, now); err != nil || did {
			t.Fatalf("bounded retry=%v %v", did, err)
		}
		if err := store.MarkWorkflowRunStatus(ctx, active[0].ID, WorkflowRunStatusSucceeded, nil, nil); err != nil {
			t.Fatal(err)
		}
		if did, err := recovery.ProcessNextEventRecovery(ctx, now.Add(EventDeliveryCapacityRetryDelay+time.Second)); err != nil || !did {
			t.Fatalf("admission=%v %v", did, err)
		}
		saved, err = recovery.GetEventRecovery(ctx, app.AccountID, job.ID)
		if err != nil || saved.QueuedCount != 1 || saved.PendingCount != 0 {
			t.Fatalf("resumed=%+v %v", saved, err)
		}
	})
}

func TestWorkflowRunRecoveryConcurrentJobs(t *testing.T) {
	workflowScheduleStores(t, func(t *testing.T, store Store) {
		ctx := context.Background()
		run, app := seedResumeRun(t, store, simpleResumeSpec())
		failResumeRun(t, store, run, "send")
		recovery := store.(EventRecoveryStore)
		// Initial recovery admission rejects duplicate jobs. Exercise competing
		// worker claims on a later generation, retaining the same one-resume fence.
		if _, err := recovery.CreateEventRecovery(ctx, app.AccountID, app.ID, api.EventRecoveryRequest{RequestID: uuid.NewString(), WorkflowRuns: &api.WorkflowRunRecoveryPlan{Name: run.WorkflowName, Runs: []api.WorkflowRunRecoveryTarget{{RunID: run.ID}}}}); err != nil {
			t.Fatal(err)
		}
		if did, err := recovery.ProcessNextEventRecovery(ctx, time.Now().UTC()); err != nil || !did {
			t.Fatalf("initial recovery %v %v", did, err)
		}
		if _, err := store.ClaimNextDueWorkflowRun(ctx); err != nil {
			t.Fatal(err)
		}
		failResumeRun(t, store, run, "send")
		jobs := []string{}
		for i := 0; i < 2; i++ {
			job, err := recovery.CreateEventRecovery(ctx, app.AccountID, app.ID, api.EventRecoveryRequest{RequestID: uuid.NewString(), WorkflowRuns: &api.WorkflowRunRecoveryPlan{Name: run.WorkflowName, Runs: []api.WorkflowRunRecoveryTarget{{RunID: run.ID, ExpectedResumeCount: 1}}}})
			if err != nil {
				t.Fatal(err)
			}
			jobs = append(jobs, job.ID)
		}
		var wg sync.WaitGroup
		errs := make(chan error, 8)
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, err := recovery.ProcessNextEventRecovery(ctx, time.Now().UTC())
				errs <- err
			}()
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatal(err)
			}
		}
		queued, skipped := int64(0), int64(0)
		for _, id := range jobs {
			job, err := recovery.GetEventRecovery(ctx, app.AccountID, id)
			if err != nil {
				t.Fatal(err)
			}
			queued += job.QueuedCount
			skipped += job.SkippedCount
		}
		if queued != 1 || skipped != 1 {
			t.Fatalf("queued=%d skipped=%d", queued, skipped)
		}
		records, err := store.(WorkflowResumeStore).ListWorkflowResumes(ctx, run.ID)
		if err != nil || len(records) != 2 {
			t.Fatalf("resumes=%+v %v", records, err)
		}
	})
}
