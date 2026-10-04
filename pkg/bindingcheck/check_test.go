// adr: 427 — policy blocks stale residents independently of passing probes.
package bindingcheck

import (
	"encoding/json"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func fixture() (api.AppBindingInventory, Policy, time.Time) {
	now := time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC)
	stamp, checked := now.Add(-time.Hour), now.Add(-time.Minute)
	generation, pending := int64(2), false
	bindings := []api.AppBindingInventoryItem{
		{Type: api.BindingTypeService, Name: "billing", Binding: "BILLING_URL", Scope: "app", State: "enforced"},
		{Type: api.BindingTypePostgres, Name: "primary", Binding: "DATABASE_URL", Scope: "production", State: "ready", CredentialGeneration: &generation, RotationPending: &pending},
		{Type: api.BindingTypeObjectStorage, Name: "assets", Binding: "ASSETS", Scope: "production", State: "active", RotationPending: &pending},
	}
	for index := range bindings {
		names := map[string][]string{
			api.BindingTypeService:       {"dns", "tls", "authorization", "routing"},
			api.BindingTypePostgres:      {"environment", "configuration", "connection", "query"},
			api.BindingTypeObjectStorage: {"environment", "configuration", "connection", "authorization", "bucket_access"},
		}[bindings[index].Type]
		checks := []api.BindingVerificationCheck{}
		for _, name := range names {
			checks = append(checks, api.BindingVerificationCheck{Name: name, Status: "passed"})
		}
		bindings[index].VerificationStatus = "passed"
		bindings[index].Verification = &api.BindingVerification{Result: "passed", Source: "task_guest", DeploymentID: "deployment-1", Scope: "production", CheckedAt: &checked,
			Checks: checks}
	}
	bindings[1].Verification.CredentialGeneration = &generation
	inventory := api.AppBindingInventory{App: "api", GeneratedAt: now, Complete: true, Bindings: bindings,
		VerificationDeploymentID: "deployment-1", VerificationScope: "production",
		RuntimeFreshness: &api.BindingRuntimeFreshness{Source: "instance_started_at", ObservedAt: now, ConfigChangedAt: &stamp,
			Deployments: []api.BindingRuntimeDeployment{{DeploymentID: "deployment-1", Scope: "production", DeploymentStatus: "live", Status: "current",
				Serving: api.BindingRuntimeInstanceCounts{Current: 1}, Resident: api.BindingRuntimeInstanceCounts{Current: 1}}}}}
	return inventory, Policy{App: "api", MaxVerificationAge: DefaultMaxVerificationAge}, now
}

func codes(findings []Finding) []string {
	result := []string{}
	for _, finding := range findings {
		result = append(result, finding.Code)
	}
	return result
}

func TestEvaluateCurrentEvidencePassesAllSupportedFamilies(t *testing.T) {
	inventory, policy, now := fixture()
	report, err := Evaluate(inventory, policy, now)
	if err != nil || !report.Passed || report.Coverage != "complete" || report.Scope != "production" || len(report.Bindings) != 3 || len(report.Blockers) != 0 {
		t.Fatalf("report=%+v err=%v", report, err)
	}
	for _, item := range report.Bindings {
		if item.Status != "passed" {
			t.Fatalf("binding=%+v", item)
		}
	}
}

