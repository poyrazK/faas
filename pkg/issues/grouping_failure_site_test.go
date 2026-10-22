package issues

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 1111
// Paired failures exercise false splits, false merges, transport parity, and
// explicit controls, independently of the fingerprint's encoding.
func TestIssueFailureSiteGroupingCorpus(t *testing.T) {
	now := time.Now().UTC()
	base := api.IssueEvent{
		EventID: uuid.NewString(), OccurredAt: now, ExceptionType: "Error",
		Message: "customer 123 failed", GroupingStrategy: FailureSiteStrategy,
		Frames: []api.IssueFrame{
			{File: "/app/src/checkout.ts", Function: "charge", Line: 10, InApp: true},
			{File: "/app/src/handler.ts", Function: "post", Line: 20, InApp: true},
		},
	}
	cases := []struct {
		name   string
		same   bool
		change func(*api.IssueEvent)
	}{
		{"different callers", true, func(e *api.IssueEvent) {
			e.Frames[1].Function = "worker"
			e.Frames = append(e.Frames, api.IssueFrame{File: "/app/jobs.ts", Function: "retry", InApp: true})
		}},
		{"line edits and build roots", true, func(e *api.IssueEvent) { e.Frames[0].File = "/build/release43/src/checkout.ts"; e.Frames[0].Line = 99 }},
		{"dynamic numbers", true, func(e *api.IssueEvent) { e.Message = "customer 789 failed" }},
		{"message whitespace", true, func(e *api.IssueEvent) { e.Message = "customer  123\nfailed" }},
		{"same function different failure", false, func(e *api.IssueEvent) { e.Message = "payment provider unavailable" }},
		{"different function", false, func(e *api.IssueEvent) { e.Frames[0].Function = "refund" }},
		{"same filename different directory", false, func(e *api.IssueEvent) { e.Frames[0].File = "/app/other/checkout.ts" }},
		{"different type", false, func(e *api.IssueEvent) { e.ExceptionType = "TypeError" }},
		{"worker source", false, func(e *api.IssueEvent) { e.SourceKind = "worker" }},
		{"dependency frames ignored", true, func(e *api.IssueEvent) {
			e.Frames = append([]api.IssueFrame{{File: "/app/node_modules/parser.js", Function: "run"}}, e.Frames...)
		}},
		{"python frame order", true, func(e *api.IssueEvent) { slices.Reverse(e.Frames); e.FrameOrder = "oldest-first" }},
		{"legacy version isolated", false, func(e *api.IssueEvent) { e.GroupingStrategy = "" }},
	}
	fingerprint := func(e api.IssueEvent) string {
		t.Helper()
		_, fp, _, err := Normalize(e, now, api.PlanHobby.IssueLimits())
		if err != nil {
			t.Fatal(err)
		}
		return fp
	}
	first := fingerprint(base)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			next := base
			next.Frames = slices.Clone(base.Frames)
			tc.change(&next)
			if same := first == fingerprint(next); same != tc.same {
				t.Fatalf("same group = %v, want %v", same, tc.same)
			}
		})
	}
	t.Run("stable codes replace messages", func(t *testing.T) {
		a, b := base, base
		a.ErrorCode, b.ErrorCode = "PAYMENT_404", "PAYMENT_404"
		if got := fingerprint(a); got != "8f2bf21c49bdc81936d974388da0a2ef2ec58fac7b12078736d50277be952034" {
			t.Fatalf("version 2 identity changed without a version bump: %s", got)
		}
		b.Message = "translated or changed wording"
		if fingerprint(a) != fingerprint(b) {
			t.Fatal("same stable code split")
		}
		b.ErrorCode = "PAYMENT_500"
		if fingerprint(a) == fingerprint(b) {
			t.Fatal("meaningful numeric codes merged")
		}
	})
	t.Run("explicit override takes precedence", func(t *testing.T) {
		a, b := base, base
		a.FingerprintOverride, b.FingerprintOverride = "checkout-payment", "checkout-payment"
		b.Frames = nil
		b.ErrorCode = "OTHER"
		b.Message = "other"
		if fingerprint(a) != fingerprint(b) {
			t.Fatal("override did not group")
		}
		b.FingerprintOverride = "refund"
		if fingerprint(a) == fingerprint(b) {
			t.Fatal("distinct overrides merged")
		}
	})
	t.Run("fallback keeps route and status", func(t *testing.T) {
		a, b := base, base
		a.Frames, b.Frames = nil, nil
		a.Route, b.Route = "/checkout", "/checkout"
		a.HTTPStatus, b.HTTPStatus = 500, 500
		b.Message = "customer 456 failed"
		if fingerprint(a) != fingerprint(b) {
			t.Fatal("dynamic fallback split")
		}
		b.HTTPStatus = 502
		if fingerprint(a) == fingerprint(b) {
			t.Fatal("different fallback statuses merged")
		}
		b.HTTPStatus, b.Route = 500, "/refund"
		if fingerprint(a) == fingerprint(b) {
			t.Fatal("different fallback routes merged")
		}
	})
	t.Run("frame component boundaries", func(t *testing.T) {
		a, b := base, base
		a.Frames = []api.IssueFrame{{File: "/app/src/checkout.ts:charge", Function: "helper", InApp: true}}
		b.Frames = []api.IssueFrame{{File: "/app/src/checkout.ts", Function: "charge:helper", InApp: true}}
		if fingerprint(a) == fingerprint(b) {
			t.Fatal("file/function separator collision merged distinct sites")
		}
	})
}

