package state

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 849
func TestCrossedUsageAlertThresholds(t *testing.T) {
	cases := []struct {
		name                 string
		limit, before, after int64
		want                 []int32
	}{
		{"reaches 80 exactly", 100, 79, 80, []int32{80}},
		{"jumps over both", 100, 10, 100, []int32{80, 100}},
		{"already past 80", 100, 80, 90, nil},
		{"rounds up for odd limits", 7, 5, 6, []int32{80}}, // ceil(5.6) = 6
		{"huge limit does not overflow", 1 << 62, 0, 1 << 62, []int32{80, 100}},
		{"no units consumed", 100, 50, 50, nil},
	}
	for _, tc := range cases {
		if got := crossedUsageAlertThresholds([]int32{80, 100}, tc.limit, tc.before, tc.after); !slices.Equal(got, tc.want) {
			t.Errorf("%s: crossed = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// adr: 849
func TestValidateAPIConsumerPlanAlerts(t *testing.T) {
	cases := []struct {
		name       string
		thresholds []int32
		limit      int64
		ok         bool
	}{
		{"none", nil, 0, true},
		{"valid", []int32{100, 50, 80}, 1000, true},
		{"needs a monthly limit", []int32{80}, 0, false},
		{"out of range", []int32{0}, 1000, false},
		{"over 100", []int32{101}, 1000, false},
		{"duplicates", []int32{80, 80}, 1000, false},
		{"too many", []int32{10, 20, 30, 40, 50, 60}, 1000, false},
	}
	for _, tc := range cases {
		if err := validateAPIConsumerPlanAlerts(tc.thresholds, tc.limit); (err == nil) != tc.ok {
			t.Errorf("%s: err = %v, want ok=%v", tc.name, err, tc.ok)
		}
	}
}

// adr: 849
func TestMemConsumerUsageAlertsFireOncePerMonthAndOnDenial(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	accountID, appID := uuid.NewString(), uuid.NewString()
	consumer, err := m.CreateAPIConsumer(ctx, accountID, appID, "cust-1", "Customer One")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.CreateAppWebhook(ctx, AppWebhook{AccountID: accountID, AppID: appID, TargetURL: "https://hooks.example.com/a", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	plan, err := m.CreateAPIConsumerPlan(ctx, APIConsumerPlan{AccountID: accountID, AppID: appID, Name: "starter",
		MaxUnitsPerMonth: 10, AlertThresholdsPercent: []int32{100, 50}})
	if err != nil || !slices.Equal(plan.AlertThresholdsPercent, []int32{50, 100}) {
		t.Fatalf("plan = %+v err=%v, want sorted thresholds", plan, err)
	}
	policy := APIConsumerPlanPolicy{PlanID: plan.ID, AppID: appID, MaxUnitsPerMonth: 10, AlertThresholdsPercent: plan.AlertThresholdsPercent}
	admit := func(units int64) APIConsumerPlanDecision {
		t.Helper()
		decision, err := m.AdmitAPIConsumerPlanRequest(ctx, accountID, consumer.ID, policy, units)
		if err != nil {
			t.Fatal(err)
		}
		return decision
	}
	for range 5 {
		admit(1)
	}
	alerts, _ := m.ListAPIConsumerUsageAlerts(ctx, accountID, appID, consumer.ID, 0)
	if len(alerts) != 1 || alerts[0].ThresholdPercent != 50 || alerts[0].UsedUnits != 5 || alerts[0].LimitUnits != 10 {
		t.Fatalf("alerts after 5 units = %+v, want one 50%% alert", alerts)
	}
	admit(1)
	// 6 used; a 5-unit request is denied before reaching the limit, which
	// still records the 100% alert once.
	if d := admit(5); d.Allowed || d.Scope != "month" {
		t.Fatalf("decision = %+v, want month denial", d)
	}
	admit(5)
	alerts, _ = m.ListAPIConsumerUsageAlerts(ctx, accountID, appID, consumer.ID, 0)
	if len(alerts) != 2 || alerts[0].ThresholdPercent != 100 || alerts[0].UsedUnits != 6 {
		t.Fatalf("alerts after denial = %+v, want 100%% alert recorded once", alerts)
	}
	events := 0
	for _, event := range m.appWebhookEventOutbox {
		if event.Event != AppWebhookEventConsumerUsageThreshold {
			continue
		}
		var payload api.APIConsumerUsageThresholdWebhookPayload
		if err := json.Unmarshal(event.Payload, &payload); err != nil || payload.ExternalRef != "cust-1" || payload.AlertID != event.SourceID {
			t.Fatalf("payload = %+v err=%v", payload, err)
		}
		events++
	}
	if events != 2 {
		t.Fatalf("webhook events = %d, want 2", events)
	}
}

// adr: 849
func TestMemUpdateConsumerPlanAlerts(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	accountID, appID := uuid.NewString(), uuid.NewString()
	plan, err := m.CreateAPIConsumerPlan(ctx, APIConsumerPlan{AccountID: accountID, AppID: appID, Name: "pro",
		MaxUnitsPerMonth: 100, AlertThresholdsPercent: []int32{80}})
	if err != nil {
		t.Fatal(err)
	}
	kept, err := m.UpdateAPIConsumerPlanLimits(ctx, accountID, appID, plan.ID, 0, 200, nil)
	if err != nil || !slices.Equal(kept.AlertThresholdsPercent, []int32{80}) {
		t.Fatalf("nil thresholds = %+v err=%v, want kept", kept, err)
	}
	if _, err := m.UpdateAPIConsumerPlanLimits(ctx, accountID, appID, plan.ID, 0, 0, nil); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("removing the monthly limit under alerts err=%v, want ErrInvalidArgument", err)
	}
	cleared, err := m.UpdateAPIConsumerPlanLimits(ctx, accountID, appID, plan.ID, 0, 0, []int32{})
	if err != nil || len(cleared.AlertThresholdsPercent) != 0 {
		t.Fatalf("empty thresholds = %+v err=%v, want cleared", cleared, err)
	}
}
