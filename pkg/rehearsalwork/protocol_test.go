package rehearsalwork

import (
	"encoding/json"
	"strings"
	"testing"
)

func validEvidence() Evidence {
	p := Plan{Version: 1, Primary: "primary", Participants: []Participant{{Workload: "primary"}, {Workload: "worker", Async: true}}}
	_, digest, _ := Normalize(p, []string{"primary", "worker"})
	return Evidence{Version: 1, RunID: strings.Repeat("a", 32), PlanSHA256: digest, Status: "complete", Closing: true, Plan: p,
		Bindings: []Binding{{Workload: "primary", AppID: "11111111-1111-4111-8111-111111111111", DeploymentID: "22222222-2222-4222-8222-222222222222"}, {Workload: "worker", AppID: "33333333-3333-4333-8333-333333333333", DeploymentID: "44444444-4444-4444-8444-444444444444"}},
		Jobs:     []Job{{ID: "55555555-5555-4555-8555-555555555555", Workload: "worker", AppID: "33333333-3333-4333-8333-333333333333", DeploymentID: "44444444-4444-4444-8444-444444444444", Method: "POST", Path: "/import", PayloadSHA256: PayloadHash([]byte(`{"customer":1}`)), State: "completed", Attempts: []Attempt{{Number: 1, InstanceID: "66666666-6666-4666-8666-666666666666", DeploymentID: "44444444-4444-4444-8444-444444444444", Outcome: "succeeded"}}}}}
}

func TestManagedWorkProofRejectsMissingOrUnknownExecution(t *testing.T) {
	for _, mode := range []string{"complete", "no_worker", "lost_attempt", "wrong_deployment", "cancelled", "open_admission", "ungranted_resource", "nil_identity", "wrong_run"} {
		t.Run(mode, func(t *testing.T) {
			e := validEvidence()
			switch mode {
			case "no_worker":
				e.Jobs = nil
			case "lost_attempt":
				e.Jobs[0].Attempts[0].Outcome = "unknown"
			case "wrong_deployment":
				e.Jobs[0].Attempts[0].DeploymentID = e.Bindings[0].DeploymentID
			case "cancelled":
				e.Jobs[0].State = "cancelled"
			case "open_admission":
				e.Closing = false
			case "ungranted_resource":
				e.Bindings[0].ObjectsResourceID = e.Jobs[0].ID
			case "nil_identity":
				e.Bindings[0].AppID = "00000000-0000-0000-0000-000000000000"
			case "wrong_run":
				e.RunID = strings.Repeat("z", 32)
			}
			if err := e.Validate(); (err == nil) != (mode == "complete") {
				t.Fatalf("%s: %v", mode, err)
			}
		})
	}
}

func TestManagedWorkPlanAndPayloadPreserveIdentity(t *testing.T) {
	e := validEvidence()
	a, d, err := Normalize(e.Plan, []string{"primary", "worker"})
	if err != nil {
		t.Fatal(err)
	}
	e.Plan.Participants[0], e.Plan.Participants[1] = e.Plan.Participants[1], e.Plan.Participants[0]
	b, next, err := Normalize(e.Plan, []string{"worker", "primary"})
	if err != nil || d != next || string(a) != string(b) {
		t.Fatal("declaration order changed plan identity")
	}
	if PayloadHash([]byte(`{"customer":9007199254740993}`)) == PayloadHash([]byte(`{"customer":9007199254740992}`)) {
		t.Fatal("large customer identities collapsed")
	}
	if PayloadHash([]byte(`{"a":1,"b":true}`)) != PayloadHash([]byte(`{ "b":true, "a":1 }`)) {
		t.Fatal("JSON formatting changed request identity")
	}
	key, secret := ObjectCredentials("fixture-base-secret", e.Bindings[0].AppID)
	otherKey, otherSecret := ObjectCredentials("fixture-base-secret", e.Bindings[1].AppID)
	if key == otherKey || secret == otherSecret || strings.Contains(secret, "fixture-base-secret") {
		t.Fatal("credentials were shared across workloads")
	}
	raw, _ := json.Marshal(e)
	if strings.Contains(string(raw), secret) {
		t.Fatal("proof contains credentials")
	}
}