func TestEvaluateEvidenceNeverPassesMissingStaleOrContradictoryMetadata(t *testing.T) {
	for _, tc := range []struct {
		name, code string
		mutate     func(*api.AppBindingInventoryItem, time.Time)
	}{
		{"failed", "verification_failed", func(b *api.AppBindingInventoryItem, _ time.Time) { b.VerificationStatus = "failed" }},
		{"revision changed", "verification_stale", func(b *api.AppBindingInventoryItem, _ time.Time) { b.VerificationStatus = "stale" }},
		{"pending", "verification_unknown", func(b *api.AppBindingInventoryItem, _ time.Time) {
			b.VerificationStatus = "unknown"
			b.Verification.Reason = "probe_pending"
		}},
		{"nil evidence", "verification_unknown", func(b *api.AppBindingInventoryItem, _ time.Time) { b.Verification = nil }},
		{"unknown result", "verification_unknown", func(b *api.AppBindingInventoryItem, _ time.Time) { b.Verification.Result = "unknown" }},
		{"contradictory reason", "verification_unknown", func(b *api.AppBindingInventoryItem, _ time.Time) { b.Verification.Reason = "check_failed" }},
		{"missing checks", "verification_unknown", func(b *api.AppBindingInventoryItem, _ time.Time) { b.Verification.Checks = nil }},
		{"partial checks", "verification_unknown", func(b *api.AppBindingInventoryItem, _ time.Time) { b.Verification.Checks = b.Verification.Checks[:1] }},
		{"duplicate checks", "verification_unknown", func(b *api.AppBindingInventoryItem, _ time.Time) { b.Verification.Checks[1] = b.Verification.Checks[0] }},
		{"unknown check", "verification_unknown", func(b *api.AppBindingInventoryItem, _ time.Time) { b.Verification.Checks[1].Name = "unknown_stage" }},
		{"failed stage", "verification_unknown", func(b *api.AppBindingInventoryItem, _ time.Time) { b.Verification.Checks[0].Status = "failed" }},
		{"unknown source", "verification_unknown", func(b *api.AppBindingInventoryItem, _ time.Time) { b.Verification.Source = "resident" }},
		{"old deployment", "verification_deployment_mismatch", func(b *api.AppBindingInventoryItem, _ time.Time) { b.Verification.DeploymentID = "deployment-old" }},
		{"old scope", "verification_scope_mismatch", func(b *api.AppBindingInventoryItem, _ time.Time) { b.Verification.Scope = "staging" }},
		{"missing time", "verification_time_unknown", func(b *api.AppBindingInventoryItem, _ time.Time) { b.Verification.CheckedAt = nil }},
		{"zero time", "verification_time_unknown", func(b *api.AppBindingInventoryItem, _ time.Time) { b.Verification.CheckedAt = new(time.Time) }},
		{"future time", "verification_time_future", func(b *api.AppBindingInventoryItem, now time.Time) {
			at := now.Add(time.Nanosecond)
			b.Verification.CheckedAt = &at
		}},
		{"expired", "verification_expired", func(b *api.AppBindingInventoryItem, now time.Time) {
			at := now.Add(-DefaultMaxVerificationAge - time.Nanosecond)
			b.Verification.CheckedAt = &at
		}},
		{"generation changed", "verification_generation_mismatch", func(b *api.AppBindingInventoryItem, _ time.Time) {
			generation := int64(1)
			b.Verification.CredentialGeneration = &generation
		}},
		{"generation absent", "verification_generation_mismatch", func(b *api.AppBindingInventoryItem, _ time.Time) { b.Verification.CredentialGeneration = nil }},
		{"provisioning", "binding_not_ready", func(b *api.AppBindingInventoryItem, _ time.Time) { b.State = "provisioning" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inventory, policy, now := fixture()
			tc.mutate(&inventory.Bindings[1], now)
			report, err := Evaluate(inventory, policy, now)
			if err != nil || report.Passed || !slices.Contains(codes(report.Blockers), tc.code) {
				t.Fatalf("report=%+v err=%v, want %s", report, err, tc.code)
			}
		})
	}
}

func TestEvaluateVerificationAgeBoundaryAndClock(t *testing.T) {
	inventory, policy, now := fixture()
	at := now.Add(-policy.MaxVerificationAge)
	inventory.Bindings[0].Verification.CheckedAt = &at
	report, err := Evaluate(inventory, policy, now)
	if err != nil || !report.Passed {
		t.Fatalf("age boundary should pass: %+v %v", report, err)
	}
	report, err = Evaluate(inventory, policy, now.Add(time.Nanosecond))
	if err != nil || report.Passed || !slices.Contains(codes(report.Blockers), "verification_expired") {
		t.Fatalf("same revision must expire as time advances: %+v %v", report, err)
	}
}

