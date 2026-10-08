package operations

import (
	"encoding/json"
	"github.com/onebox-faas/faas/pkg/api"
	"strings"
	"testing"
)

func testSpec() api.OperationDefinitionSpec {
	return api.OperationDefinitionSpec{Name: "export", Method: "POST", Path: "/exports", Owner: api.OperationOwnerPlatformTenant,
		InputSchema:    []byte(`{"type":"object","properties":{"count":{"type":"integer","minimum":1}},"required":["count"],"additionalProperties":false}`),
		OutputSchema:   []byte(`{"type":"object","properties":{"file":{"type":"string"}},"required":["file"],"additionalProperties":false}`),
		ProgressStages: []string{"collecting", "uploading"}}
}

func TestCanonicalOperationInput(t *testing.T) {
	for _, pair := range [][2]string{
		{`{"b":1.00,"a":[100,-0]}`, `{"a":[1e2,0.0],"b":1e0}`},
		{`{"n":9007199254740993}`, `{"n":9.007199254740993e15}`},
		{`{"x":"\u0061"}`, `{"x":"a"}`},
	} {
		a, err := InputFingerprint([]byte(pair[0]))
		if err != nil {
			t.Fatal(err)
		}
		b, err := InputFingerprint([]byte(pair[1]))
		if err != nil || a != b {
			t.Fatalf("equivalent inputs differ: %v", err)
		}
	}
	for _, raw := range []string{`{"a":1,"a":1}`, `{"nested":{"a":1,"\u0061":2}}`, `{} {}`, `1e99999999`, `{"a":`, strings.Repeat("[", api.OperationJSONMaxDepth+2) + "0" + strings.Repeat("]", api.OperationJSONMaxDepth+2)} {
		if _, err := CanonicalJSON([]byte(raw)); err == nil {
			t.Fatalf("accepted ambiguous or unbounded JSON: %.80s", raw)
		}
	}
	a, _ := InputFingerprint([]byte(`9007199254740992`))
	b, _ := InputFingerprint([]byte(`9007199254740993`))
	if a == b {
		t.Fatal("large integer values collapsed")
	}
}

func TestOperationContractSchemasAndProgress(t *testing.T) {
	c, err := Compile(testSpec(), api.MustLimitsFor(api.PlanHobby).Operations)
	if err != nil {
		t.Fatal(err)
	}
	if c.Spec.Recovery != api.OperationRecoveryReconcile {
		t.Fatal("unknown effects must default to reconciliation")
	}
	for _, raw := range []string{`{"count":1}`, `{"count":1e2}`} {
		if err := c.ValidateInput([]byte(raw), 4096); err != nil {
			t.Fatal(err)
		}
	}
	for _, raw := range []string{`{"count":0}`, `{"count":"1"}`, `{"count":1,"tenant":"forged"}`, `{"count":1,"count":2}`} {
		if err := c.ValidateInput([]byte(raw), 4096); err == nil {
			t.Fatalf("accepted invalid input %s", raw)
		}
	}
	if err := c.ValidateOutput([]byte(`{"file":"obj://export"}`), 4096); err != nil {
		t.Fatal(err)
	}
	if err := c.ValidateOutput([]byte(`{"ok":true}`), 4096); err == nil {
		t.Fatal("accepted invalid output")
	}
	if err := c.ValidateProgress(api.OperationReportRequest{ReportID: "r1", Stage: "collecting", Completed: 1, Total: 2}); err != nil {
		t.Fatal(err)
	}
	for _, r := range []api.OperationReportRequest{{Stage: "collecting", Total: 2}, {ReportID: "r", Stage: "unknown", Total: 2}, {ReportID: "r", Stage: "collecting", Completed: 3, Total: 2}} {
		if err := c.ValidateProgress(r); err == nil {
			t.Fatal("accepted invalid progress")
		}
	}
}

func TestOperationContractCannotLoadExternalResources(t *testing.T) {
	for _, schema := range []string{`{"$ref":"https://example.com/schema"}`, `{"$ref":"file:///etc/passwd"}`, `{"$ref":"other.json"}`, `{"$dynamicRef":"https:\/\/example.com/schema"}`, `{"$id":"https://example.com/schema","type":"object"}`, `{"type":"object","type":"string"}`} {
		spec := testSpec()
		spec.InputSchema = []byte(schema)
		if _, err := Compile(spec, api.MustLimitsFor(api.PlanHobby).Operations); err == nil {
			t.Fatalf("accepted external/ambiguous schema %s", schema)
		}
	}
	spec := testSpec()
	spec.InputSchema = []byte(`{"$defs":{"count":{"type":"integer"}},"type":"object","properties":{"count":{"$ref":"#/$defs/count"}}}`)
	if _, err := Compile(spec, api.MustLimitsFor(api.PlanHobby).Operations); err != nil {
		t.Fatal(err)
	}
}

