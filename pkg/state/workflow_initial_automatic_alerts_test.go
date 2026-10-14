package state

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestWorkflowInitialAutomaticAttention(t *testing.T) {
	for _, scenario := range []string{"clear_and_reblock", "reason_change", "no_receivers"} {
		t.Run(scenario, func(t *testing.T) {
			workflowScheduleStores(t, func(t *testing.T, s Store) {
				ctx := context.Background()
				_, app, review, _ := seedInitialAutomatic(t, s)
				hook := func() string {
					h, err := s.CreateAppWebhook(ctx, AppWebhook{AccountID: app.AccountID, AppID: app.ID, TargetURL: "https://attention.example.test/hook", SecretSealed: []byte("sealed"), Enabled: true, EventFilter: []string{string(AppWebhookEventRecoveryQueueBlocked)}})
					if err != nil {
						t.Fatal(err)
					}
					return h.ID
				}
				hookID := ""
				if scenario != "no_receivers" {
					hookID = hook()
				}
				now := time.Now().UTC().Truncate(time.Microsecond)
				pollInitialAutomatic(t, s, now)
				r := initialAutomaticReview(t, s, app, review.ID)
				job := r.JobID
				pause := func(state string) {
					jobs := s.(EventRecoveryStore)
					var err error
					if state == "paused" {
						_, err = jobs.PauseEventRecovery(ctx, app.AccountID, job)
					} else {
						_, err = jobs.ResumeEventRecovery(ctx, app.AccountID, job)
					}
					if err != nil {
						t.Fatal(err)
					}
				}
				pause("paused")
				pollInitialAutomatic(t, s, now.Add(time.Minute))
				r = initialAutomaticReview(t, s, app, review.ID)
				if r.AutomaticBlockReason != "job_paused" || r.AutomaticBlockSequence != 1 || r.AutomaticBlockNotification == nil {
					t.Fatalf("missing pause attention %+v", r)
				}
				first := r.AutomaticBlockNotification.EventID
				if scenario == "no_receivers" {
					if r.AutomaticBlockNotification.Status != "no_receivers" {
						t.Fatal(r.AutomaticBlockNotification)
					}
					hookID = hook()
				}
				pollInitialAutomatic(t, s, now.Add(2*time.Minute))
				r = initialAutomaticReview(t, s, app, review.ID)
				if r.AutomaticBlockSequence != 1 || r.AutomaticBlockNotification.EventID != first {
					t.Fatalf("unchanged block replayed %+v", r)
				}
				// Queue and review reads do not write or capture notifications.
				if _, err := s.(WorkflowRecoveryPolicyOperatorStore).GetWorkflowRecoveryAutomaticQueue(ctx, app.AccountID, app.ID); err != nil {
					t.Fatal(err)
				}
				if scenario == "reason_change" {
					if m, ok := s.(*MemStore); ok {
						m.mu.Lock()
						delete(m.eventRecoveryJobs, job)
						m.mu.Unlock()
					} else if _, err := s.(*PgStore).pool.Exec(ctx, "DELETE FROM event_recovery_jobs WHERE id=$1::uuid", job); err != nil {
						t.Fatal(err)
					}
				} else {
					pause("running")
				}
				pollInitialAutomatic(t, s, now.Add(3*time.Minute))
				r = initialAutomaticReview(t, s, app, review.ID)
				if r.AutomaticBlockSequence != 2 || r.AutomaticBlockNotification.EventID == first || r.AutomaticResult != nil {
					t.Fatalf("transition or proof %+v", r)
				}
				expected := "blocker_cleared"
				if scenario == "reason_change" {
					expected = "evidence_unknown"
				}
				if r.AutomaticBlockNotification.Reason != expected {
					t.Fatalf("reason %+v", r)
				}
				count := 2
				if scenario == "no_receivers" {
					count = 1
				}
				if scenario == "clear_and_reblock" {
					pause("paused")
					pollInitialAutomatic(t, s, now.Add(4*time.Minute))
					r = initialAutomaticReview(t, s, app, review.ID)
					if r.AutomaticBlockSequence != 3 || r.AutomaticBlockNotification.EventID == first {
						t.Fatal("repeated pause reused old event identity")
					}
					count = 3
				}
				if _, err := s.(AppWebhookEventOutboxStore).DrainAppWebhookEventOutbox(ctx, 32); err != nil {
					t.Fatal(err)
				}
				rows, err := s.ClaimDueAppWebhookDeliveries(ctx, 32, now.Add(5*time.Minute))
				if err != nil {
					t.Fatal(err)
				}
				captured := 0
				for _, row := range rows {
					if row.Event != AppWebhookEventRecoveryQueueBlocked {
						continue
					}
					captured++
					var payload api.WorkflowRecoveryQueueBlockedWebhookPayload
					if err := json.Unmarshal(row.Payload, &payload); err != nil {
						t.Fatal(err)
					}
					if row.WebhookID != hookID || payload.InitialFailureID != r.Failure.ID || payload.ReviewID != r.ID || payload.ChildJobID != job || payload.ReviewStatusURL != "/v1/apps/"+app.Slug+"/initial-recovery-reviews/"+r.ID || payload.JobURL == "" || payload.OperatorAction == "" {
						t.Fatalf("payload or recipients %+v", payload)
					}
					if payload.Reason == "blocker_cleared" && payload.State != "recovered" {
						t.Fatal(payload)
					}
					if payload.Reason != "blocker_cleared" && payload.State != "blocked" {
						t.Fatal(payload)
					}
				}
				if captured != count {
					t.Fatalf("deliveries=%d want=%d", captured, count)
				}
			})
		})
	}
}