func TestIssueLegacyFingerprintGolden(t *testing.T) {
	now := time.Now().UTC()
	for _, tc := range []struct {
		name  string
		event api.IssueEvent
		want  string
	}{
		{"frames", api.IssueEvent{Frames: []api.IssueFrame{{File: "/app/src/checkout.ts", Function: "charge", InApp: true}, {File: "/app/src/handler.ts", Function: "post", InApp: true}}}, "68cdab030b4cf5876e07c47c14eea99edb3e08d7d4801ffcf79f8d421f8b8c67"},
		{"fallback", api.IssueEvent{Route: "/checkout", HTTPStatus: 500, Message: "customer 123 failed"}, "f25bdd7048d71edd929f9adaf09b1eb4a00f45cb0f4b7b7ed9d192ffa4cb14c9"},
		{"override", api.IssueEvent{FingerprintOverride: "checkout-payment"}, "f6dd1de69906fcca70658200dff18a7d134203b4b683a02d82d4de604fcaede6"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.event.EventID, tc.event.OccurredAt, tc.event.ExceptionType = uuid.NewString(), now, "Error"
			for _, strategy := range []string{"", "legacy"} {
				tc.event.GroupingStrategy = strategy
				// New optional metadata must not change version 1 identities.
				tc.event.ErrorCode, tc.event.FrameOrder = "NEW_CODE", "oldest-first"
				out, fp, _, err := Normalize(tc.event, now, api.PlanHobby.IssueLimits())
				if err != nil || fp != tc.want || GroupingVersionFor(out) != 1 {
					t.Fatalf("legacy identity changed: %s, %v", fp, err)
				}
			}
		})
	}
}

