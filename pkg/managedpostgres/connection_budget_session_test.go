package managedpostgres

import (
	"slices"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestConnectionBudgetModelsSessionBindingsAtRoleLimit(t *testing.T) {
	s := budgetSnapshot()
	s.Capacity.AllocationConnections = 90
	s.Capacity.UsableConnections = s.Capacity.SQL.Ceiling(90)
	s.Bindings = append(s.Bindings, connectionBudgetBinding{AppID: "worker", Scope: "default", Access: "session", State: "ready", Plan: api.PlanPro})
	got := describeConnectionBudget(s, s.CheckedAt)
	want := int64(6 + api.SessionBindingConnectionLimit + api.DataAPIConnectionBudgetControlHeadroom)
	if got.Inventory.SessionBindings != 1 || got.Inventory.OtherBindings != 0 || got.Demand.SessionConnectionCeiling != api.SessionBindingConnectionLimit ||
		got.Demand.ModeledPeakConnections != want || slices.Contains(got.Reasons, "client_pool_sizes_unknown") || got.State != "within_budget" {
		t.Fatalf("session binding not modeled at its role limit: %+v", got)
	}
	s.Bindings[1].Rotation = true
	if got = describeConnectionBudget(s, s.CheckedAt); got.Demand.SessionConnectionCeiling != 2*api.SessionBindingConnectionLimit {
		t.Fatalf("a rotating session binding holds two roles: %+v", got.Demand)
	}
	s.Capacity.AllocationConnections = 50
	s.Capacity.UsableConnections = s.Capacity.SQL.Ceiling(50)
	if got = describeConnectionBudget(s, s.CheckedAt); got.State != "over_budget" {
		t.Fatalf("session ceiling must count against capacity: %+v", got)
	}
}
