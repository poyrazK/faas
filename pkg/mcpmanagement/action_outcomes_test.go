// adr: 20261024210000
package mcpmanagement

import (
	"bytes"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func outcomeDetail(t *testing.T, m *management, id string) actionOutcome {
	t.Helper()
	value, err := m.getActionOutcome(t.Context(), budgetHistorySelector(id))
	if err != nil {
		t.Fatal(err)
	}
	guard := value.(*guardedRead)
	if err := guard.check(t.Context()); err != nil {
		t.Fatal(err)
	}
	return guard.value.(actionOutcome)
}

func TestActionOutcomeMCPReadOnly(t *testing.T) {
	for _, protocol := range []string{"2026-07-28", "2025-11-25"} {
		t.Run(protocol, func(t *testing.T) {
			f, backend, opts := newBudgetFixture(t)
			cs, ctx := connectConfiguredTestServer(t, backend, opts, protocol)
			id := planID(t, callTool(t, ctx, cs, "gregale_plan_route_budgets", budgetArgs(), false))
			pending := callTool(t, ctx, cs, "gregale_get_action_outcome", budgetHistorySelector(id), false)
			if string(pending["reason"]) != `"no_recorded_attempt"` || f.receiptReads != 0 {
				t.Fatal("unattempted plan queried a receipt")
			}
			f.mu.Lock()
			f.ambiguous = true
			f.mu.Unlock()
			callTool(t, ctx, cs, "gregale_apply_route_budgets", applyArgs{PlanID: id}, true)
			out := callTool(t, ctx, cs, "gregale_get_action_outcome", budgetHistorySelector(id), false)
			if string(out["outcome"]) != `"verified_receipt"` || string(out["native_completion"]) != `"not_observed"` || f.writes != 1 {
				t.Fatalf("outcome = %s writes=%d", out, f.writes)
			}
			history := callTool(t, ctx, cs, "gregale_get_action", budgetHistorySelector(id), false)
			var entry actionView
			if json.Unmarshal(history["action"], &entry) != nil || entry.Apply.State != "outcome_unknown" {
				t.Fatal("read rewrote recorded history")
			}
			raw, _ := json.Marshal(out)
			for _, private := range []string{"request_id", "request_hash", "routes", "enabled", "/checkout", "reason", "PRIVATE_BUDGET_REASON"} {
				if bytes.Contains(raw, []byte(private)) {
					t.Fatalf("outcome exposed %s", private)
				}
			}
		})
	}
}

func TestActionOutcomeExpiredEncryptedRestart(t *testing.T) {
	f, backend, opts := newBudgetFixture(t)
	store, path, key := testStateStore(t)
	opts.planStore, opts.planNamespace = store, "outcome-grant"
	client := api.NewClient(backend.URL, testToken)
	_, m, err := newManagementServer(client, opts)
	if err != nil {
		t.Fatal(err)
	}
	id := budgetHistoryPlan(t, m)
	original := m.plans[id].Budgets.Proposed
	f.mu.Lock()
	f.ambiguous = true
	f.mu.Unlock()
	if _, err := m.applyRouteBudgets(t.Context(), applyArgs{PlanID: id}); err == nil {
		t.Fatal("lost reply succeeded")
	}
	m.plans[id].ExpiresAt = time.Now().Add(-time.Second)
	if err := m.persistPlans(); err != nil {
		t.Fatal(err)
	}
	// Even if native configuration moves on, the old receipt remains historical.
	later := original
	revision := int64(1)
	later.RequestID, later.ExpectedRevision, later.Enabled, later.Routes = uuid.NewString(), &revision, false, []api.RouteMonitorRoute{}
	if _, err := f.store.SetEnvironmentRouteBudgets(t.Context(), f.account.ID, f.app.ID, "staging", later); err != nil {
		t.Fatal(err)
	}
	lookup := m.budgetOutcomes[id]
	ciphertext, err := os.ReadFile(path)
	if err != nil || bytes.Contains(ciphertext, []byte(lookup.RequestID)) || bytes.Contains(ciphertext, []byte(lookup.RequestHash)) {
		t.Fatal("lookup was not encrypted")
	}
	var snapshot budgetOutcomeSnapshot
	if err := store.read(budgetOutcomeNamespace+opts.planNamespace, &snapshot); err != nil || len(snapshot.Items) != 1 {
		t.Fatal("lookup was not durably saved before write")
	}
	encoded, _ := json.Marshal(snapshot.Items[id])
	var fields map[string]string
	if json.Unmarshal(encoded, &fields) != nil || len(fields) != 3 || fields["RequestID"] != original.RequestID || fields["RequestHash"] != lookup.RequestHash || fields["Binding"] == "" {
		t.Fatal("lookup retained more than its UUID, fingerprint and binding")
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	next, err := OpenStateStore(path, key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = next.Close() })
	opts.planStore, opts.AllowWrites = next, false
	_, restored, err := newManagementServer(client, opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(restored.plans) != 0 || len(restored.budgetOutcomes) != 1 {
		t.Fatal("restart resurrected expired mutation payload or lost lookup")
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	history := actionDetail(t, restored, budgetHistorySelector(id))
	out := outcomeDetail(t, restored, id)
	if out.Outcome != "verified_receipt" || out.Receipt == nil || *out.Receipt.BudgetRevision != 1 || *out.Receipt.AppPolicyRevision != 0 || out.Receipt.BudgetSource != "environment_policy" || out.NativeCompletion != "not_observed" {
		t.Fatalf("expired outcome missing: %+v", out)
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) || !reflect.DeepEqual(history, actionDetailIgnoringObservation(t, restored, id, history)) {
		t.Fatal("outcome read changed durable history, expiry or storage")
	}
	if _, err := restored.applyRouteBudgets(t.Context(), applyArgs{PlanID: id}); err == nil {
		t.Fatal("expired lookup restored write authority")
	}
	current, err := f.store.GetEnvironmentRouteBudgets(t.Context(), f.account.ID, f.app.ID, "staging")
	if err != nil || current.Revision != 2 || f.writes != 1 || len(restored.plans) != 0 {
		t.Fatal("outcome read replayed original write")
	}
	// The lookup is grant-local and is removed with retained history.
	opts.planNamespace = "another-grant"
	_, other, err := newManagementServer(client, opts)
	if err != nil || len(other.actions) != 0 || len(other.budgetOutcomes) != 0 {
		t.Fatal("another grant inherited outcome")
	}
	entry := restored.actions[id]
	entry.RetainedUntil = time.Now().Add(-time.Second)
	restored.actions[id] = entry
	if err := restored.persistPlans(); err != nil {
		t.Fatal(err)
	}
	var pruned budgetOutcomeSnapshot
	if err := next.read(budgetOutcomeNamespace+restored.opts.planNamespace, &pruned); err != nil || len(pruned.Items) != 0 || len(restored.budgetOutcomes) != 0 {
		t.Fatal("expired history retained a lookup")
	}
}

func actionDetailIgnoringObservation(t *testing.T, m *management, id string, previous actionHistoryDetail) actionHistoryDetail {
	t.Helper()
	current := actionDetail(t, m, budgetHistorySelector(id))
	current.ObservedAt = previous.ObservedAt
	return current
}

func TestActionOutcomeUnavailableAndMismatchedEvidence(t *testing.T) {
	cases := []struct {
		name   string
		status int
		change func(*api.EnvironmentRouteBudgetReceipt)
	}{
		{name: "old API", status: 404}, {name: "unavailable", status: 503},
		{name: "request ID", change: func(r *api.EnvironmentRouteBudgetReceipt) { r.RequestID = uuid.NewString() }},
		{name: "fingerprint", change: func(r *api.EnvironmentRouteBudgetReceipt) { r.RequestHash = strings.Repeat("0", 64) }},
		{name: "app", change: func(r *api.EnvironmentRouteBudgetReceipt) { r.AppID = uuid.NewString() }},
		{name: "scope", change: func(r *api.EnvironmentRouteBudgetReceipt) { r.Scope = "default" }},
		{name: "project", change: func(r *api.EnvironmentRouteBudgetReceipt) { r.ProjectID = uuid.NewString() }},
		{name: "environment", change: func(r *api.EnvironmentRouteBudgetReceipt) { r.EnvironmentID = uuid.NewString() }},
		{name: "revision", change: func(r *api.EnvironmentRouteBudgetReceipt) { r.Revision++ }},
		{name: "app revision", change: func(r *api.EnvironmentRouteBudgetReceipt) { r.AppRevision++ }},
		{name: "policy source", change: func(r *api.EnvironmentRouteBudgetReceipt) { r.BudgetSource = "app_policy" }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			f, b, opts := newBudgetFixture(t)
			_, m, err := newManagementServer(api.NewClient(b.URL, testToken), opts)
			if err != nil {
				t.Fatal(err)
			}
			id := budgetHistoryPlan(t, m)
			f.mu.Lock()
			f.ambiguous = true
			f.mu.Unlock()
			if _, err := m.applyRouteBudgets(t.Context(), applyArgs{PlanID: id}); err == nil {
				t.Fatal("lost reply succeeded")
			}
			f.mu.Lock()
			f.receiptStatus, f.receiptTransform = test.status, test.change
			f.mu.Unlock()
			out := outcomeDetail(t, m, id)
			reason := "receipt_unverified"
			if test.status != 0 {
				reason = "receipt_unavailable"
			}
			if out.Outcome != "unresolved" || out.Reason != reason || out.Receipt != nil || f.writes != 1 {
				t.Fatalf("unverified evidence became committed: %+v", out)
			}
		})
	}
}

func TestActionOutcomeAbsentReceiptAndFinalGuards(t *testing.T) {
	for _, change := range []string{"missing", "lookup", "environment", "account", "consent", "removed", "binding", "storage"} {
		t.Run(change, func(t *testing.T) {
			f, b, opts := newBudgetFixture(t)
			store, _, _ := testStateStore(t)
			opts.planStore, opts.planNamespace = store, "guard-grant"
			_, m, err := newManagementServer(api.NewClient(b.URL, testToken), opts)
			if err != nil {
				t.Fatal(err)
			}
			id := budgetHistoryPlan(t, m)
			// Crash between durable attempt recording and the actual native write.
			m.plans[id].attempted = true
			if err := m.persistPlans(); err != nil {
				t.Fatal(err)
			}
			value, err := m.getActionOutcome(t.Context(), budgetHistorySelector(id))
			if err != nil {
				t.Fatal(err)
			}
			guard := value.(*guardedRead)
			out := guard.value.(actionOutcome)
			if out.Outcome != "unresolved" || out.Reason != "receipt_unavailable" || out.Receipt != nil {
				t.Fatal("absent receipt proved non-commit")
			}
			switch change {
			case "lookup":
				delete(m.budgetOutcomes, id)
			case "environment":
				f.mu.Lock()
				f.env.ID = uuid.NewString()
				f.mu.Unlock()
			case "account":
				f.mu.Lock()
				f.account.ID = uuid.NewString()
				f.mu.Unlock()
			case "consent":
				delete(m.opts.Environments.Named, "shop/staging")
			case "removed":
				delete(m.actions, id)
			case "binding":
				entry := m.actions[id]
				entry.AppID = uuid.NewString()
				m.actions[id] = entry
			case "storage":
				if err := store.Close(); err != nil {
					t.Fatal(err)
				}
			}
			err = guard.check(t.Context())
			if (change == "missing") != (err == nil) {
				t.Fatalf("final guard %s = %v", change, err)
			}
			if f.writes != 0 {
				t.Fatal("receipt lookup issued a write")
			}
		})
	}
}

func TestBudgetOutcomeRestoreCompatibilityAndValidation(t *testing.T) {
	for _, change := range []string{"legacy", "expired legacy", "future", "binding", "orphan", "preserved"} {
		t.Run(change, func(t *testing.T) {
			_, b, opts := newBudgetFixture(t)
			store, _, _ := testStateStore(t)
			opts.planStore, opts.planNamespace = store, "compat-grant"
			client := api.NewClient(b.URL, testToken)
			_, m, err := newManagementServer(client, opts)
			if err != nil {
				t.Fatal(err)
			}
			id := budgetHistoryPlan(t, m)
			m.plans[id].attempted = true
			if change == "expired legacy" {
				m.plans[id].ExpiresAt = time.Now().Add(-time.Second)
			}
			if err := m.persistPlans(); err != nil {
				t.Fatal(err)
			}
			snapshot := budgetOutcomeSnapshot{Version: 1, Items: map[string]budgetOutcomeLookup{id: m.budgetOutcomes[id]}}
			switch change {
			case "legacy", "expired legacy":
				snapshot = budgetOutcomeSnapshot{}
			case "future":
				snapshot.Version = 2
			case "binding":
				lookup := snapshot.Items[id]
				lookup.Binding = strings.Repeat("0", 64)
				snapshot.Items[id] = lookup
			case "orphan":
				snapshot.Items[strings.Repeat("f", 64)] = snapshot.Items[id]
				delete(snapshot.Items, id)
			}
			if err := store.updateMany(map[string]any{budgetOutcomeNamespace + opts.planNamespace: snapshot, "unrelated/future": map[string]int{"version": 9}}); err != nil {
				t.Fatal(err)
			}
			_, restored, err := newManagementServer(client, opts)
			if change == "future" || change == "binding" {
				if err == nil {
					t.Fatal("invalid snapshot restored")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if change == "expired legacy" {
				out := outcomeDetail(t, restored, id)
				if out.Outcome != "unresolved" || out.Reason != "lookup_unavailable" || len(restored.plans) != 0 {
					t.Fatal("legacy history reconstructed expired request")
				}
			} else if len(restored.budgetOutcomes) != 1 {
				t.Fatal("live legacy reference was not imported or orphan was not pruned")
			}
			var future map[string]int
			if err := store.read("unrelated/future", &future); err != nil || future["version"] != 9 {
				t.Fatal("unknown namespace was changed")
			}
		})
	}
}

func TestBudgetOutcomeGrantRevocation(t *testing.T) {
	store, _, _ := testStateStore(t)
	lookup := budgetOutcomeSnapshot{Version: 1, Items: map[string]budgetOutcomeLookup{strings.Repeat("a", 64): {RequestID: uuid.NewString(), RequestHash: strings.Repeat("b", 64), Binding: strings.Repeat("c", 64)}}}
	if err := store.updateMany(map[string]any{budgetOutcomeNamespace + "revoked": lookup, budgetOutcomeNamespace + "active": lookup}); err != nil {
		t.Fatal(err)
	}
	if err := store.update("oauth", oauthSnapshot{Grants: map[string]storedGrant{"active": {}}}); err != nil {
		t.Fatal(err)
	}
	var revoked, active budgetOutcomeSnapshot
	if err := store.read(budgetOutcomeNamespace+"revoked", &revoked); err != nil || len(revoked.Items) != 0 {
		t.Fatal("revocation retained private lookup")
	}
	if err := store.read(budgetOutcomeNamespace+"active", &active); err != nil || !reflect.DeepEqual(active, lookup) {
		t.Fatal("revocation removed unrelated active grant")
	}
}
