package state

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// adr: 1284
func TestRecoveryOverviewManualRetry(t *testing.T) {
	workflowScheduleStores(t, func(t *testing.T, s Store) {
		ctx := context.Background()
		r, app, _, _ := seedRecoveryNotificationReview(t, s, true)
		// The fixture evaluates a future worker clock; retain an already observed review here.
		r.ObservedAt = time.Now().UTC().Add(-time.Second).Truncate(time.Microsecond)
		r.Plan.CreatedAt = r.ObservedAt
		save := func() {
			t.Helper()
			raw, e := json.Marshal(r)
			if e != nil {
				t.Fatal(e)
			}
			if m, ok := s.(*MemStore); ok {
				m.mu.Lock()
				m.recoveryPolicies[canonicalMemUUID(app.ID)].Reviews[r.ID] = raw
				m.mu.Unlock()
			} else {
				_, e = s.(*PgStore).pool.Exec(ctx, "UPDATE workflow_recovery_policy_reviews SET review=$2::jsonb,observed_at=$3,review_decision=$4::jsonb WHERE id=$1::uuid", r.ID, raw, r.ObservedAt, nil)
				if e != nil {
					t.Fatal(e)
				}
			}
		}
		save()
		read := func() *api.WorkflowRecoveryOverview {
			t.Helper()
			h, e := s.(WorkflowRecoveryHealthOverviewStore).GetWorkflowRecoveryPolicyHealth(ctx, app.AccountID, app.ID, 300)
			if e != nil {
				t.Fatal(e)
			}
			return h.Overview
		}
		o := read()
		if o.ManualRetryAwaitingReview != 1 || o.Status != "waiting" || len(o.Issues) != 1 || o.Issues[0].Scope != "manual_retry" || o.Issues[0].JobID != r.JobID || o.Issues[0].InspectionURL != "/v1/apps/"+app.Slug+"/recovery-policy/reviews/"+r.ID+"/status" {
			t.Fatalf("undecided %+v", o)
		}
		r.Policy.Mode = "automatic_retry"
		save()
		if o = read(); o.ManualRetryAwaitingReview != 0 {
			t.Fatalf("automatic review %+v", o)
		}
		r.Policy.Mode = "preview"
		r.ObservedAt = time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)
		save()
		if o = read(); o.ManualRetryAwaitingReview != 0 {
			t.Fatalf("future review %+v", o)
		}
		r.ObservedAt = time.Now().UTC().Add(-time.Second).Truncate(time.Microsecond)
		save()
		// Explicitly suppressed stale reviews are not offered as awaiting inspection.
		suppress := func(on bool) {
			t.Helper()
			if m, ok := s.(*MemStore); ok {
				m.mu.Lock()
				p := m.recoveryPolicies[canonicalMemUUID(app.ID)]
				if p.Notifications == nil {
					p.Notifications = map[string]*memRecoveryReviewNotification{}
				}
				if p.Notifications[r.ID] == nil {
					p.Notifications[r.ID] = &memRecoveryReviewNotification{}
				}
				if on {
					m.recoveryPolicies[canonicalMemUUID(app.ID)].Notifications[r.ID].Receipt = newRecoveryReviewNotification(r.ID, "stale", false, []string{}, time.Now().UTC())
				} else {
					m.recoveryPolicies[canonicalMemUUID(app.ID)].Notifications[r.ID].Receipt = nil
				}
				m.mu.Unlock()
			} else {
				var raw []byte
				if on {
					var e error
					raw, e = json.Marshal(newRecoveryReviewNotification(r.ID, "stale", false, []string{}, time.Now().UTC()))
					if e != nil {
						t.Fatal(e)
					}
				}
				_, e := s.(*PgStore).pool.Exec(ctx, "UPDATE workflow_recovery_review_notifications SET receipt=$2::jsonb WHERE review_id=$1::uuid", r.ID, raw)
				if e != nil {
					t.Fatal(e)
				}
			}
		}
		suppress(true)
		if o = read(); o.ManualRetryAwaitingReview != 0 {
			t.Fatalf("suppressed %+v", o)
		}
		suppress(false)
		p, e := s.(WorkflowRecoveryPolicyStore).GetWorkflowRecoveryPolicy(ctx, app.AccountID, app.ID)
		if e != nil {
			t.Fatal(e)
		}
		policy := p.Policy
		policy.Enabled = false
		if _, e = s.(WorkflowRecoveryPolicyStore).SetWorkflowRecoveryPolicy(ctx, app.AccountID, app.ID, api.SetWorkflowRecoveryPolicyRequest{Policy: policy, ExpectedVersion: p.Version}); e != nil {
			t.Fatal(e)
		}
		if o = read(); o.ManualRetryAwaitingReview != 0 {
			t.Fatalf("policy changed %+v", o)
		}
		if _, e = s.(WorkflowRecoveryReviewInspector).DecideWorkflowRecoveryPolicyReview(ctx, app.AccountID, app.ID, r.ID, api.WorkflowRecoveryReviewDecisionRequest{Decision: "approved", Reason: "reviewed"}); e != nil {
			t.Fatal(e)
		}
		o = read()
		if o.ManualRetryUnresolved != 1 || o.ManualRetryAwaitingReview != 0 || o.Status != "waiting" || o.Issues[0].Reason != "awaiting_completion_observation" {
			t.Fatalf("approved after policy change %+v", o)
		}
		completion := newRecoveryReviewCompletion(r.ID, api.WorkflowRecoveryReviewStatus{ReviewID: r.ID, Complete: true, Outcome: "succeeded"}, nil, time.Now().UTC())
		if m, ok := s.(*MemStore); ok {
			m.mu.Lock()
			m.recoveryPolicies[canonicalMemUUID(app.ID)].Notifications[r.ID].Completion = completion
			m.mu.Unlock()
		} else {
			raw, e := json.Marshal(completion)
			if e != nil {
				t.Fatal(e)
			}
			e = sqlc.New().WorkflowRecoveryReviewCompletionCapture(ctx, s.(*PgStore).pool, sqlc.WorkflowRecoveryReviewCompletionCaptureParams{ReviewID: mustPgUUID(r.ID), Completion: raw})
			if e != nil {
				t.Fatal(e)
			}
		}
		if o = read(); o.ManualRetryUnresolved != 0 || o.Status != "inactive" {
			t.Fatalf("completed %+v", o)
		}
	})
}