func TestIssueFailureSiteParsedStackParity(t *testing.T) {
	now := time.Now().UTC()
	for _, tc := range []struct {
		name, stack, order string
		frames             []api.IssueFrame
	}{
		{"Node anonymous", "Error: fail\n    at /app/fail.js:10:5\n    at handler (/app/handler.js:20:5)", "newest-first", []api.IssueFrame{{File: "/app/fail.js", Function: "<anonymous>", Line: 10, InApp: true}, {File: "/app/handler.js", Function: "handler", Line: 20, InApp: true}}},
		{"Python", "Traceback (most recent call last):\n  File \"/app/handler.py\", line 20, in handler\n  File \"/app/fail.py\", line 10, in fail\nValueError: fail", "oldest-first", []api.IssueFrame{{File: "/app/handler.py", Function: "handler", Line: 20, InApp: true}, {File: "/app/fail.py", Function: "fail", Line: 10, InApp: true}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			event := api.IssueEvent{EventID: uuid.NewString(), OccurredAt: now, ExceptionType: "Error", Message: "fail", GroupingStrategy: FailureSiteStrategy, StackTrace: tc.stack}
			parsed, fp, _, err := Normalize(event, now, api.PlanHobby.IssueLimits())
			if err != nil || parsed.FrameOrder != tc.order {
				t.Fatalf("parse order: %+v %v", parsed, err)
			}
			event.Frames, event.FrameOrder = tc.frames, tc.order
			out, structured, _, err := Normalize(event, now, api.PlanHobby.IssueLimits())
			if err != nil || structured != fp || GroupingVersionFor(out) != 2 {
				t.Fatalf("SDK/parsed stack grouping mismatch: %s %s %v", fp, structured, err)
			}
		})
	}
	t.Run("deep chained Python stack retains final failure", func(t *testing.T) {
		var stack strings.Builder
		for i := 0; i < 40; i++ {
			fmt.Fprintf(&stack, "  File \"/app/caller.py\", line %d, in caller\n", i+1)
		}
		stack.WriteString("ValueError: first\nDuring handling of the above exception, another exception occurred:\n  File \"/app/final.py\", line 50, in fail\nRuntimeError: final")
		event := api.IssueEvent{EventID: uuid.NewString(), OccurredAt: now, ExceptionType: "RuntimeError", Message: "final", GroupingStrategy: FailureSiteStrategy, StackTrace: stack.String()}
		parsed, fp, _, err := Normalize(event, now, api.PlanHobby.IssueLimits())
		if err != nil || len(parsed.Frames) != api.IssueMaxFrames {
			t.Fatalf("bounded parsing failed: %v", err)
		}
		event.Frames, event.FrameOrder = []api.IssueFrame{{File: "/app/final.py", Function: "fail", Line: 50, InApp: true}}, "oldest-first"
		_, want, _, err := Normalize(event, now, api.PlanHobby.IssueLimits())
		if err != nil || fp != want {
			t.Fatal("final exception failure site lost")
		}
	})
}

func TestIssueGroupingControlsValidateAndRedact(t *testing.T) {
	now := time.Now().UTC()
	base := api.IssueEvent{EventID: uuid.NewString(), OccurredAt: now, ExceptionType: "Error", GroupingStrategy: FailureSiteStrategy}
	for _, mutate := range []func(*api.IssueEvent){
		func(e *api.IssueEvent) { e.GroupingStrategy = "unknown" },
		func(e *api.IssueEvent) { e.FrameOrder = "unknown" },
		func(e *api.IssueEvent) { e.ErrorCode = strings.Repeat("a", api.IssueMaxTypeBytes+1) },
		func(e *api.IssueEvent) { e.ErrorCode = string([]byte{0xff}) },
	} {
		next := base
		mutate(&next)
		if _, _, _, err := Normalize(next, now, api.PlanHobby.IssueLimits()); err == nil {
			t.Fatal("invalid grouping control accepted")
		}
	}
	base.ErrorCode = "alice@example.com"
	out, fp, _, err := Normalize(base, now, api.PlanHobby.IssueLimits())
	if err != nil || strings.Contains(out.ErrorCode, "@example.com") || len(out.Redactions) == 0 {
		t.Fatalf("code not sanitized: %+v %v", out, err)
	}
	_, sanitized, _, err := Normalize(out, now, api.PlanHobby.IssueLimits())
	if err != nil || fp != sanitized {
		t.Fatal("fingerprint was derived before code redaction")
	}
}
