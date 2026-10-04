package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Private consumers cannot be paused through the public trigger API. Model
// a paused consumer observation without transferring its mutation authority.
type pausedPromotionConsumerStore struct {
	*state.MemStore
	bindingID string
}

func (s *pausedPromotionConsumerStore) ListQueueBindingConsumersForApp(ctx context.Context, accountID, appID string) ([]state.QueueBindingConsumerInventory, error) {
	rows, err := s.MemStore.ListQueueBindingConsumersForApp(ctx, accountID, appID)
	for i := range rows {
		if rows[i].BindingID == s.bindingID {
			enabled := false
			rows[i].ConsumerEnabled = &enabled
		}
	}
	return rows, err
}

func TestBindingPromotionQueueReadinessAndRecovery(t *testing.T) {
	for _, tc := range []struct {
		name, code string
		mutate     func(*testing.T, testEnv, state.App, state.QueueBinding, string)
	}{
		{"missing", "queue_consumer_missing", func(t *testing.T, _ testEnv, _ state.App, _ state.QueueBinding, id string) {
			if id != "" {
				t.Fatalf("unprovisioned binding has consumer %s", id)
			}
		}},
		{"paused", "queue_consumer_paused", func(_ *testing.T, e testEnv, _ state.App, binding state.QueueBinding, _ string) {
			e.s.store = &pausedPromotionConsumerStore{MemStore: e.store, bindingID: binding.ID}
		}},
		{"degraded", "queue_consumer_degraded", func(t *testing.T, e testEnv, _ state.App, _ state.QueueBinding, id string) {
			if err := e.store.RecordTriggerConsumerHealth(context.Background(), id, state.TriggerConsumerHealthObservation{LastPollAt: time.Now().UTC(), Error: "PRIVATE_QUEUE_ERROR"}); err != nil {
				t.Fatal(err)
			}
		}},
		{"stale", "queue_consumer_stale", func(t *testing.T, e testEnv, _ state.App, _ state.QueueBinding, id string) {
			if err := e.store.RecordTriggerConsumerHealth(context.Background(), id, state.TriggerConsumerHealthObservation{LastPollAt: time.Now().Add(-time.Minute), Success: true}); err != nil {
				t.Fatal(err)
			}
		}},
		{"future", "queue_consumer_time_future", func(t *testing.T, e testEnv, _ state.App, _ state.QueueBinding, id string) {
			if err := e.store.RecordTriggerConsumerHealth(context.Background(), id, state.TriggerConsumerHealthObservation{LastPollAt: time.Now().Add(time.Minute), Success: true}); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, app, serving, candidate := promotionFixture(t)
			completePromotionProbe(t, e, app, candidate, passedPostgresVerification)
			var binding state.QueueBinding
			if tc.name == "missing" {
				// Retain a legacy binding whose private consumer is not yet
				// provisioned; public deletion of an owned consumer is rejected.
				binding = seedInventoryQueue(t, e, app, "events", false)
			} else {
				binding = seedHealthyPromotionQueue(t, e, app)
			}
			id, err := queueBindingTriggerID(context.Background(), e.store, app.ID, binding.ID)
			if err != nil {
				t.Fatal(err)
			}
			tc.mutate(t, e, app, binding, id)
			path := "/v1/deployments/" + candidate.ID + "/promote"
			r := e.do(t, http.MethodPost, path, api.BindingPromotionRequest{AllowUnsupported: true}, nil)
			if r.Code != 409 || !strings.Contains(r.Body.String(), tc.code) || strings.Contains(r.Body.String(), "PRIVATE_QUEUE_ERROR") {
				t.Fatalf("waiver hid queue failure or exposed error: %d %s", r.Code, r.Body.String())
			}
			assertPromotionWeights(t, e, serving, candidate, 0)
			e.s.store = e.store
			if _, err := e.store.UpdateQueueBindingWithConsumer(context.Background(), e.acct.ID, app.ID, binding.ID, state.UpdateQueueBindingParams{}); err != nil {
				t.Fatal(err)
			}
			id, err = queueBindingTriggerID(context.Background(), e.store, app.ID, binding.ID)
			if err != nil {
				t.Fatal(err)
			}
			if err := e.store.RecordTriggerConsumerHealth(context.Background(), id, state.TriggerConsumerHealthObservation{LastPollAt: time.Now().UTC(), Success: true}); err != nil {
				t.Fatal(err)
			}
			r = e.do(t, http.MethodPost, path, api.BindingPromotionRequest{AllowUnsupported: true}, nil)
			if r.Code != 200 || !strings.Contains(r.Body.String(), `"coverage":"partial"`) {
				t.Fatalf("recovery: %d %s", r.Code, r.Body.String())
			}
			assertPromotionWeights(t, e, serving, candidate, 100)
		})
	}
}

