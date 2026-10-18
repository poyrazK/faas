package managedpostgres

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres/connectioncapacity"
)

func budgetSnapshot() ConnectionBudgetSnapshot {
	now := time.Now().UTC()
	id := uuid.NewString()
	return ConnectionBudgetSnapshot{DatabaseID: id, DatabaseName: "primary", DatabaseState: "ready", Generation: 2, BackendFingerprint: strings.Repeat("a", 64), CheckedAt: now,
		ModelsVerified: true, OtherConsumersCovered: true,
		Capacity: &ConnectionBudgetCapacityEvidence{DatabaseID: id, BackendFingerprint: strings.Repeat("a", 64), Generation: 2, UsableConnections: 20, CheckedAt: now, ExpiresAt: now.Add(api.DataAPIConnectionBudgetMaxAge),
			Source: "native_qualification", SourceSHA256: strings.Repeat("b", 64), AllocationCheckedAt: now, AllocationExpiresAt: now.Add(time.Minute), AllocationConnections: 20,
			SQL: &connectioncapacity.Observation{MaxConnections: 100, SuperuserReserved: 3, DatabaseLimit: -1, RoleLimit: -1}},
		Bindings: []connectionBudgetBinding{{AppID: "api", Scope: "default", Access: "data_api", State: "ready", Plan: api.PlanPro, MaxConcurrency: 3, Residents: 2, Parked: 1, Deployments: 1}}}
}

func TestConnectionBudgetPlansWakeAndLifecycleOverlapWithoutDoubleCounting(t *testing.T) {
	s := budgetSnapshot()
	got := describeConnectionBudget(s, s.CheckedAt)
	if got.State != "within_budget" || got.Enforced || got.Demand.DataAPIResidentEstimate != 4 || got.Demand.DataAPIWakeEstimate != 6 || got.Demand.ModeledPeakConnections != 7 {
		t.Fatal(got)
	}
	s.Bindings[0].Rotation = true
	got = describeConnectionBudget(s, s.CheckedAt)
	if got.Inventory.RotationPending != 1 || got.Inventory.OverlapConsumers != 1 || got.Demand.DataAPIWakeEstimate != 8 {
		t.Fatal(got)
	}
	s.Bindings[0].Residents, s.Bindings[0].Parked, s.Bindings[0].Deployments = 4, 0, 2
	got = describeConnectionBudget(s, s.CheckedAt)
	if got.Demand.DataAPIWakeEstimate != 8 {
		t.Fatal("rotation added another copy of the observed replacement", got)
	}
	s.Capacity.UsableConnections = 8
	s.Capacity.AllocationConnections = 8
	if got = describeConnectionBudget(s, s.CheckedAt); got.State != "over_budget" {
		t.Fatal(got)
	}
	s.Capacity.UsableConnections = 9
	s.Capacity.AllocationConnections = 9
	if got = describeConnectionBudget(s, s.CheckedAt); got.State != "within_budget" {
		t.Fatal("exact budget must fit", got)
	}
	other := s.Bindings[0]
	other.Access = "auth"
	s.Bindings = append(s.Bindings, other)
	got = describeConnectionBudget(s, s.CheckedAt)
	if got.Inventory.ResidentInstances != 4 || got.Inventory.AuthConsumers != 1 || got.Demand.AuthWakeEstimate != 16 {
		t.Fatal("physical instances duplicated across access modes", got)
	}
}