func TestWorkflowInitialAutomaticAttentionReasons(t *testing.T) {
	for _, reason := range []string{"evidence_unknown", "terminal_outcome_not_saved", "execution_quota", "job_paused"} {
		t.Run(reason, func(t *testing.T) {
			r := api.WorkflowInitialRecoveryReview{ID: "review", Policy: api.WorkflowRecoveryPolicy{Mode: "automatic_initial"}}
			now := time.Now().UTC()
			if p := initialAutomaticBlockTransition(&r, reason, now); p == nil || p.State != "blocked" {
				t.Fatalf("missing %s alert", reason)
			}
			if p := initialAutomaticBlockTransition(&r, reason, now.Add(time.Minute)); p != nil {
				t.Fatal("duplicate alert")
			}
			if p := initialAutomaticBlockTransition(&r, "execution_cooldown", now); p != nil || r.AutomaticBlockReason != reason {
				t.Fatal("normal wait cleared unknown evidence")
			}
			if p := initialAutomaticBlockTransition(&r, "child_created", now); p == nil || p.State != "recovered" || p.PreviousReason != reason {
				t.Fatal("missing clear alert")
			}
		})
	}
}

func TestWorkflowInitialAutomaticQuotaAttention(t *testing.T) {
	workflowScheduleStores(t, func(t *testing.T, s Store) {
		ctx := context.Background()
		run, app, review, _ := seedInitialAutomatic(t, s)
		initialChangeFillAccountQuota(t, s, app, run)
		hook, err := s.CreateAppWebhook(ctx, AppWebhook{AccountID: app.AccountID, AppID: app.ID, TargetURL: "https://attention.example.test/quota", SecretSealed: []byte("sealed"), Enabled: true, EventFilter: []string{string(AppWebhookEventRecoveryQueueBlocked)}})
		if err != nil {
			t.Fatal(err)
		}
		now := time.Now().UTC().Truncate(time.Microsecond)
		pollInitialAutomatic(t, s, now)
		r := initialAutomaticReview(t, s, app, review.ID)
		if r.JobID != "" || r.AutomaticBlockReason != "execution_quota" || r.AutomaticBlockSequence != 1 || r.AutomaticBlockNotification == nil || r.AutomaticBlockNotification.Status != "queued" {
			t.Fatalf("quota attention %+v", r)
		}
		pollInitialAutomatic(t, s, now.Add(time.Minute))
		if r = initialAutomaticReview(t, s, app, review.ID); r.AutomaticBlockSequence != 1 {
			t.Fatal("quota replay")
		}
		if _, err = s.(AppWebhookEventOutboxStore).DrainAppWebhookEventOutbox(ctx, 32); err != nil {
			t.Fatal(err)
		}
		rows, err := s.ClaimDueAppWebhookDeliveries(ctx, 32, now.Add(2*time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		count := 0
		for _, row := range rows {
			if row.Event != AppWebhookEventRecoveryQueueBlocked {
				continue
			}
			count++
			var p api.WorkflowRecoveryQueueBlockedWebhookPayload
			if json.Unmarshal(row.Payload, &p) != nil || row.WebhookID != hook.ID || p.Reason != "execution_quota" || p.State != "blocked" || p.ChildJobID != "" || p.JobURL != "" || p.InitialFailureID != r.Failure.ID {
				t.Fatalf("quota payload %s", row.Payload)
			}
		}
		if count != 1 {
			t.Fatalf("quota deliveries %d", count)
		}
	})
}