func TestBindingPromotionQueueUnobservedDisabledAndExternalConsumers(t *testing.T) {
	for _, mode := range []string{"unobserved", "disabled", "pull"} {
		t.Run(mode, func(t *testing.T) {
			e, app, serving, candidate := promotionFixture(t)
			completePromotionProbe(t, e, app, candidate, passedPostgresVerification)
			binding := seedInventoryQueue(t, e, app, "events", false)
			switch mode {
			case "unobserved":
				if _, err := e.store.UpdateQueueBindingWithConsumer(context.Background(), e.acct.ID, app.ID, binding.ID, state.UpdateQueueBindingParams{}); err != nil {
					t.Fatal(err)
				}
			case "disabled":
				disabled := false
				if _, err := e.store.UpdateQueueBindingWithConsumer(context.Background(), e.acct.ID, app.ID, binding.ID, state.UpdateQueueBindingParams{Enabled: &disabled}); err != nil {
					t.Fatal(err)
				}
			case "pull":
				pull := "pull"
				if _, err := e.store.UpdateQueueBindingWithConsumer(context.Background(), e.acct.ID, app.ID, binding.ID, state.UpdateQueueBindingParams{Mode: &pull}); err != nil {
					t.Fatal(err)
				}
			}
			r := e.do(t, http.MethodPost, "/v1/deployments/"+candidate.ID+"/promote", api.BindingPromotionRequest{AllowUnsupported: true}, nil)
			if mode == "unobserved" {
				if r.Code != 409 || !strings.Contains(r.Body.String(), "queue_consumer_unobserved") {
					t.Fatalf("unobserved consumer accepted: %d %s", r.Code, r.Body.String())
				}
				assertPromotionWeights(t, e, serving, candidate, 0)
			} else {
				coverage := "partial"
				if mode == "disabled" {
					coverage = "complete"
				}
				if r.Code != 200 || !strings.Contains(r.Body.String(), `"coverage":"`+coverage+`"`) {
					t.Fatalf("%s queue: %d %s", mode, r.Code, r.Body.String())
				}
				assertPromotionWeights(t, e, serving, candidate, 100)
			}
		})
	}
}

type expiringQueuePromotionStore struct{ *state.MemStore }

func (s *expiringQueuePromotionStore) PromoteDeploymentWithBindings(ctx context.Context, id string, fence state.BindingPromotionFence, serving string) (state.BindingPromotionResult, error) {
	// Simulate waiting for the traffic lock without changing any inventory row.
	select {
	case <-ctx.Done():
		return state.BindingPromotionResult{}, ctx.Err()
	case <-time.After(time.Until(fence.ValidUntil) + time.Millisecond):
	}
	return s.MemStore.PromoteDeploymentWithBindings(ctx, id, fence, serving)
}

func TestBindingPromotionQueuePollExpiresAtTrafficWrite(t *testing.T) {
	e, app, serving, candidate := promotionFixture(t)
	completePromotionProbe(t, e, app, candidate, passedPostgresVerification)
	binding := seedHealthyPromotionQueue(t, e, app)
	id, err := queueBindingTriggerID(context.Background(), e.store, app.ID, binding.ID)
	if err != nil {
		t.Fatal(err)
	}
	polled := time.Now().Add(-api.QueueConsumerMaxPollAge + 2*time.Second)
	if err := e.store.RecordTriggerConsumerHealth(context.Background(), id, state.TriggerConsumerHealthObservation{LastPollAt: polled, Success: true}); err != nil {
		t.Fatal(err)
	}
	e.s.store = &expiringQueuePromotionStore{e.store}
	r := e.do(t, http.MethodPost, "/v1/deployments/"+candidate.ID+"/promote", api.BindingPromotionRequest{AllowUnsupported: true, MaxVerificationAge: "1h"}, nil)
	var problem api.Problem
	if err := json.Unmarshal(r.Body.Bytes(), &problem); err != nil {
		t.Fatal(err)
	}
	if r.Code != 409 || problem.Code != "bindings_check_changed" || problem.BindingsCheck == nil || problem.BindingsCheck.Passed || len(problem.BindingsCheck.Blockers) != 1 {
		t.Fatalf("expired poll authorized traffic write: %d %s", r.Code, r.Body.String())
	}
	finding := problem.BindingsCheck.Blockers[0]
	if finding.Code != "queue_consumer_stale" || finding.Type != "queue" || finding.Binding != binding.Name || !strings.Contains(finding.Message, "scheduler") {
		t.Fatalf("incorrect expiry recovery: %+v", finding)
	}
	assertPromotionWeights(t, e, serving, candidate, 0)
}