func TestConnectionBudgetMissingUnqualifiedAndStaleEvidenceCannotPass(t *testing.T) {
	cases := map[string]func(*ConnectionBudgetSnapshot){
		"capacity_unverified":                   func(s *ConnectionBudgetSnapshot) { s.Capacity = nil },
		"runtime_pool_configuration_unverified": func(s *ConnectionBudgetSnapshot) { s.ModelsVerified = false },
		"external_consumers_unobserved":         func(s *ConnectionBudgetSnapshot) { s.OtherConsumersCovered = false },
		"inventory_stale":                       func(s *ConnectionBudgetSnapshot) { s.CheckedAt = s.CheckedAt.Add(-time.Hour) },
		"database_not_ready":                    func(s *ConnectionBudgetSnapshot) { s.DatabaseState = "updating" },
		"inventory_truncated":                   func(s *ConnectionBudgetSnapshot) { s.Truncated = true },
		"runtime_inventory_unavailable":         func(s *ConnectionBudgetSnapshot) { s.InventoryUnavailable = true },
		"node_observations_stale":               func(s *ConnectionBudgetSnapshot) { s.Bindings[0].StaleNodes = 1 },
		"auxiliary_connections_unmodeled":       func(s *ConnectionBudgetSnapshot) { s.Bindings[0].Tasks = 1 },
		"binding_transition":                    func(s *ConnectionBudgetSnapshot) { s.Bindings[0].State = "retiring" },
		"multiple_runtime_bindings":             func(s *ConnectionBudgetSnapshot) { s.Bindings = append(s.Bindings, s.Bindings[0]) },
		"inventory_invalid":                     func(s *ConnectionBudgetSnapshot) { s.Bindings[0].Residents = -1 },
	}
	for reason, change := range cases {
		t.Run(reason, func(t *testing.T) {
			s := budgetSnapshot()
			now := s.CheckedAt
			change(&s)
			got := describeConnectionBudget(s, now)
			if got.State != "unknown" || !slices.Contains(got.Reasons, reason) {
				t.Fatal(got)
			}
		})
	}
	for _, mode := range []string{"database", "generation", "placement", "expired", "future", "too_old"} {
		t.Run(mode, func(t *testing.T) {
			s := budgetSnapshot()
			switch mode {
			case "database":
				s.Capacity.DatabaseID = uuid.NewString()
			case "generation":
				s.Capacity.Generation++
			case "placement":
				s.Capacity.BackendFingerprint = "private-provider"
			case "expired":
				s.Capacity.ExpiresAt = s.CheckedAt
			case "future":
				s.Capacity.CheckedAt = s.CheckedAt.Add(time.Second)
			case "too_old":
				s.Capacity.CheckedAt = s.CheckedAt.Add(-time.Hour)
			}
			got := describeConnectionBudget(s, s.CheckedAt)
			if got.State != "unknown" || got.Capacity.UsableConnections != nil {
				t.Fatal(got)
			}
		})
	}
}

func TestConnectionBudgetKeepsUnboundedConsumersAndDeduplicatesBindings(t *testing.T) {
	s := budgetSnapshot()
	duplicate := s.Bindings[0]
	duplicate.State = "deleting"
	s.Bindings = append(s.Bindings, duplicate)
	s.Bindings = append(s.Bindings, connectionBudgetBinding{AppID: "migration", Scope: "default", Access: "migration", State: "ready"}, connectionBudgetBinding{AppID: "client", Scope: "default", Access: "read_write", State: "ready"})
	got := describeConnectionBudget(s, s.CheckedAt)
	if got.State != "unknown" || got.Inventory.DataAPIConsumers != 1 || got.Demand.DataAPIResidentEstimate != 4 || got.Demand.MigrationHeadroom != 1 || !slices.Contains(got.Reasons, "migration_demand_unbounded") || !slices.Contains(got.Reasons, "client_pool_sizes_unknown") || !slices.Contains(got.Reasons, "binding_transition") {
		t.Fatal(got)
	}
	bytes, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"migration\"", "client\"", "provider_identity", "credential_ref", "backend_fingerprint", "AppID"} {
		if strings.Contains(string(bytes), secret) {
			t.Fatal("private identity escaped", string(bytes))
		}
	}
	s = budgetSnapshot()
	s.Capacity.ExpiresAt = s.CheckedAt.Add(time.Second)
	got = describeConnectionBudget(s, s.CheckedAt)
	if got.ExpiresAt != s.Capacity.ExpiresAt.Format(time.RFC3339Nano) {
		t.Fatal("report outlived capacity proof", got)
	}
}