func TestEvaluateRuntimeCannotBeHiddenByProbeOrRefreshCompletion(t *testing.T) {
	for _, tc := range []struct {
		name, code string
		mutate     func(*api.AppBindingInventory)
	}{
		{"old serving resident", "runtime_stale", func(i *api.AppBindingInventory) {
			d := &i.RuntimeFreshness.Deployments[0]
			d.Serving, d.Resident, d.Status = api.BindingRuntimeInstanceCounts{Stale: 1}, api.BindingRuntimeInstanceCounts{Stale: 1}, "stale"
		}},
		{"old draining resident", "runtime_stale", func(i *api.AppBindingInventory) {
			d := &i.RuntimeFreshness.Deployments[0]
			d.Resident.Stale, d.Status = 1, "stale"
		}},
		{"replacement does not hide old serving", "runtime_stale", func(i *api.AppBindingInventory) {
			d := &i.RuntimeFreshness.Deployments[0]
			d.Serving, d.Resident, d.Starting, d.Status = api.BindingRuntimeInstanceCounts{Stale: 1}, api.BindingRuntimeInstanceCounts{Current: 1, Stale: 1}, 1, "stale"
		}},
		{"unknown resident", "runtime_unknown", func(i *api.AppBindingInventory) {
			d := &i.RuntimeFreshness.Deployments[0]
			d.Serving, d.Resident, d.Status = api.BindingRuntimeInstanceCounts{Unknown: 1}, api.BindingRuntimeInstanceCounts{Unknown: 1}, "unknown"
		}},
		{"starting", "runtime_updating", func(i *api.AppBindingInventory) {
			d := &i.RuntimeFreshness.Deployments[0]
			d.Serving, d.Starting, d.Status = api.BindingRuntimeInstanceCounts{}, 1, "updating"
		}},
		{"unreadable", "runtime_unknown", func(i *api.AppBindingInventory) { i.RuntimeFreshness = nil }},
		{"missing selected deployment", "runtime_unknown", func(i *api.AppBindingInventory) { i.RuntimeFreshness.Deployments = nil }},
		{"unknown source", "runtime_unknown", func(i *api.AppBindingInventory) { i.RuntimeFreshness.Source = "guest_ack" }},
		{"missing stamp", "runtime_unknown", func(i *api.AppBindingInventory) { i.RuntimeFreshness.ConfigChangedAt = nil }},
		{"zero stamp", "runtime_unknown", func(i *api.AppBindingInventory) { i.RuntimeFreshness.ConfigChangedAt = new(time.Time) }},
		{"future observation", "runtime_unknown", func(i *api.AppBindingInventory) { i.RuntimeFreshness.ObservedAt = i.GeneratedAt.Add(time.Second) }},
		{"inconsistent config clock", "runtime_unknown", func(i *api.AppBindingInventory) {
			at := i.RuntimeFreshness.ObservedAt.Add(time.Second)
			i.RuntimeFreshness.ConfigChangedAt = &at
		}},
		{"selected deployment not live", "runtime_unknown", func(i *api.AppBindingInventory) { i.RuntimeFreshness.Deployments[0].DeploymentStatus = "superseded" }},
		{"duplicate deployment", "runtime_unknown", func(i *api.AppBindingInventory) {
			i.RuntimeFreshness.Deployments = append(i.RuntimeFreshness.Deployments, i.RuntimeFreshness.Deployments[0])
		}},
		{"serving and starting overlap", "runtime_unknown", func(i *api.AppBindingInventory) {
			i.RuntimeFreshness.Deployments[0].Starting = 1
			i.RuntimeFreshness.Deployments[0].Status = "updating"
		}},
		{"negative counts", "runtime_unknown", func(i *api.AppBindingInventory) { i.RuntimeFreshness.Deployments[0].Resident.Stale = -1 }},
		{"serving absent from resident", "runtime_unknown", func(i *api.AppBindingInventory) { i.RuntimeFreshness.Deployments[0].Serving.Current = 2 }},
		{"contradictory status", "runtime_unknown", func(i *api.AppBindingInventory) { i.RuntimeFreshness.Deployments[0].Status = "inactive" }},
		{"superseded deployment resident", "runtime_stale", func(i *api.AppBindingInventory) {
			i.RuntimeFreshness.Deployments = append(i.RuntimeFreshness.Deployments, api.BindingRuntimeDeployment{DeploymentID: "old-deployment", Scope: "production", DeploymentStatus: "superseded", Status: "stale", Resident: api.BindingRuntimeInstanceCounts{Stale: 1}})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inventory, policy, now := fixture()
			inventory.Bindings[1].Refresh = &api.BindingRefresh{Status: "completed"}
			tc.mutate(&inventory)
			report, err := Evaluate(inventory, policy, now)
			if err != nil || report.Passed || !slices.Contains(codes(report.Blockers), tc.code) {
				t.Fatalf("passing evidence/completed handoff hid runtime: %+v %v", report, err)
			}
		})
	}
}

