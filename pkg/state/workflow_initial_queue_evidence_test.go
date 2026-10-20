package state_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// adr: 1272
func TestWorkflowInitialQueuePreparedEvidence(t *testing.T) {
	for _, paused := range []bool{false, true} {
		state.WorkflowScheduleStores(t, func(t *testing.T, s state.Store) {
			ctx := context.Background()
			run, app, r, p := state.SeedInitialAutomatic(t, s)
			operator := s.(state.WorkflowRecoveryPolicyOperatorStore)
			if paused {
				if _, err := operator.ControlWorkflowRecoveryPolicy(ctx, app.AccountID, app.ID, state.ControlRequest(p, "pause")); err != nil {
					t.Fatal(err)
				}
			}
			retained := func() api.WorkflowInitialRecoveryReview {
				t.Helper()
				if m, ok := s.(*state.MemStore); ok {
					(*m.Mu()).Lock()
					defer (*m.Mu()).Unlock()
					return state.RecoveryPolicyClone((*m.InitialRecoveryReviews())[state.CanonicalMemUUID(r.ID)])
				}
				raw, err := sqlc.New().WorkflowInitialReviewGet(ctx, (*s.(*state.PgStore).Pool()), sqlc.WorkflowInitialReviewGetParams{AccountID: state.MustPgUUID(app.AccountID), AppID: state.MustPgUUID(app.ID), ID: state.MustPgUUID(r.ID)})
				if err != nil {
					t.Fatal(err)
				}
				saved, err := state.DecodeInitialReview(raw)
				if err != nil {
					t.Fatal(err)
				}
				return saved
			}
			before := retained()
			for range 2 {
				q, err := operator.GetWorkflowRecoveryAutomaticQueue(ctx, app.AccountID, app.ID)
				if err != nil || q.Pending != 1 || q.Outstanding != 0 || q.HourlyExecutions != 0 || len(q.Entries) != 1 {
					t.Fatalf("queue %+v %v", q, err)
				}
				e := q.Entries[0]
				if e.InitialEvidence == nil || e.Evidence != nil {
					t.Fatal("initial proof missing or retry proof invented")
				}
				d := e.InitialEvidence.Diagnosis
				if e.InitialEvidence.Stage != "prepared" || d.JobID != "" || d.Repairable || d.ResumeCount != 1 || d.RunID != run.ID || d.Reason != "not_submitted_automatic_initial" || !d.ObservedAt.Equal(q.ObservedAt) {
					t.Fatalf("prepared proof %+v", e)
				}
				if !strings.Contains(e.InitialEvidence.CLICommand, "failure-recovery-evidence") || strings.Contains(e.InitialEvidence.CLICommand, "--repair") || !strings.Contains(e.InitialEvidence.EvidenceURL, "/initial-recovery-reviews/"+r.ID+"/evidence") {
					t.Fatal("wrong initial guidance")
				}
				if paused && (e.WaitCategory != "policy" || e.WaitReason != "automatic_paused") {
					t.Fatal("policy pause lost")
				}
			}
			if !reflect.DeepEqual(before, retained()) {
				t.Fatal("queue changed prepared review or receipts")
			}
			if _, err := operator.GetWorkflowRecoveryAutomaticQueue(ctx, uuid.NewString(), app.ID); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("scope %v", err)
			}
		})
	}
}
