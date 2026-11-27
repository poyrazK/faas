package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// orgPlanSink serves a Free personal account and a Pro organization "acme",
// and records every plan change it receives.
type orgPlanSink struct {
	mu      sync.Mutex
	changes []string
}

func (s *orgPlanSink) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/v1/account":
		_ = json.NewEncoder(w).Encode(api.AccountResponse{Email: "a@b.c", Plan: "free"})
	case r.Method == http.MethodGet && r.URL.Path == "/v1/orgs/acme":
		_ = json.NewEncoder(w).Encode(api.OrgResponse{Slug: "acme", Plan: "pro"})
	case r.Method == http.MethodPatch:
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		s.mu.Lock()
		s.changes = append(s.changes, r.URL.Path+" "+body["plan"])
		s.mu.Unlock()
		_ = json.NewEncoder(w).Encode(api.AccountResponse{Plan: body["plan"]})
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func withOrgFlag(t *testing.T, org string) {
	t.Helper()
	previous := projectOrganization
	projectOrganization = org
	t.Cleanup(func() { projectOrganization = previous })
}

// TestCmdPlan_OrgChangesTheOrganizationPlan pins ADR-20261127200000:
// `gregale --org acme billing plan` changes acme's plan, not the personal
// account's, and compares against acme's plan for the downgrade prompt.
func TestCmdPlan_OrgChangesTheOrganizationPlan(t *testing.T) {
	sink := &orgPlanSink{}
	srv := httptest.NewServer(sink)
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	withOrgFlag(t, "acme")
	_, restore := captureStdout(t)
	defer restore()
	_, restoreErr := captureStderr(t)
	defer restoreErr()

	// Pro → Hobby is a downgrade for acme (the personal account is Free):
	// without --yes the non-interactive prompt refuses and nothing changes.
	if code := cmdPlan([]string{"hobby"}); code == 0 {
		t.Fatalf("cmdPlan(hobby) without --yes = 0; want the downgrade confirmation to refuse")
	}
	if len(sink.changes) != 0 {
		t.Fatalf("plan changed without confirmation: %v", sink.changes)
	}
	if code := cmdPlan([]string{"hobby", "--yes"}); code != 0 {
		t.Fatalf("cmdPlan(hobby --yes) = %d, want 0", code)
	}
	if len(sink.changes) != 1 || sink.changes[0] != "/v1/orgs/acme/plan hobby" {
		t.Fatalf("plan changes = %v, want [/v1/orgs/acme/plan hobby]", sink.changes)
	}
}