func TestEvaluateInactiveRuntimeDoesNotClaimAdoption(t *testing.T) {
	inventory, policy, now := fixture()
	d := &inventory.RuntimeFreshness.Deployments[0]
	d.Serving, d.Resident, d.Status = api.BindingRuntimeInstanceCounts{}, api.BindingRuntimeInstanceCounts{}, "inactive"
	inventory.RuntimeFreshness.ConfigChangedAt = nil
	report, err := Evaluate(inventory, policy, now)
	if err != nil || !report.Passed || !slices.Contains(codes(report.Warnings), "runtime_inactive") {
		t.Fatalf("inactive runtime=%+v %v", report, err)
	}
}

func TestEvaluateRotationRequiresRetirementAndHandoff(t *testing.T) {
	for _, tc := range []struct{ status, code string }{
		{"completed", "rotation_pending"}, {"failed", "refresh_failed"}, {"queued", "refresh_incomplete"}, {"running", "refresh_incomplete"},
		{"retrying", "refresh_incomplete"}, {"not_queued", "refresh_incomplete"}, {"unknown", "refresh_unknown"},
	} {
		t.Run(tc.status, func(t *testing.T) {
			inventory, policy, now := fixture()
			pending := true
			inventory.Bindings[2].RotationPending = &pending
			inventory.Bindings[2].Refresh = &api.BindingRefresh{Status: tc.status}
			report, err := Evaluate(inventory, policy, now)
			if err != nil || report.Passed || !slices.Contains(codes(report.Blockers), tc.code) || !slices.Contains(codes(report.Blockers), "rotation_pending") {
				t.Fatalf("rotation=%+v %v", report, err)
			}
		})
	}
	inventory, policy, now := fixture()
	inventory.Bindings[1].RotationPending = nil
	report, err := Evaluate(inventory, policy, now)
	if err != nil || report.Passed || !slices.Contains(codes(report.Blockers), "rotation_unknown") {
		t.Fatalf("missing rotation state=%+v %v", report, err)
	}
}

func TestEvaluateSelectsManualTaskScopeAndCannotAcceptAnotherScope(t *testing.T) {
	inventory, policy, now := fixture()
	inventory.Bindings = append(inventory.Bindings, api.AppBindingInventoryItem{Type: api.BindingTypePostgres, Scope: "staging", Binding: "DATABASE_URL", State: "failed"})
	inventory.RuntimeFreshness.Deployments = append(inventory.RuntimeFreshness.Deployments, api.BindingRuntimeDeployment{Scope: "staging", DeploymentID: "stage", Status: "stale", Resident: api.BindingRuntimeInstanceCounts{Stale: 1}})
	report, err := Evaluate(inventory, policy, now)
	if err != nil || !report.Passed || len(report.Runtime) != 1 {
		t.Fatalf("other scope affected check: %+v %v", report, err)
	}
	skipped := false
	for _, item := range report.Bindings {
		skipped = skipped || item.Scope == "staging" && item.Status == "skipped" && item.Reason == "outside_scope"
	}
	if !skipped {
		t.Fatalf("scope exclusion is not explicit: %+v", report.Bindings)
	}
	policy.Scope, inventory.Scope = "staging", "staging"
	report, err = Evaluate(inventory, policy, now)
	if err != nil || report.Passed || !slices.Contains(codes(report.Blockers), "deployment_scope_mismatch") {
		t.Fatalf("other deployment scope accepted: %+v %v", report, err)
	}
}

func TestEvaluateIncompleteReadsAndDeploymentIdentityFailClosed(t *testing.T) {
	for _, tc := range []struct {
		code   string
		mutate func(*api.AppBindingInventory)
	}{
		{"inventory_incomplete", func(i *api.AppBindingInventory) { i.Complete = false }},
		{"inventory_incomplete", func(i *api.AppBindingInventory) {
			i.Issues = []api.BindingInventoryIssue{{Type: "postgres", Severity: "warning", Code: "managed_postgres_unavailable"}}
		}},
		{"inventory_app_mismatch", func(i *api.AppBindingInventory) { i.App = "other-app" }},
		{"inventory_scope_mismatch", func(i *api.AppBindingInventory) { i.Scope = "staging" }},
		{"deployment_missing", func(i *api.AppBindingInventory) { i.VerificationDeploymentID = "" }},
		{"deployment_scope_mismatch", func(i *api.AppBindingInventory) { i.VerificationScope = "" }},
	} {
		t.Run(tc.code, func(t *testing.T) {
			inventory, policy, now := fixture()
			tc.mutate(&inventory)
			report, err := Evaluate(inventory, policy, now)
			if err != nil || report.Passed || !slices.Contains(codes(report.Blockers), tc.code) {
				t.Fatalf("report=%+v %v", report, err)
			}
		})
	}
}

