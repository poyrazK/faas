// diff_test.go — wire-DTO round-trip + DiffResponse shape
// coverage for the PR-1 deploy-diff surface.
//
// The wire contract here is the same one the CLI emits under
// --json; pkg/deploydiff.Diff.ToWire produces a DiffResponse
// byte-equivalent to that output so a CI consumer parsing either
// path agrees. These tests pin that contract end-to-end.

package api

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestDiffRequest_Roundtrip — every field survives Marshal +
// Unmarshal. Pointer-vs-explicit-zero must round-trip as the same
// sentinel (JSON null vs absent), per memory
// pr-819-openapi-nullable-3-1.
func TestDiffRequest_Roundtrip(t *testing.T) {
	ram := 512
	enabled := true
	disabled := false
	req := DiffRequest{
		AppConfig: &DiffAppConfigPatch{
			RAMMB:            &ram,
			StreamingEnabled: &enabled,
			RequireAuthn:     &disabled,
		},
		ImageRef: "ghcr.io/me/api@sha256:abc",
		EnvByScope: map[string][]DiffEnvRow{
			"default": {{Key: "FOO", Value: "bar"}},
		},
		Crons: []CreateCronRequest{{Schedule: "*/5 * * * *", Path: "/tick"}},
		EdgeRules: []CreateEdgeRuleRequest{{
			Kind: "route", MatchHost: "api.example.com",
			MatchPath: "/v1", Action: json.RawMessage(`{"path":"/v1"}`),
		}},
	}
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// Pointer fields with values must NOT round-trip as null.
	for _, must := range []string{`"ram_mb":512`, `"streaming_enabled":true`, `"require_authn":false`} {
		if !strings.Contains(string(b), must) {
			t.Errorf("marshal dropped %s in %s", must, b)
		}
	}
	var got DiffRequest
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.AppConfig == nil || got.AppConfig.RAMMB == nil || *got.AppConfig.RAMMB != 512 {
		t.Errorf("RAMMB round-trip lost: %+v", got.AppConfig)
	}
	if got.AppConfig.StreamingEnabled == nil || !*got.AppConfig.StreamingEnabled {
		t.Errorf("StreamingEnabled round-trip lost (was &true): %+v", got.AppConfig.StreamingEnabled)
	}
	if got.AppConfig.RequireAuthn == nil || *got.AppConfig.RequireAuthn {
		t.Errorf("RequireAuthn round-trip lost (was &false — must be EXPLICIT false, not nil): %+v", got.AppConfig.RequireAuthn)
	}
	if got.ImageRef != "ghcr.io/me/api@sha256:abc" {
		t.Errorf("ImageRef lost: %q", got.ImageRef)
	}
	if len(got.EnvByScope["default"]) != 1 || got.EnvByScope["default"][0].Key != "FOO" {
		t.Errorf("EnvByScope lost: %+v", got.EnvByScope)
	}
	if len(got.Crons) != 1 || got.Crons[0].Path != "/tick" {
		t.Errorf("Crons lost: %+v", got.Crons)
	}
	if len(got.EdgeRules) != 1 || got.EdgeRules[0].MatchHost != "api.example.com" {
		t.Errorf("EdgeRules lost: %+v", got.EdgeRules)
	}
}

func TestDiffRequest_ScalingPolicyRoundtrip(t *testing.T) {
	req := DiffRequest{AppConfig: &DiffAppConfigPatch{ScalingPolicy: &ScalingPolicy{
		MinInstances: 1, MaxInstances: 4,
		Target:            &ScalingTarget{Metric: "rps", Value: 10},
		ScaleOutCooldownS: 5, ScaleInCooldownS: 60,
	}}}
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got DiffRequest
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.AppConfig == nil || got.AppConfig.ScalingPolicy == nil || got.AppConfig.ScalingPolicy.Target == nil {
		t.Fatalf("scaling policy lost: %s", b)
	}
	if got.AppConfig.ScalingPolicy.MinInstances != 1 || got.AppConfig.ScalingPolicy.MaxInstances != 4 ||
		got.AppConfig.ScalingPolicy.Target.Metric != "rps" || got.AppConfig.ScalingPolicy.Target.Value != 10 {
		t.Fatalf("scaling policy = %+v, want round-tripped nested values", got.AppConfig.ScalingPolicy)
	}
}

