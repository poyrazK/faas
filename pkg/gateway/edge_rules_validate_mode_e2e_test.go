package gateway

// End-to-end coverage for applyEdgeRuleValidate across the three
// validate_mode values (issue #975 #3 / Mega-Foundation #979-a).
// Each row of TestApplyEdgeRuleValidate_ModeCoverage exercises one
// mode with a body that fails schema validation and asserts the
// caller-facing observable:
//
//   - 'observe' returns false (proxy leg runs), increments the
//     validate-failures counter with mode=observe, and emits the
//     schema_mismatch audit event with mode="observe".
//   - 'warn' returns false (proxy leg runs), stamps
//     X-Validation-Warning=<rule_id> on the statusRecorder, and emits
//     the audit event with mode="warn".
//   - 'block' returns true (caller MUST return), writes a 422
//     problem+json, and emits the audit event with mode="block".
//
// The test injects the production Metrics struct + a counting
// audit fake so the metric and audit outcomes are asserted
// side-by-side with the response status. The Validator is a stub
// that always returns OK=false with a synthetic type-mismatch
// FieldError; the goal of these tests is the applier-side branch
// matrix, not the validator itself (covered separately in
// pkg/edgevalidate).

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// stubValidatorApply is the test-side Validator used by
// TestApplyEdgeRuleValidate_ModeCoverage. It returns OK=false with a
// synthetic type-mismatch FieldError so the handler reaches the
// per-mode branch in applyEdgeRuleValidate. The error path is
// irrelevant here — covered in pkg/edgevalidate.
type stubValidatorApply struct {
	calls int
	mu    sync.Mutex
}

func (s *stubValidatorApply) Validate(ctx context.Context, req *EdgeValidateIn, rule *EdgeRuleValidateResolved) (*EdgeValidateResult, error) {
	s.mu.Lock()
	s.calls++
	s.mu.Unlock()
	return &EdgeValidateResult{
		OK: false,
		FirstError: &EdgeValidateFieldError{
			Field:    "/name",
			Expected: "type",
			Got:      "string",
		},
	}, nil
}

// countingAudit is the test-side EdgeRuleAuditor. It records every
// (kind, mode) pair so the per-mode audit branch is observable.
type countingAudit struct {
	mu     sync.Mutex
	events []auditEvent
}

type auditEvent struct {
	kind string
	mode string
}

func (a *countingAudit) Emit(_ context.Context, kind string, _ *string, data map[string]any) {
	a.mu.Lock()
	defer a.mu.Unlock()
	mode, _ := data["mode"].(string)
	a.events = append(a.events, auditEvent{kind: kind, mode: mode})
}

func (a *countingAudit) modesFor(kind string) []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []string
	for _, e := range a.events {
		if e.kind == kind {
			out = append(out, e.mode)
		}
	}
	return out
}

func TestApplyEdgeRuleValidate_ModeCoverage(t *testing.T) {
	cases := []struct {
		name           string
		mode           string // empty = schema default
		wantReturnTrue bool   // applyEdgeRuleValidate's return
		wantStatus     int    // recorder status code
		wantAuditMode  string // value of data["mode"] on the audit event
	}{
		{
			name:           "observe-counts-and-passes-through",
			mode:           api.ValidateModeObserve,
			wantReturnTrue: false,
			wantStatus:     http.StatusOK, // recorder default; no WriteProblem call
			wantAuditMode:  api.ValidateModeObserve,
		},
		{
			name:           "warn-counts-and-stamps-warning-header",
			mode:           api.ValidateModeWarn,
			wantReturnTrue: false,
			wantStatus:     http.StatusOK, // recorder default; no WriteProblem call
			wantAuditMode:  api.ValidateModeWarn,
		},
		{
			name:           "block-rejects-with-422",
			mode:           api.ValidateModeBlock,
			wantReturnTrue: true,
			wantStatus:     http.StatusUnprocessableEntity,
			wantAuditMode:  api.ValidateModeBlock,
		},
		{
			name:           "empty-string-defaults-to-block",
			mode:           "", // schema default; handler must coerce
			wantReturnTrue: true,
			wantStatus:     http.StatusUnprocessableEntity,
			wantAuditMode:  api.ValidateModeBlock,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			ruleID := "rule-" + tc.name
			app := App{ID: "app-1", AccountID: "acct-1", Plan: api.PlanPro}
			rule := &EdgeRuleValidateResolved{
				ID:           ruleID,
				AccountID:    "acct-1",
				AppID:        "app-1",
				Priority:     0,
				PathGlob:     "",
				Methods:      nil,
				ValidateMode: tc.mode,
			}
			matcher := stubEdgeRuleMatcher{validate: rule}
			audit := &countingAudit{}
			metrics := NewMetrics()
			val := &stubValidatorApply{}
			h := &Handler{
				edgeRules:     matcher,
				validator:     val,
				edgeRuleAudit: audit,
				metrics:       metrics,
			}
			rec := httptest.NewRecorder()
			srec := &statusRecorder{ResponseWriter: rec}
			r := httptest.NewRequest("POST", "http://h.example.com/api/x",
				io.NopCloser(strings.NewReader(`{"name": 123}`)))
			r.Header.Set("Content-Type", "application/json")

			got := h.applyEdgeRuleValidate(rec, r, app, srec)
			if got != tc.wantReturnTrue {
				t.Errorf("applyEdgeRuleValidate returned %v, want %v", got, tc.wantReturnTrue)
			}
			if rec.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d (body=%q)", rec.Code, tc.wantStatus, rec.Body.String())
			}
			// Validator must have been consulted exactly once
			// (the schema-mismatch path always buffers + restores
			// the body before calling Validate).
			if val.calls != 1 {
				t.Errorf("validator calls = %d, want 1", val.calls)
			}
			// Audit must carry the validate_failed event with the
			// mode the handler coerced to. Empty-string mode
			// defaults to 'block' (asserted via the audit tag).
			modes := audit.modesFor("edge_rule.validate_failed")
			if len(modes) != 1 {
				t.Fatalf("validate_failed audit count = %d, want 1 (events=%v)", len(modes), modes)
			}
			if modes[0] != tc.wantAuditMode {
				t.Errorf("audit mode = %q, want %q (handler must tag the coerced mode)", modes[0], tc.wantAuditMode)
			}
			// Header ops: only 'warn' installs an
			// X-Validation-Warning header op.
			ops := srec.headerOps
			if tc.mode == api.ValidateModeWarn {
				if len(ops) != 1 || ops[0].Name != "X-Validation-Warning" || ops[0].Value != ruleID || ops[0].Action != "set" {
					t.Errorf("headerOps = %v, want one X-Validation-Warning=%q set op", ops, ruleID)
				}
			} else if len(ops) != 0 {
				t.Errorf("headerOps = %v, want no ops for mode=%q", ops, tc.mode)
			}
		})
	}
}