func TestEvaluateUnsupportedCoverageRequiresExplicitWaiver(t *testing.T) {
	for _, kind := range []string{api.BindingTypeQueue, api.BindingTypeOutbound} {
		for _, allow := range []bool{false, true} {
			t.Run(kind+map[bool]string{false: " strict", true: " waived"}[allow], func(t *testing.T) {
				inventory, policy, now := fixture()
				policy.AllowUnsupported = allow
				inventory.Bindings = []api.AppBindingInventoryItem{{Type: kind, Name: "delivery", Access: "pull", Scope: "app", State: "enabled", VerificationStatus: "passed"}}
				report, err := Evaluate(inventory, policy, now)
				if err != nil || report.Passed != allow || report.Coverage != "partial" || report.Bindings[0].Status != "unsupported" {
					t.Fatalf("unsupported was represented as verified: %+v %v", report, err)
				}
			})
		}
	}
	inventory, policy, now := fixture()
	policy.AllowUnsupported = true
	inventory.Bindings = []api.AppBindingInventoryItem{{Type: "future_family", State: "enabled"}}
	report, err := Evaluate(inventory, policy, now)
	if err != nil || report.Passed || !slices.Contains(codes(report.Blockers), "binding_type_unknown") {
		t.Fatalf("unknown type was waived: %+v %v", report, err)
	}
}

func TestEvaluateDisabledAndEmptyInventoriesHaveNoConnectivityCoverage(t *testing.T) {
	for _, bindings := range [][]api.AppBindingInventoryItem{nil, {{Type: api.BindingTypeQueue, State: "disabled", Scope: "app"}, {Type: api.BindingTypeOutbound, State: "disabled", Scope: "app"}}} {
		inventory, policy, now := fixture()
		inventory.Bindings = bindings
		report, err := Evaluate(inventory, policy, now)
		if err != nil || !report.Passed || report.Coverage != "none" || !slices.Contains(codes(report.Warnings), "no_bindings") || report.Bindings == nil || report.Blockers == nil {
			t.Fatalf("empty coverage=%+v %v", report, err)
		}
	}
}

func TestEvaluateIsDeterministicAndDoesNotMutateInput(t *testing.T) {
	inventory, policy, now := fixture()
	before, _ := json.Marshal(inventory)
	one, err := Evaluate(inventory, policy, now)
	if err != nil {
		t.Fatal(err)
	}
	two, err := Evaluate(inventory, policy, now)
	after, _ := json.Marshal(inventory)
	if err != nil || !reflect.DeepEqual(one, two) || string(before) != string(after) {
		t.Fatalf("non-deterministic or mutating evaluation: %v", err)
	}
}

func TestEvaluateRejectsInvalidPolicy(t *testing.T) {
	for _, mutate := range []func(*Policy){
		func(p *Policy) { p.App = "" }, func(p *Policy) { p.Scope = "__all__" },
		func(p *Policy) { p.MaxVerificationAge = 0 }, func(p *Policy) { p.MaxVerificationAge = -time.Second },
	} {
		inventory, policy, now := fixture()
		mutate(&policy)
		if _, err := Evaluate(inventory, policy, now); err == nil {
			t.Fatalf("accepted invalid policy: %+v", policy)
		}
	}
}

func TestEvaluateCannotUseAnotherDeploymentsPassedEvidence(t *testing.T) {
	inventory, policy, now := fixture()
	policy.DeploymentID = "deployment-candidate"
	report, err := Evaluate(inventory, policy, now)
	if err != nil || report.Passed || !slices.Contains(codes(report.Blockers), "deployment_mismatch") || !slices.Contains(codes(report.Blockers), "deployment_selection_unconfirmed") {
		t.Fatalf("serving evidence satisfied candidate policy: %+v %v", report, err)
	}
	policy.DeploymentID = inventory.VerificationDeploymentID
	report, err = Evaluate(inventory, policy, now)
	if err != nil || report.Passed || !slices.Contains(codes(report.Blockers), "deployment_selection_unconfirmed") {
		t.Fatalf("ignored selector was accepted: %+v %v", report, err)
	}
	inventory.RequestedDeploymentID = policy.DeploymentID
	report, err = Evaluate(inventory, policy, now)
	if err != nil || !report.Passed || report.ExpectedDeploymentID != policy.DeploymentID {
		t.Fatalf("confirmed selector blocked: %+v %v", report, err)
	}
}