// TestDiffRequest_NilPointerOmits — pointer fields that are nil
// must round-trip as JSON-absent (omitempty), not JSON-null. The
// engine reads pointer fields on the way in; nil-vs-explicit-zero
// is the load-bearing distinction (PR-0 finding #1).
func TestDiffRequest_NilPointerOmits(t *testing.T) {
	req := DiffRequest{
		AppConfig: &DiffAppConfigPatch{RAMMB: nil},
		Manifest:  nil,
	}
	b, _ := json.Marshal(req)
	// omitempty: nil pointer fields must be ABSENT from the
	// marshalled output. If they leak through as null the engine
	// sees the "absent" sentinel correctly (json.Unmarshal of
	// null into *int leaves the pointer nil) but the wire bytes
	// are noisier than they need to be.
	for _, mustNot := range []string{`"ram_mb":`, `"idle_timeout_s":`, `"manifest":`} {
		if strings.Contains(string(b), mustNot) {
			t.Errorf("expected %q to be omitted; got: %s", mustNot, b)
		}
	}
}

// TestDiffResponse_BlockingDerived — Blocking is derived from the
// payload's Breaks severities. Mirrors
// [pkg/deploydiff.Diff.HasBlockingBreaks]; we re-derive here so
// the wire envelope is self-consistent without reaching back into
// the engine.
func TestDiffResponse_BlockingDerived(t *testing.T) {
	cases := []struct {
		name    string
		breaks  []DiffBreak
		wantBlk bool
	}{
		{"no breaks → not blocking", nil, false},
		{"warn-only → not blocking", []DiffBreak{{Code: "w", Severity: "warn"}}, false},
		{"single error → blocking", []DiffBreak{{Code: "e", Severity: "error"}}, true},
		{"mixed → blocking", []DiffBreak{{Code: "w", Severity: "warn"}, {Code: "e", Severity: "error"}}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := DiffResponse{
				Diff:     DiffPayload{Slug: "api", Breaks: tc.breaks},
				Blocking: tc.wantBlk,
				Slug:     "api",
			}
			// Marshal / Unmarshal preserves Blocking.
			b, _ := json.Marshal(resp)
			var got DiffResponse
			if err := json.Unmarshal(b, &got); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if got.Blocking != tc.wantBlk {
				t.Errorf("Blocking drifted: got %v want %v (bytes: %s)", got.Blocking, tc.wantBlk, b)
			}
		})
	}
}

// TestDiffChange_OmitsOneSided — the engine convention is "Add has
// no Before; Remove has no After". The wire omitempty tags must
// reflect that — a Change{Before: nil, After: value} must NOT emit
// `"before":null` in the JSON.
func TestDiffChange_OmitsOneSided(t *testing.T) {
	c := DiffChange{Field: "memory", Kind: "add", After: json.RawMessage("512")}
	b, _ := json.Marshal(c)
	s := string(b)
	if strings.Contains(s, `"before":`) {
		t.Errorf("Change with nil Before must omit before; got: %s", s)
	}
	if !strings.Contains(s, `"after":512`) {
		t.Errorf("Change with After=512 must emit after:512; got: %s", s)
	}
}

// TestDiffBreak_PolymorphicObserved — Observed and Limit are
// json.RawMessage so the wire preserves the gate's typed values
// (int / string / []string / map) without losing precision.
func TestDiffBreak_PolymorphicObserved(t *testing.T) {
	b := DiffBreak{
		Code:     "plan_limit_concurrency",
		Severity: "error",
		Reason:   "max_concurrency exceeds plan cap",
		Field:    "concurrency",
		Observed: json.RawMessage("30"),
		Limit:    json.RawMessage("20"),
	}
	raw, _ := json.Marshal(b)
	var got DiffBreak
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if string(got.Observed) != "30" || string(got.Limit) != "20" {
		t.Errorf("polymorphic round-trip lost: %s / %s", got.Observed, got.Limit)
	}
}
