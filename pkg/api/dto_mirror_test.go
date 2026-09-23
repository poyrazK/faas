// Whitebox tests for the traffic-mirroring DTOs and error sentinels
// (issue #72 / ADR-125 PR-A2). Three concerns pinned:
//
//  1. JSON tag discipline — every request/response struct
//     serialises to the canonical snake_case wire shape. The
//     SDK contract (pkg/api/client.go) and the generated SDK
//     snapshots depend on stable tag names; a rename is a wire-
//     breaking change.
//
//  2. Round-trip — every request struct survives
//     Marshal → Unmarshal with all fields preserved. Catches
//     the classic `*[]string` vs `[]string` bug (a PATCH with
//     `"redact_headers": []` clears vs the omitempty omit case).
//
//  3. Error sentinel surface — every mirror constructor returns
//     the expected HTTP status + code + a Detail that contains
//     the limit + observed value (so the CLI renders actionable
//     retry guidance without consulting the docs).
//
// Companion files: dto_test.go (general), errors_sweep*.go (sweep).
package api

import (
	"encoding/json"
	"strings"
	"testing"
)

// mirrorTestLimits is a representative Limits block for the
// PlanPro plan so the quota-exceeded prose carries the cap
// (MirrorTargetsPerApp: 1) rather than the zero default. Mirrors
// testSidecarLimits' shape; copied locally to keep this test
// file independent of the sidecar test helpers.
func mirrorTestLimits() Limits {
	return Limits{
		Plan:                PlanPro,
		RAMMB:               512,
		MirrorRuleAllowed:   true,
		MirrorTargetsPerApp: 1,
		MaxConcurrency:      5,
		DeployedApps:        25,
		EnvValueMaxBytes:    1 << 20,
		AppLayerMaxMB:       1024,
	}
}

func TestCreateMirrorRuleRequest_RoundTrip(t *testing.T) {
	percent := 25
	in := CreateMirrorRuleRequest{
		SourceDeploymentID: "11111111-1111-1111-1111-111111111111",
		MirrorDeploymentID: "22222222-2222-2222-2222-222222222222",
		Percent:            &percent,
		IncludeBody:        true,
		RedactHeaders:      []string{"X-Tenant-Id", "X-Trace-Id"},
	}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, want := range []string{
		`"source_deployment_id":"11111111-1111-1111-1111-111111111111"`,
		`"mirror_deployment_id":"22222222-2222-2222-2222-222222222222"`,
		`"percent":25`,
		`"include_body":true`,
		`"redact_headers":["X-Tenant-Id","X-Trace-Id"]`,
	} {
		if !strings.Contains(string(b), want) {
			t.Errorf("Marshal missing %s in %s", want, string(b))
		}
	}
	var out CreateMirrorRuleRequest
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.SourceDeploymentID != in.SourceDeploymentID || out.MirrorDeploymentID != in.MirrorDeploymentID ||
		out.Percent == nil || *out.Percent != *in.Percent || out.IncludeBody != in.IncludeBody ||
		len(out.RedactHeaders) != len(in.RedactHeaders) {
		t.Fatalf("round-trip mismatch: %+v vs %+v", out, in)
	}
}

func TestUpdateMirrorRuleRequest_PatchSemantics(t *testing.T) {
	// Pointer-field round-trip — verifies the absent vs zero
	// distinction survives JSON. Critical: a PATCH with an
	// omitted "percent" field must NOT default to 0; the handler
	// reads pointer-nil as "absent". The `omitempty` tag on
	// every field is what guarantees the marshal side skips
	// nil pointers; this test pins that contract.
	b, err := json.Marshal(UpdateMirrorRuleRequest{})
	if err != nil {
		t.Fatalf("marshal empty: %v", err)
	}
	if string(b) != "{}" {
		t.Errorf("empty UpdateMirrorRuleRequest must serialise to {}, got %s", string(b))
	}

	p100 := 100
	enabled := true
	includeBody := true
	headers := []string{"X-Foo"}
	in := UpdateMirrorRuleRequest{
		Percent:       &p100,
		Enabled:       &enabled,
		IncludeBody:   &includeBody,
		RedactHeaders: &headers,
	}
	b, err = json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal full: %v", err)
	}
	for _, want := range []string{
		`"percent":100`,
		`"enabled":true`,
		`"include_body":true`,
		`"redact_headers":["X-Foo"]`,
	} {
		if !strings.Contains(string(b), want) {
			t.Errorf("marshal full missing %s in %s", want, string(b))
		}
	}
	var out UpdateMirrorRuleRequest
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Percent == nil || *out.Percent != 100 {
		t.Errorf("Percent: got %+v, want &100", out.Percent)
	}
	if out.RedactHeaders == nil || len(*out.RedactHeaders) != 1 {
		t.Errorf("RedactHeaders: got %+v, want &[X-Foo]", out.RedactHeaders)
	}
}

