// Tests for the PR #993 / issue #757 review MED-4 caps + TLS gate.
// Scope: the helpers (kafkaSkipVerifyRequested) and the error
// constructors — the heavyweight createTrigger/updateTrigger HTTP
// flow has no test surface in this package (the existing handler
// tests live in pkg/apidhttp or test against a fully wired server).
// The helper unit test pins the (kind, blob) → bool extraction that
// the handler branches on; the error test pins the wire shape
// (status, code, WithLimit) so the CLI / SDK error-message contract
// stays stable.
//
// Tests live in their own file (handlers_triggers_caps_test.go)
// rather than as a new test block inside handlers_triggers_test.go
// because the production file has no parallel _test.go — keeping
// this review-fix test in its own file avoids touching a 1k+ LOC
// handler source for unrelated test plumbing.
package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestKafkaSkipVerifyRequested is the MED-4 plan-gate unit test.
// The handler calls this for every createTrigger / updateTrigger
// request and rejects the request when:
//
//	kind == kafka
//	tls.skip_verify == true
//	plan.TLSSkipVerifyAllowed() == false
//
// This helper extracts just the bool the handler needs; the plan
// gate lives at the callsite because the handler holds the
// account, and account → plan is the Plan type's job.
func TestKafkaSkipVerifyRequested(t *testing.T) {
	cases := []struct {
		name    string
		kind    api.TriggerKind
		raw     string
		want    bool
		wantErr bool
	}{
		{
			name: "kafka_with_skip_verify_true",
			kind: api.TriggerKindKafka,
			raw:  `{"brokers":["b1:9092"],"topic":"t","group":"g","tls":{"skip_verify":true}}`,
			want: true,
		},
		{
			name: "kafka_with_skip_verify_false",
			kind: api.TriggerKindKafka,
			raw:  `{"brokers":["b1:9092"],"topic":"t","group":"g","tls":{"skip_verify":false}}`,
			want: false,
		},
		{
			name: "kafka_no_tls_block",
			kind: api.TriggerKindKafka,
			raw:  `{"brokers":["b1:9092"],"topic":"t","group":"g"}`,
			want: false,
		},
		{
			name: "kafka_empty_blob",
			kind: api.TriggerKindKafka,
			raw:  "",
			want: false,
		},
		{
			name: "nats_skipped_unconditionally",
			kind: api.TriggerKindNATS,
			// Even with a skip_verify-shaped blob, NATS doesn't
			// read it. The handler must not gate on this; the
			// 403 is kafka-specific.
			raw: `{"url":"nats://n","stream":"s","subject":"x","durable":"d","tls":{"skip_verify":true}}`,
			// NATS doesn't have a tls block in the typed config,
			// but the helper short-circuits on kind first so the
			// raw blob doesn't matter.
			want: false,
		},
		{
			name: "queue_skipped_unconditionally",
			kind: api.TriggerKindQueue,
			raw:  `{"url":"q","tls":{"skip_verify":true}}`,
			want: false,
		},
		{
			name:    "malformed_blob_returns_error",
			kind:    api.TriggerKindKafka,
			raw:     `{`,
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := kafkaSkipVerifyRequested(tc.kind, json.RawMessage(tc.raw))
			if tc.wantErr {
				if err == nil {
					t.Fatalf("err = nil, want parse error")
				}
				return
			}
			if err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			if got != tc.want {
				t.Errorf("kafkaSkipVerifyRequested = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestErrTriggerBatchWindowTooLarge_WireShape pins the error
// envelope that the CLI / SDK parse. Body must carry the plan cap
// and the observed value so the user knows what to lower the
// batch_window to. WithLimit surfaces them as the standard
// "limit"/"observed" pair the rest of the quota envelope uses.
func TestErrTriggerBatchWindowTooLarge_WireShape(t *testing.T) {
	p := api.ErrTriggerBatchWindowTooLarge(api.PlanHobby, 30, 600)
	if p.Status != http.StatusForbidden {
		t.Errorf("Status = %d, want 403", p.Status)
	}
	if p.Code != api.CodeTriggerBatchWindowTooLarge {
		t.Errorf("Code = %q, want %q", p.Code, api.CodeTriggerBatchWindowTooLarge)
	}
	if p.Limit == nil || *p.Limit != 30 {
		t.Errorf("Limit = %v, want 30", p.Limit)
	}
	if p.Observed == nil || *p.Observed != 600 {
		t.Errorf("Observed = %v, want 600", p.Observed)
	}
	if !strings.Contains(p.Detail, "hobby") || !strings.Contains(p.Detail, "30") || !strings.Contains(p.Detail, "600") {
		t.Errorf("Detail = %q, want hobby/30/600 in copy", p.Detail)
	}
}

// TestErrTriggerTLSSkipVerifyNotAllowed_WireShape pins the
// trigger_tls_skip_verify_not_allowed envelope. Plan code in the
// copy so the CLI can branch on it; no WithLimit because the gate
// is binary (skip_verify was requested → reject) rather than a
// quota observation.
func TestErrTriggerTLSSkipVerifyNotAllowed_WireShape(t *testing.T) {
	p := api.ErrTriggerTLSSkipVerifyNotAllowed(api.PlanHobby)
	if p.Status != http.StatusForbidden {
		t.Errorf("Status = %d, want 403", p.Status)
	}
	if p.Code != api.CodeTriggerTLSSkipVerifyNotAllowed {
		t.Errorf("Code = %q, want %q", p.Code, api.CodeTriggerTLSSkipVerifyNotAllowed)
	}
	if !strings.Contains(p.Detail, "hobby") || !strings.Contains(p.Detail, "skip_verify") {
		t.Errorf("Detail = %q, want hobby+skip_verify in copy", p.Detail)
	}
	// The detail must mention the upgrade path so the CLI can
	// print actionable advice without grepping the docs URL.
	if !strings.Contains(p.Detail, "Pro") {
		t.Errorf("Detail = %q, want Pro upgrade hint", p.Detail)
	}
}

// TestPlanTLSSkipVerifyAllowed_FreeAndHobbyClosed pins the
// plan-side gate. PR #993 review MED-4 spec: Free + Hobby=false,
// Pro + Scale=true. The handler depends on this helper returning
// false for the closed plans and true for the open plans; an
// accidental swap would silently weaken / harden the gate.
func TestPlanTLSSkipVerifyAllowed_FreeAndHobbyClosed(t *testing.T) {
	cases := []struct {
		plan api.Plan
		want bool
	}{
		{api.PlanFree, false},
		{api.PlanHobby, false},
		{api.PlanPro, true},
		{api.PlanScale, true},
	}
	for _, tc := range cases {
		t.Run(string(tc.plan), func(t *testing.T) {
			if got := tc.plan.TLSSkipVerifyAllowed(); got != tc.want {
				t.Errorf("Plan(%s).TLSSkipVerifyAllowed = %v, want %v", tc.plan, got, tc.want)
			}
		})
	}
}

func TestErrTriggerKindNotAllowedWireShape(t *testing.T) {
	p := api.ErrTriggerKindNotAllowed(api.PlanHobby, api.TriggerKindKafka)
	if p.Status != http.StatusForbidden {
		t.Errorf("Status = %d, want 403", p.Status)
	}
	if p.Code != api.CodeTriggerKindNotAllowed {
		t.Errorf("Code = %q, want %q", p.Code, api.CodeTriggerKindNotAllowed)
	}
	if !strings.Contains(p.Detail, "hobby") || !strings.Contains(p.Detail, "kafka") {
		t.Errorf("Detail = %q, want hobby and kafka", p.Detail)
	}
}

func TestCreateTriggerRejectsKindOutsidePlan(t *testing.T) {
	e := setup(t, api.PlanHobby)
	app, err := e.store.CreateApp(t.Context(), state.App{AccountID: e.acct.ID, Slug: "hobby-app"})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	rec := e.do(t, http.MethodPost, "/v1/triggers", api.CreateTriggerRequest{
		AppID:  app.ID,
		Kind:   api.TriggerKindKafka,
		Slug:   "orders",
		Config: json.RawMessage(`{"brokers":["b:9092"],"topic":"orders","group":"g"}`),
	}, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", rec.Code, rec.Body.String())
	}
	var problem api.Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil {
		t.Fatalf("decode problem: %v", err)
	}
	if problem.Code != api.CodeTriggerKindNotAllowed {
		t.Fatalf("code = %q, want %q", problem.Code, api.CodeTriggerKindNotAllowed)
	}
}

func TestCreateTriggerAcceptsAllowedKafkaConfig(t *testing.T) {
	e := setup(t, api.PlanPro)
	app, err := e.store.CreateApp(t.Context(), state.App{AccountID: e.acct.ID, Slug: "pro-app"})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	rec := e.do(t, http.MethodPost, "/v1/triggers", api.CreateTriggerRequest{
		AppID:  app.ID,
		Kind:   api.TriggerKindKafka,
		Slug:   "orders",
		Config: json.RawMessage(`{"brokers":["b:9092"],"topic":"orders","group":"g"}`),
	}, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body.String())
	}
}

func TestBatchCreateTriggerRejectsKindOutsidePlan(t *testing.T) {
	e := setup(t, api.PlanHobby)
	app, err := e.store.CreateApp(t.Context(), state.App{AccountID: e.acct.ID, Slug: "hobby-app"})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	rec := e.do(t, http.MethodPost, "/v1/triggers:batch_create", api.CreateTriggerBatchRequest{
		AppID: app.ID,
		ManifestYAML: `triggers:
  - kind: kafka
    app: hobby-app
    slug: orders
    config:
      brokers: ["b:9092"]
      topic: orders
      group: g
`,
	}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var out batchCreateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(out.Created) != 0 || len(out.Errors) != 1 || !strings.Contains(out.Errors[0].Message, "kafka") {
		t.Fatalf("batch result = %+v, want one kafka plan error", out)
	}
}