// adr: 638
func TestOperationHTTPTransactionVersionIsExplicitAndImmutable(t *testing.T) {
	limits := api.MustLimitsFor(api.PlanPro).Operations
	spec := testSpec()
	ordinary, err := Compile(spec, limits)
	if err != nil {
		t.Fatal(err)
	}
	spec.HTTPTransactionVersion = api.OperationHTTPTransactionVersion
	transaction, err := Compile(spec, limits)
	if err != nil || transaction.Revision == ordinary.Revision {
		t.Fatalf("transaction negotiation was not pinned in definition revision: %v", err)
	}
	for _, version := range []int{-1, 2} {
		spec.HTTPTransactionVersion = version
		if _, err := Compile(spec, limits); err == nil {
			t.Fatalf("unsupported version %d compiled", version)
		}
	}
}

func TestOperationDefinitionAndIdentityAreStable(t *testing.T) {
	spec := testSpec()
	limits := api.MustLimitsFor(api.PlanHobby).Operations
	a, err := Compile(spec, limits)
	if err != nil {
		t.Fatal(err)
	}
	spec.Recovery = api.OperationRecoveryReconcile
	b, err := Compile(spec, limits)
	if err != nil || a.Revision != b.Revision {
		t.Fatal("default and explicit policies differ")
	}
	spec.Path = "/new-export"
	changed, err := Compile(spec, limits)
	if err != nil || changed.Revision == a.Revision {
		t.Fatal("changed definition reused revision")
	}
	for _, other := range []string{IdentityScope("acct", "app", "production", "bob", "export", "key"), IdentityScope("acct", "app", "staging", "alice", "export", "key"), IdentityScope("acct", "app", "production", "alice", "other", "key")} {
		if other == IdentityScope("acct", "app", "production", "alice", "export", "key") {
			t.Fatal("idempotency scope collided")
		}
	}
	if _, err := Compile(testSpec(), api.MustLimitsFor(api.PlanFree).Operations); err == nil {
		t.Fatal("free plan admits operations")
	}
}

func TestSchemaDataDoesNotBecomeResourceReference(t *testing.T) {
	spec := testSpec()
	spec.InputSchema = json.RawMessage(`{"type":"object","properties":{"$ref":{"const":"https://customer.example/data"},"value":{"const":{"$ref":"https://customer.example/literal"}}},"default":{"$ref":"not-a-schema"},"examples":[{"$id":"ordinary-data"}]}`)
	contract, err := Compile(spec, api.MustLimitsFor(api.PlanHobby).Operations)
	if err != nil {
		t.Fatal(err)
	}
	if err := contract.ValidateInput([]byte(`{"$ref":"https://customer.example/data","value":{"$ref":"https://customer.example/literal"}}`), 4096); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{`{"properties":{"value":{"$ref":"https://external.example/schema"}}}`, `{"allOf":[{"$id":"https://external.example/schema"}]}`, `{"$defs":{"x":{"$dynamicRef":"other.json"}}}`} {
		spec.InputSchema = json.RawMessage(raw)
		if _, err := Compile(spec, api.MustLimitsFor(api.PlanHobby).Operations); err == nil {
			t.Fatalf("accepted external schema resource: %s", raw)
		}
	}
}

// ADR-521: canonical inputs remain usable by an ordinary typed HTTP handler.
func TestCanonicalOperationIntegersRemainTypedHandlerCompatible(t *testing.T) {
	for _, raw := range []string{`{"completed":100,"total":1000}`, `{"total":1e3,"completed":100.00}`, `{"completed":1e2,"total":1000.0}`} {
		canonical, err := CanonicalJSON([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		var report struct {
			Completed int64 `json:"completed"`
			Total     int64 `json:"total"`
		}
		if err := json.Unmarshal(canonical, &report); err != nil || report.Completed != 100 || report.Total != 1000 {
			t.Fatalf("typed handler cannot read canonical input: %s, %v", canonical, err)
		}
	}
	raw := []byte(`{"text":"<>&"}`)
	canonical, err := CanonicalJSON(raw)
	if err != nil || len(canonical) > len(raw) {
		t.Fatalf("HTML escaping inflated ordinary input: %s, %v", canonical, err)
	}
}