func TestBindingPromotionRejectsQueueFailureAfterPassedCheck(t *testing.T) {
	e, app, serving, candidate := promotionFixture(t)
	completePromotionProbe(t, e, app, candidate, passedPostgresVerification)
	binding := seedHealthyPromotionQueue(t, e, app)
	id, err := queueBindingTriggerID(context.Background(), e.store, app.ID, binding.ID)
	if err != nil {
		t.Fatal(err)
	}
	e.s.store = &changingBindingPromotionStore{MemStore: e.store, before: func() {
		if err := e.store.RecordTriggerConsumerHealth(context.Background(), id, state.TriggerConsumerHealthObservation{LastPollAt: time.Now(), Error: "PRIVATE_QUEUE_ERROR"}); err != nil {
			t.Fatal(err)
		}
	}}
	r := e.do(t, http.MethodPost, "/v1/deployments/"+candidate.ID+"/promote", api.BindingPromotionRequest{AllowUnsupported: true}, nil)
	if r.Code != 409 || !strings.Contains(r.Body.String(), "promotion_observations_changed") || strings.Contains(r.Body.String(), "PRIVATE_QUEUE_ERROR") {
		t.Fatalf("consumer failure raced traffic write: %d %s", r.Code, r.Body.String())
	}
	assertPromotionWeights(t, e, serving, candidate, 0)
}

func TestBindingPromotionDeadlineUsesEarliestRequiredObservation(t *testing.T) {
	now := time.Now().UTC()
	proofAt, pollAt := now.Add(-time.Minute), now.Add(-time.Second)
	report := api.BindingCheckReport{DeploymentID: "candidate", Scope: "default", Bindings: []api.BindingCheckBindingResult{
		{Type: api.BindingTypePostgres, Status: "passed", CheckedAt: &proofAt},
		{Type: api.BindingTypeQueue, Status: "unsupported"},
	}}
	inventory := api.AppBindingInventory{Bindings: []api.AppBindingInventoryItem{{Type: api.BindingTypeQueue, Name: "events", Binding: "worker", State: "enabled", Access: "push", ObservedAt: &pollAt}}}
	deadline, finding := bindingPromotionDeadline(report, inventory, time.Hour)
	if !deadline.Equal(pollAt.Add(api.QueueConsumerMaxPollAge)) || finding.Code != "queue_consumer_stale" || finding.Binding != "worker" {
		t.Fatalf("queue poll did not bound promotion: %v %+v", deadline, finding)
	}
	deadline, finding = bindingPromotionDeadline(report, inventory, time.Minute)
	if !deadline.Equal(proofAt.Add(time.Minute)) || finding.Code != "verification_expired" {
		t.Fatalf("earlier proof ignored: %v %+v", deadline, finding)
	}
	for _, mutate := range []func(*api.AppBindingInventoryItem){
		func(b *api.AppBindingInventoryItem) { b.Access = "pull" },
		func(b *api.AppBindingInventoryItem) { b.State = "disabled" },
	} {
		item := inventory.Bindings[0]
		mutate(&item)
		deadline, finding = bindingPromotionDeadline(report, api.AppBindingInventory{Bindings: []api.AppBindingInventoryItem{item}}, time.Hour)
		if !deadline.Equal(proofAt.Add(time.Hour)) || finding.Code != "verification_expired" {
			t.Fatalf("inactive/external consumer bounded promotion: %v %+v", deadline, finding)
		}
	}
	report.Bindings = report.Bindings[1:]
	deadline, finding = bindingPromotionDeadline(report, inventory, time.Hour)
	if !deadline.Equal(pollAt.Add(api.QueueConsumerMaxPollAge)) || finding.Code != "queue_consumer_stale" {
		t.Fatalf("queue-only gate has no expiry: %v %+v", deadline, finding)
	}
}
