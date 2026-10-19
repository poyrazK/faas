package main_test

// commands2_apply_rescue_test.go — pins the ADR-124 follow-up #1
// post-apply rescue signal. The wire invariant from
// cmd/apid/scan_service.go:864 is
// `gateRescuedByExclude := !preCanApply && canApply`, so the helper
// fires only when the post-exclude apply succeeded but the
// pre-exclude gate would have blocked. Reasons come from the wire
// verbatim. The render lives in a small helper (renderApplyRescue)
// so these tests can pin the wire shape without standing up the
// full deploy command (auth + scan + confirmation prompt + apply).
//
// Pins:
//
//  1. rescued=true, non-empty reasons → header + reasons joined "; "
//  2. rescued=true, empty reasons → header still renders (no
//     silent drop when the server omitted per-reason detail)
//  3. rescued=false (regardless of reasons) → no output (the
//     happy path stays silent; the printout is rescue-only)
//  4. multi-reason join uses "; " between entries (matches the
//     textual convention of the server-side wire + the planProblem
//     Detail string in commands_decompose_warn_test.go).

import (
	"bytes"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// TestRenderApplyRescue_RendersHeaderAndReasons is the load-bearing
// case: --exclude rescued a blocked gate AND the server returned
// at least one reason. The operator must see the rescue header
// AND the reason list so they understand what would have failed
// without --exclude.
func TestRenderApplyRescue_RendersHeaderAndReasons(t *testing.T) {
	apply := api.ApplyResponse{
		ProjectID: "demo",
		PlanResponse: api.PlanResponse{
			GateRescuedByExclude: true,
			CanApplyReasons:      []string{"plan_apps_over_limit", "plan_crons_over_limit"},
		},
	}

	var buf bytes.Buffer
	main.RenderApplyRescue(&buf, apply)
	out := buf.String()

	if !strings.Contains(out, "gate was rescued by --exclude") {
		t.Errorf("missing rescue header; got: %q", out)
	}
	if !strings.Contains(out, "pre-exclude would have blocked") {
		t.Errorf("missing pre-exclude footer; got: %q", out)
	}
	if !strings.Contains(out, "plan_apps_over_limit") {
		t.Errorf("missing first reason; got: %q", out)
	}
	if !strings.Contains(out, "plan_crons_over_limit") {
		t.Errorf("missing second reason; got: %q", out)
	}
	if !strings.Contains(out, "; ") {
		t.Errorf("missing reason separator '; '; got: %q", out)
	}
}

// TestRenderApplyRescue_HeaderWithoutReasons pins the case where
// the server omitted the per-reason list (empty CanApplyReasons).
// The header MUST still render — the operator deserves to see the
// rescue signal even when the server chose not to enumerate the
// pre-exclude reasons. A refactor that "simplified" the branch and
// rendered nothing on empty reasons would silently drop the signal
// for operators whose server version does not yet emit reasons.
func TestRenderApplyRescue_HeaderWithoutReasons(t *testing.T) {
	apply := api.ApplyResponse{
		ProjectID: "demo",
		PlanResponse: api.PlanResponse{
			GateRescuedByExclude: true,
			CanApplyReasons:      nil,
		},
	}

	var buf bytes.Buffer
	main.RenderApplyRescue(&buf, apply)
	out := buf.String()

	if !strings.Contains(out, "gate was rescued by --exclude") {
		t.Errorf("missing rescue header on empty reasons; got: %q", out)
	}
	// The reasons line must NOT render (no reasons to join).
	if strings.Contains(out, "reasons:") {
		t.Errorf("unexpected 'reasons:' line on empty reasons; got: %q", out)
	}
}

// TestRenderApplyRescue_QuietOnHappyPath pins the negative case:
// a normal apply (rescued=false) MUST emit nothing. The render is
// rescue-only; the common path stays silent so existing scripts
// that grep "Created project" or stream stdout into pipelines do
// not see an unexpected line.
func TestRenderApplyRescue_QuietOnHappyPath(t *testing.T) {
	apply := api.ApplyResponse{
		ProjectID: "demo",
		PlanResponse: api.PlanResponse{
			GateRescuedByExclude: false,
			CanApplyReasons:      []string{"ignored"}, // would render if branch were wrong
		},
	}

	var buf bytes.Buffer
	main.RenderApplyRescue(&buf, apply)
	if buf.Len() != 0 {
		t.Errorf("happy path wrote output; got: %q", buf.String())
	}
}

// TestRenderApplyRescue_HeaderAndReasonsCoexist pins the
// regression guard against an accidental branch swap. If someone
// refactors the helper to render reasons without the header, an
// empty-reason rescued plan would emit an empty line. The header
// is the operator-visible signal; the reasons are optional detail.
// This test pins both must coexist.
func TestRenderApplyRescue_HeaderAndReasonsCoexist(t *testing.T) {
	apply := api.ApplyResponse{
		ProjectID: "demo",
		PlanResponse: api.PlanResponse{
			GateRescuedByExclude: true,
			CanApplyReasons:      []string{"single_reason"},
		},
	}

	var buf bytes.Buffer
	main.RenderApplyRescue(&buf, apply)
	out := buf.String()

	headerIdx := strings.Index(out, "gate was rescued by --exclude")
	reasonIdx := strings.Index(out, "single_reason")
	if headerIdx < 0 {
		t.Fatalf("missing header; got: %q", out)
	}
	if reasonIdx < 0 {
		t.Fatalf("missing reason; got: %q", out)
	}
	if headerIdx > reasonIdx {
		t.Errorf("reason appeared before header (header=%d, reason=%d); got: %q",
			headerIdx, reasonIdx, out)
	}
}
