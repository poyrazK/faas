package state_test

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"reflect"
	"testing"
	"time"
)

func TestWorkflowRecoveryActionReceipts(t *testing.T) {
	state.WorkflowScheduleStores(t, func(t *testing.T, store state.Store) {
		ctx := state.WithEventRecoveryActor(context.Background(), "api_key", "response-reviewer")
		run, app := state.SeedResumeRun(t, store, state.SimpleResumeSpec())
		state.FailResumeRun(t, store, run, "send")
		recovery := store.(state.EventRecoveryStore)
		request := uuid.NewString()
		job, err := recovery.CreateEventRecovery(ctx, app.AccountID, app.ID, api.EventRecoveryRequest{RequestID: request, WorkflowRuns: &api.WorkflowRunRecoveryPlan{Name: run.WorkflowName, Runs: []api.WorkflowRunRecoveryTarget{{RunID: run.ID}}}})
		if err != nil {
			t.Fatal(err)
		}
		receipts := store.(state.WorkflowRecoveryActionReceiptStore)
		controls := store.(state.WorkflowRecoveryCancellationStore)
		found, err := receipts.GetEventRecoveryByRequest(ctx, app.AccountID, app.ID, request)
		if err != nil || found.ID != job.ID {
			t.Fatalf("by request=%+v %v", found, err)
		}
		for _, scope := range [][2]string{{uuid.NewString(), app.ID}, {app.AccountID, uuid.NewString()}} {
			if _, err := receipts.GetEventRecoveryByRequest(ctx, scope[0], scope[1], request); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("foreign scope=%v", err)
			}
		}
		n := 1
		req := api.WorkflowRecoveryCancellationRequest{RequestID: uuid.NewString(), Position: 1, ExpectedResumeCount: &n, Reason: "provider incident"}
		if _, err = controls.WorkflowRecoveryCancellation(ctx, app.AccountID, job.ID, req, true); err != nil {
			t.Fatal(err)
		}
		if _, err = receipts.GetWorkflowRecoveryActionReceipt(ctx, app.AccountID, job.ID, req.RequestID); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("preview wrote receipt: %v", err)
		}
		refused, err := controls.WorkflowRecoveryCancellation(ctx, app.AccountID, job.ID, req, false)
		if err != nil || refused.Outcome != "not_admitted" {
			t.Fatalf("refusal=%+v %v", refused, err)
		}
		if did, e := recovery.ProcessNextEventRecovery(ctx, time.Now().UTC()); e != nil || !did {
			t.Fatalf("admit=%v %v", did, e)
		}
		repeated, err := controls.WorkflowRecoveryCancellation(ctx, app.AccountID, job.ID, req, false)
		if err != nil || !reflect.DeepEqual(repeated, refused) {
			t.Fatalf("refusal not frozen=%+v %v", repeated, err)
		}
		req.RequestID = uuid.NewString()
		type result struct {
			out api.WorkflowRecoveryCancellationResponse
			err error
		}
		results := make(chan result, 2)
		for i := 0; i < 2; i++ {
			go func() {
				out, e := controls.WorkflowRecoveryCancellation(ctx, app.AccountID, job.ID, req, false)
				results <- result{out, e}
			}()
		}
		for i := 0; i < 2; i++ {
			r := <-results
			if r.err != nil || r.out.Outcome != "cancelled" {
				t.Fatalf("concurrent keyed apply=%+v %v", r.out, r.err)
			}
		}
		receipt, err := receipts.GetWorkflowRecoveryActionReceipt(ctx, app.AccountID, job.ID, req.RequestID)
		if err != nil || receipt.Response.Outcome != "cancelled" || receipt.ActorID != "response-reviewer" || receipt.CapturedAt.IsZero() {
			t.Fatalf("receipt=%+v %v", receipt, err)
		}
		// Mutation of returned values must not alter the durable request.
		*receipt.Request.ExpectedResumeCount = 9
		if _, err = receipts.GetWorkflowRecoveryActionReceipt(ctx, uuid.NewString(), job.ID, req.RequestID); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("foreign receipt=%v", err)
		}
		changed := req
		changed.Reason = "changed"
		if _, err = controls.WorkflowRecoveryCancellation(ctx, app.AccountID, job.ID, changed, false); !errors.Is(err, state.ErrEventRecoveryRequestConflict) {
			t.Fatalf("changed action=%v", err)
		}
		// Model a newer live generation without bypassing the public cancellation
		// safety rule that cancelled runs cannot normally be resumed.
		if mem, ok := store.(*state.MemStore); ok {
			(*mem.Mu()).Lock()
			live := (*mem.WorkflowRuns())[run.ID]
			live.ResumeCount = 2
			(*mem.WorkflowRuns())[run.ID] = live
			(*mem.Mu()).Unlock()
		} else if _, err = (*store.(*state.PgStore).Pool()).Exec(ctx, "UPDATE workflow_runs SET resume_count=2 WHERE id=$1::uuid", run.ID); err != nil {
			t.Fatal(err)
		}
		repeated, err = controls.WorkflowRecoveryCancellation(ctx, app.AccountID, job.ID, req, false)
		if err != nil || repeated.Outcome != "cancelled" {
			t.Fatalf("lost reply after generation advanced=%+v %v", repeated, err)
		}
		h, err := recovery.ListEventRecoveryHistory(ctx, app.AccountID, job.ID, 0, 100)
		if err != nil || len(h.Entries) != 2 {
			t.Fatalf("duplicate audit=%+v %v", h, err)
		}
		for i := 2; i < api.WorkflowRecoveryActionReceiptsMax; i++ {
			extra := req
			extra.RequestID = uuid.NewString()
			if _, err = controls.WorkflowRecoveryCancellation(ctx, app.AccountID, job.ID, extra, false); err != nil {
				t.Fatalf("receipt %d: %v", i, err)
			}
		}
		extra := req
		extra.RequestID = uuid.NewString()
		if _, err = controls.WorkflowRecoveryCancellation(ctx, app.AccountID, job.ID, extra, false); !errors.Is(err, state.ErrWorkflowRecoveryActionReceiptLimit) {
			t.Fatalf("unbounded receipts=%v", err)
		}
		if _, err = controls.WorkflowRecoveryCancellation(ctx, app.AccountID, job.ID, req, false); err != nil {
			t.Fatalf("existing receipt blocked by quota=%v", err)
		}
	})
}
