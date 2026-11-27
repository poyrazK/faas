package main_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// orgPlanSink serves a Free personal account and a Pro organization "acme",
// and records every plan change it receives. With checkout set, a plan change
// answers 402 with a checkout URL instead.
type orgPlanSink struct {
	mu       sync.Mutex
	checkout bool
	changes  []string
}

func (s *orgPlanSink) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/v1/account":
		main.WriteJSONTest(w, api.AccountResponse{Email: "a@b.c", Plan: "free"})
	case r.Method == http.MethodGet && r.URL.Path == "/v1/orgs/acme":
		main.WriteJSONTest(w, api.OrgResponse{Slug: "acme", Plan: "pro"})
	case r.Method == http.MethodPatch:
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		s.mu.Lock()
		s.changes = append(s.changes, r.URL.Path+" "+body["plan"])
		s.mu.Unlock()
		if s.checkout {
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusPaymentRequired)
			_ = json.NewEncoder(w).Encode(api.Problem{
				Status: http.StatusPaymentRequired, Code: api.CodePayment, Title: "Billing subscription required",
				Detail: "update the payment method", BillingPortalURL: "https://billing.example/portal",
			})
			return
		}
		main.WriteJSONTest(w, api.AccountResponse{Plan: body["plan"]})
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func startOrgPlanSink(t *testing.T, sink *orgPlanSink) {
	t.Helper()
	srv := httptest.NewServer(sink)
	t.Cleanup(srv.Close)
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	main.ResetJSONOut(t)
}

// TestPlanUnderOrgChangesTheOrganizationPlan pins ADR-20261127200000:
// `gregale --org acme billing plan` changes acme's plan, not the personal
// account's, and compares against acme's plan for the downgrade prompt.
func TestPlanUnderOrgChangesTheOrganizationPlan(t *testing.T) {
	sink := &orgPlanSink{}
	startOrgPlanSink(t, sink)
	_, restore := main.CaptureStdout(t)
	defer restore()
	_, restoreErr := main.CaptureStderr(t)
	defer restoreErr()

	// Pro → Hobby is a downgrade for acme (the personal account is Free):
	// without --yes the non-interactive prompt refuses and nothing changes.
	if code := main.Run([]string{"--org", "acme", "billing", "plan", "hobby"}); code == 0 {
		t.Fatalf("billing plan hobby without --yes = 0; want the downgrade confirmation to refuse")
	}
	if len(sink.changes) != 0 {
		t.Fatalf("plan changed without confirmation: %v", sink.changes)
	}
	if code := main.Run([]string{"--org", "acme", "billing", "plan", "hobby", "--yes"}); code != 0 {
		t.Fatalf("billing plan hobby --yes = %d, want 0", code)
	}
	if len(sink.changes) != 1 || sink.changes[0] != "/v1/orgs/acme/plan hobby" {
		t.Fatalf("plan changes = %v, want [/v1/orgs/acme/plan hobby]", sink.changes)
	}
}

// TestPlanUnderOrgHintsKeepTheOrg: the retry line after a billing hand-off
// keeps --org, so pasting it does not change the personal account's plan.
func TestPlanUnderOrgHintsKeepTheOrg(t *testing.T) {
	startOrgPlanSink(t, &orgPlanSink{checkout: true})
	main.WithRecorder(t)
	stdout, restore := main.CaptureStdout(t)
	defer restore()
	_, restoreErr := main.CaptureStderr(t)
	defer restoreErr()

	main.Run([]string{"--org", "acme", "billing", "plan", "scale"})
	if out := stdout.String(); !strings.Contains(out, "Then retry: gregale --org acme billing plan scale") {
		t.Fatalf("stdout = %q, want the retry line to keep --org acme", out)
	}
}
