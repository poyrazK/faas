package state_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 1273
func TestWorkflowInitialAttentionHistory(t *testing.T) {
	for _, receivers := range []bool{true, false} {
		state.WorkflowScheduleStores(t, func(t *testing.T, s state.Store) {
			ctx := state.WithEventRecoveryActor(context.Background(), "api_key", "initial-history-operator")
			run, app, r, _ := state.SeedInitialAutomatic(t, s)
			if receivers {
				if _, err := s.CreateAppWebhook(ctx, state.AppWebhook{AccountID: app.AccountID, AppID: app.ID, TargetURL: "https://initial-proof.example.test/hook", SecretSealed: []byte("sealed"), Enabled: true, EventFilter: []string{string(state.AppWebhookEventRecoveryQueueBlocked)}}); err != nil {
					t.Fatal(err)
				}
			}
			state.PollInitialAutomatic(t, s, time.Now().UTC())
			r = state.InitialAutomaticReview(t, s, app, r.ID)
			if _, err := s.(state.EventRecoveryStore).ProcessNextEventRecovery(ctx, time.Now().UTC()); err != nil {
				t.Fatal(err)
			}
			if _, err := s.ClaimNextDueWorkflowRun(ctx); err != nil {
				t.Fatal(err)
			}
			if err := s.MarkWorkflowRunStatus(ctx, run.ID, state.WorkflowRunStatusFailed, nil, nil); err != nil {
				t.Fatal(err)
			}
			if m, ok := s.(*state.MemStore); ok {
				(*m.Mu()).Lock()
				delete((*m.EventRecoveryJobs())[r.JobID].WorkflowResults, 1)
				(*m.Mu()).Unlock()
			} else {
				if _, err := (*s.(*state.PgStore).Pool()).Exec(ctx, "DELETE FROM event_recovery_workflow_results WHERE job_id=$1::uuid", r.JobID); err != nil {
					t.Fatal(err)
				}
			}
			state.PollInitialAutomatic(t, s, time.Now().Add(time.Minute))
			blocked := state.InitialAutomaticReview(t, s, app, r.ID)
			if blocked.AutomaticBlockNotification == nil || blocked.AutomaticBlockNotification.DiagnosisReason != "terminal_outcome_not_saved" || blocked.AutomaticBlockNotification.EvidenceKind != "initial" {
				t.Fatalf("guidance %+v", blocked)
			}
			// The same proof cannot emit another alert or event.
			state.PollInitialAutomatic(t, s, time.Now().Add(2*time.Minute))
			if state.InitialAutomaticReview(t, s, app, r.ID).AutomaticBlockSequence != blocked.AutomaticBlockSequence {
				t.Fatal("repeat alert")
			}
			req := api.WorkflowInitialEvidenceRepairRequest{RequestID: uuid.NewString(), Reason: "retain verified initial outcome"}
			for range 2 {
				if _, err := s.(state.WorkflowInitialEvidenceStore).RepairWorkflowInitialEvidence(ctx, app.AccountID, app.ID, r.ID, req); err != nil {
					t.Fatal(err)
				}
			}
			state.PollInitialAutomatic(t, s, time.Now().Add(3*time.Minute))
			final := state.InitialAutomaticReview(t, s, app, r.ID)
			if final.AutomaticBlockReason != "" || final.AutomaticBlockNotification.State != "recovered" {
				t.Fatalf("clear %+v", final)
			}
			if _, err := s.(state.AppWebhookEventOutboxStore).DrainAppWebhookEventOutbox(ctx, 32); err != nil {
				t.Fatal(err)
			}
			deliveries, err := s.ClaimDueAppWebhookDeliveries(ctx, 32, time.Now().Add(time.Hour))
			if err != nil {
				t.Fatal(err)
			}
			count := 0
			for _, delivery := range deliveries {
				if delivery.Event != state.AppWebhookEventRecoveryQueueBlocked {
					continue
				}
				count++
				var p api.WorkflowRecoveryQueueBlockedWebhookPayload
				if err = json.Unmarshal(delivery.Payload, &p); err != nil {
					t.Fatal(err)
				}
				if p.EvidenceKind != "initial" || p.RunID != run.ID || p.ResumeCount == nil || *p.ResumeCount != 1 || p.Repairable == nil || p.EvidenceURL == "" || p.CLICommand == "" {
					t.Fatalf("payload %s", delivery.Payload)
				}
				if p.State == "blocked" && !*p.Repairable {
					t.Fatal("verified repair missing")
				}
			}
			want := 0
			if receivers {
				want = 2
			}
			if count != want {
				t.Fatalf("events %d", count)
			}
			// Repairs occur now; worker alert timestamps are supplied by the worker clock.
			history, err := s.(state.WorkflowRecoveryHistoryStore).GetWorkflowRecoveryHistory(ctx, app.AccountID, app.ID, api.WorkflowRecoveryHistoryOptions{RunID: run.ID, Limit: 100})
			if err != nil {
				t.Fatal(err)
			}
			repairs := 0
			for _, e := range history.Entries {
				if e.Kind == "initial_evidence_repaired" {
					repairs++
					if !e.Durable || e.InitialFailureID != r.Failure.ID || e.RequestID != req.RequestID || e.ActorID != "initial-history-operator" || e.Outcome != "failed" || e.ResumeCount == nil || *e.ResumeCount != 1 {
						t.Fatalf("repair event %+v", e)
					}
				}
			}
			if repairs != 1 {
				t.Fatalf("repair count %d", repairs)
			}
			// Check all committed transition events directly, including future-clock captures.
			if m, ok := s.(*state.MemStore); ok {
				(*m.Mu()).Lock()
				counts := map[string]int{}
				for _, e := range (*m.RecoveryPolicies())[state.CanonicalMemUUID(app.ID)].History {
					counts[e.Entry.Kind]++
				}
				(*m.Mu()).Unlock()
				if counts["initial_recovery_blocked"] != 1 || counts["initial_recovery_cleared"] != 1 {
					t.Fatalf("history %+v", counts)
				}
			} else {
				var blocks, clears int
				if err = (*s.(*state.PgStore).Pool()).QueryRow(ctx, "SELECT count(*) FILTER(WHERE entry->>'kind'='initial_recovery_blocked'),count(*) FILTER(WHERE entry->>'kind'='initial_recovery_cleared') FROM workflow_recovery_history_events WHERE app_id=$1::uuid", app.ID).Scan(&blocks, &clears); err != nil {
					t.Fatal(err)
				}
				if blocks != 1 || clears != 1 {
					t.Fatalf("history %d %d", blocks, clears)
				}
			}
		})
	}
}

// adr: 1273
func TestWorkflowInitialAttentionDiagnosisChanges(t *testing.T) {
	r := api.WorkflowInitialRecoveryReview{ID: uuid.NewString(), Policy: api.WorkflowRecoveryPolicy{Mode: "automatic_initial"}}
	d := api.WorkflowInitialEvidenceDiagnosis{Reason: "completion_time_unverified"}
	p := state.InitialAutomaticBlockTransition(&r, "evidence_unknown", time.Now(), d)
	receipt := state.AutomaticBlockedReceipt(*p, nil)
	r.AutomaticBlockNotification = &receipt
	if state.InitialAutomaticBlockTransition(&r, "evidence_unknown", time.Now(), d) != nil {
		t.Fatal("unchanged diagnosis repeated")
	}
	d.Reason = "generation_superseded"
	if p = state.InitialAutomaticBlockTransition(&r, "evidence_unknown", time.Now(), d); p == nil || p.State != "blocked" || p.DiagnosisReason != d.Reason {
		t.Fatal("changed diagnosis lost")
	}
	if state.InitialAutomaticBlockTransition(&r, "policy_changed", time.Now(), d) != nil || r.AutomaticBlockReason != "evidence_unknown" {
		t.Fatal("policy change cleared blocker")
	}
}
