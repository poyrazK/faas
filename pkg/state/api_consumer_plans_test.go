package state

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

// adr: 953
func TestDecidePlanAdmissionWindows(t *testing.T) {
	now := time.Date(2026, 9, 30, 23, 59, 30, 0, time.UTC)
	policy := APIConsumerPlanPolicy{MaxRequestsPerMinute: 2, MaxUnitsPerMonth: 30}
	var counter planAdmissionCounter
	var decision APIConsumerPlanDecision
	for i := range 2 {
		if counter, decision = decidePlanAdmission(counter, policy, 10, now); !decision.Allowed {
			t.Fatalf("request %d denied: %+v", i, decision)
		}
	}
	if _, decision = decidePlanAdmission(counter, policy, 1, now); decision.Allowed || decision.Scope != "minute" || decision.RetryAfterSeconds != 30 {
		t.Fatalf("third request in the minute = %+v, want minute denial retrying in 30s", decision)
	}
	next := now.Add(31 * time.Second) // a new minute and a new month
	if counter, decision = decidePlanAdmission(counter, policy, 10, next); !decision.Allowed || counter.MonthUsed != 10 || counter.MinuteUsed != 1 {
		t.Fatalf("new month = %+v %+v, want both windows reset", counter, decision)
	}
	counter.MinuteUsed = 0
	counter.MonthUsed = 25
	if _, decision = decidePlanAdmission(counter, policy, 10, next); decision.Allowed || decision.Scope != "month" || decision.Observed != 25 {
		t.Fatalf("over monthly cap = %+v, want month denial", decision)
	}
	if _, decision = decidePlanAdmission(planAdmissionCounter{}, APIConsumerPlanPolicy{}, 1000, next); !decision.Allowed {
		t.Fatal("zero limits must be unlimited")
	}
}

func TestMemConsumerPlansAssignmentsAndPolicy(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	accountID, appID, consumerID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	plan, err := m.CreateAPIConsumerPlan(ctx, APIConsumerPlan{AccountID: accountID, AppID: appID, Name: "pro", MaxUnitsPerMonth: 100})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.CreateAPIConsumerPlan(ctx, APIConsumerPlan{AccountID: accountID, AppID: appID, Name: "pro"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate name err=%v, want ErrConflict", err)
	}
	if _, err := m.CreateAPIConsumerPlan(ctx, APIConsumerPlan{AccountID: accountID, AppID: appID, Name: "Bad Name"}); err == nil {
		t.Fatal("invalid name accepted")
	}
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	if _, err := m.CreateAPIConsumerRateCardVersion(ctx, APIConsumerRateCardInput{AccountID: accountID, AppID: appID, Currency: "EUR",
		PriceMillicentsPerUnit: 5, PlanID: plan.ID, RouteWeights: map[string]int64{"POST /generate": 20}, EffectiveFrom: start}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.AssignAPIConsumerPlan(ctx, APIConsumerPlanAssignment{AccountID: accountID, AppID: appID, ConsumerID: consumerID,
		PlanID: plan.ID, EffectiveFrom: start.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.AssignAPIConsumerPlan(ctx, APIConsumerPlanAssignment{AccountID: accountID, AppID: appID, ConsumerID: consumerID,
		PlanID: uuid.NewString(), EffectiveFrom: start.Add(2 * time.Hour)}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown plan err=%v, want ErrNotFound", err)
	}
	before, err := m.GetAPIConsumerPlanPolicy(ctx, accountID, appID, consumerID, start)
	if err != nil || before.Limited() {
		t.Fatalf("policy before assignment = %+v err=%v, want default (unlimited)", before, err)
	}
	after, err := m.GetAPIConsumerPlanPolicy(ctx, accountID, appID, consumerID, start.Add(90*time.Minute))
	if err != nil || after.PlanID != plan.ID || after.MaxUnitsPerMonth != 100 || after.Units("POST /generate") != 20 || after.Units("GET /x") != 1 {
		t.Fatalf("policy after assignment = %+v err=%v", after, err)
	}
	updated, err := m.UpdateAPIConsumerPlanLimits(ctx, accountID, appID, plan.ID, 60, 0)
	if err != nil || updated.MaxRequestsPerMinute != 60 || updated.MaxUnitsPerMonth != 0 {
		t.Fatalf("updated = %+v err=%v", updated, err)
	}
}