func TestMirrorRuleResponse_AlwaysStrippedHeaders(t *testing.T) {
	// The AlwaysStrippedHeaders field is the customer's source
	// of truth for "what the gateway always strips" — it must
	// round-trip through JSON AND must always carry the canonical
	// six headers regardless of the customer's RedactHeaders.
	in := MirrorRuleResponse{
		ID:                    "mr-1",
		SourceDeploymentID:    "src",
		MirrorDeploymentID:    "mir",
		Percent:               10,
		RedactHeaders:         []string{"X-Custom"},
		AlwaysStrippedHeaders: MirrorAlwaysStrippedHeaders,
	}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if len(MirrorAlwaysStrippedHeaders) != 6 {
		t.Fatalf("MirrorAlwaysStrippedHeaders changed shape: got %d entries, want 6 (Authorization, Cookie, Set-Cookie, X-API-Key, Proxy-Authorization, WWW-Authenticate)", len(MirrorAlwaysStrippedHeaders))
	}
	for _, want := range []string{
		`"always_stripped_headers"`,
		`"Authorization"`,
		`"Cookie"`,
		`"X-API-Key"`,
	} {
		if !strings.Contains(string(b), want) {
			t.Errorf("Marshal missing %s", want)
		}
	}
}

func TestParseMirrorWindow(t *testing.T) {
	cases := []struct {
		in      string
		want    MirrorWindowDuration
		wantErr bool
	}{
		{"", MirrorWindow1h, false},
		{"1h", MirrorWindow1h, false},
		{"24h", MirrorWindow24h, false},
		{"7d", MirrorWindow7d, false},
		{"garbage", 0, true},
		{"2h", 0, true},
		{"30m", 0, true},
	}
	for _, c := range cases {
		got, err := ParseMirrorWindow(c.in)
		if (err != nil) != c.wantErr {
			t.Errorf("ParseMirrorWindow(%q): err = %v, wantErr = %v", c.in, err, c.wantErr)
			continue
		}
		if !c.wantErr && got != c.want {
			t.Errorf("ParseMirrorWindow(%q): got %d, want %d", c.in, got, c.want)
		}
	}
}

func TestParseMirrorWindow_SentinelExported(t *testing.T) {
	// The unexported sentinel is what internal callers
	// errors.Is against. We assert it's non-nil and carries
	// the canonical hint so a future rename of the input
	// vocabulary is caught here.
	_, err := ParseMirrorWindow("nonsense")
	if err == nil {
		t.Fatal("expected error for nonsense input")
	}
	if !strings.Contains(err.Error(), "1h, 24h, or 7d") {
		t.Errorf("parse-error message lost the vocabulary hint: %q", err.Error())
	}
}

func TestMirrorSentinels_StatusAndCode(t *testing.T) {
	// Each constructor must return the status + code pair
	// promised in errors.go. Pinning these in a test catches
	// accidental swaps (e.g. 403 ↔ 422) during a refactor.
	limits := mirrorTestLimits()
	cases := []struct {
		name    string
		problem *Problem
		status  int
		code    string
		detail  string
	}{
		{"plan gate", ErrPlanMirrorNotAllowed(PlanHobby), 403, CodePlanMirrorNotAllowed, "hobby"},
		{"quota exceeded", ErrMirrorRuleQuotaExceeded(limits, 3), 422, CodeMirrorRuleQuotaExceeded, "3"},
		{"invalid percent high", ErrInvalidMirrorPercent(150), 422, CodeInvalidMirrorPercent, "150"},
		{"invalid percent negative", ErrInvalidMirrorPercent(-5), 422, CodeInvalidMirrorPercent, "-5"},
		{"source == target", ErrMirrorSourceTargetSame(), 422, CodeMirrorSourceTargetSame, "source_deployment_id"},
		{"deployment not live", ErrMirrorDeploymentNotLive(), 409, CodeMirrorDeploymentNotLive, "live"},
		{"cross-app mismatch", ErrMirrorCrossAppMismatch(), 422, CodeMirrorCrossAppMismatch, "same app"},
		{"rule not found", ErrMirrorRuleNotFound("mr-x"), 404, CodeMirrorRuleNotFound, "mr-x"},
		{"invalid window", ErrInvalidMirrorWindow("2h"), 422, CodeInvalidMirrorWindow, "2h"},
	}
	for _, c := range cases {
		if c.problem.Status != c.status {
			t.Errorf("%s: status = %d, want %d", c.name, c.problem.Status, c.status)
		}
		if c.problem.Code != c.code {
			t.Errorf("%s: code = %q, want %q", c.name, c.problem.Code, c.code)
		}
		if !strings.Contains(c.problem.Detail, c.detail) {
			t.Errorf("%s: detail %q missing %q", c.name, c.problem.Detail, c.detail)
		}
		if c.problem.Title == "" {
			t.Errorf("%s: empty title", c.name)
		}
		if c.problem.DocsURL == "" {
			t.Errorf("%s: empty docs URL", c.name)
		}
	}
}

func TestErrMirrorRuleQuotaExceeded_WithLimitShape(t *testing.T) {
	// The WithLimit envelope is what the CLI uses to render
	// "you have N of M" retry guidance. Pin the shape so a
	// future refactor doesn't silently drop the cap/observed
	// pair (which would regress the CLI's actionable error).
	l := mirrorTestLimits()
	p := ErrMirrorRuleQuotaExceeded(l, 2)
	if p.Limit == nil || *p.Limit != int64(l.MirrorTargetsPerApp) {
		t.Errorf("Limit: got %+v, want &%d", p.Limit, l.MirrorTargetsPerApp)
	}
	if p.Observed == nil || *p.Observed != 2 {
		t.Errorf("Observed: got %+v, want &2", p.Observed)
	}
}
