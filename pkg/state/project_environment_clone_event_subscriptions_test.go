package state

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestValidateCloneEventSubscriptionPublication(t *testing.T) {
	captured := projectCloneEventSubscription{Source: "storage", Type: "object.created", Filter: json.RawMessage(`{}`), Enabled: true}
	record := projectCloneWorkloadRecord{snapshot: projectCloneWorkloadSnapshot{EventSubscriptions: &projectCloneEventSubscriptions{AppID: "a",
		Subscriptions: []projectCloneEventSubscription{captured}}}}
	record.AppID, record.WorkloadSlug = "a", "web"
	if err := validateCloneEventSubscriptionPublication(record, []projectCloneEventSubscription{captured}, []bool{false}); err != nil {
		t.Fatalf("matching paused copy: %v", err)
	}
	if err := validateCloneEventSubscriptionPublication(record, []projectCloneEventSubscription{captured}, []bool{true}); !errors.Is(err, ErrConflict) {
		t.Fatalf("an enabled copy published: %v", err)
	}
	changed := captured
	changed.AcknowledgmentLeaseSeconds = 60
	if err := validateCloneEventSubscriptionPublication(record, []projectCloneEventSubscription{changed}, []bool{false}); !errors.Is(err, ErrConflict) {
		t.Fatalf("a changed copy published: %v", err)
	}
	if err := validateCloneEventSubscriptionPublication(record, nil, nil); !errors.Is(err, ErrConflict) {
		t.Fatalf("a missing copy published: %v", err)
	}
}

// A resume enables only the subscriptions a clone paused in the stage, and
// refuses, changing nothing, when that would pass the plan's caps
// (ADR-20261104120000 amendment 2026-11-14).
func TestMemClonePausedEventSubscriptionsResume(t *testing.T) {
	ctx := context.Background()
	seed := func() *MemStore {
		m := NewMemStore()
		m.apps["app"] = App{ID: "app", AccountID: "acct", ProjectID: "proj"}
		m.projectEnvironments["env"] = ProjectEnvironment{ID: "env", AccountID: "acct", ProjectID: "proj", Slug: "stage"}
		m.eventSubscriptions = map[string]EventSubscription{
			"paused": {ID: "paused", AccountID: "acct", AppID: "app", DeploymentScope: "stage", EnvironmentID: "env", ClonePaused: true},
			"off":    {ID: "off", AccountID: "acct", AppID: "app", DeploymentScope: "stage", EnvironmentID: "env"},
			"prod":   {ID: "prod", AccountID: "acct", AppID: "app", Enabled: true},
		}
		return m
	}
	roomy := api.Limits{EventSubscriptionsPerApp: 5, EventSubscriptionsPerAccount: 10}
	m := seed()
	resumed, err := m.ResumeEnvironmentClonePausedEventSubscriptions(ctx, "acct", "proj", "stage", roomy)
	if err != nil || len(resumed) != 1 || resumed[0].ID != "paused" {
		t.Fatalf("resumed = %+v, %v", resumed, err)
	}
	if s := m.eventSubscriptions["paused"]; !s.Enabled || s.ClonePaused {
		t.Fatalf("resumed subscription = %+v", s)
	}
	if m.eventSubscriptions["off"].Enabled {
		t.Fatal("a resume enabled a subscription the clone did not pause")
	}
	if again, err := m.ResumeEnvironmentClonePausedEventSubscriptions(ctx, "acct", "proj", "stage", roomy); err != nil || len(again) != 0 {
		t.Fatalf("a second resume = %+v, %v", again, err)
	}

	m = seed()
	_, err = m.ResumeEnvironmentClonePausedEventSubscriptions(ctx, "acct", "proj", "stage", api.Limits{EventSubscriptionsPerApp: 1, EventSubscriptionsPerAccount: 10})
	var quota *EventSubscriptionQuotaError
	if !errors.As(err, &quota) || quota.Scope != api.PlanQuotaScopeApp || quota.Limit != 1 || quota.Observed != 2 {
		t.Fatalf("over-cap resume = %v", err)
	}
	if s := m.eventSubscriptions["paused"]; s.Enabled || !s.ClonePaused {
		t.Fatalf("a refused resume changed the subscription: %+v", s)
	}

	if _, err := m.ResumeEnvironmentClonePausedEventSubscriptions(ctx, "acct", "proj", "production", roomy); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("production resume = %v", err)
	}
	if _, err := m.ResumeEnvironmentClonePausedEventSubscriptions(ctx, "acct", "proj", "nowhere", roomy); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing environment resume = %v", err)
	}
	m.eventSubscriptions["off"] = EventSubscription{ID: "off", AccountID: "acct", AppID: "app", EnvironmentID: "env", ClonePaused: true}
	if err := m.SetEventSubscriptionEnabled(ctx, "off", "acct", "app", true); err != nil || m.eventSubscriptions["off"].ClonePaused {
		t.Fatalf("an explicit enable kept the clone pause: %+v, %v", m.eventSubscriptions["off"], err)
	}
}
