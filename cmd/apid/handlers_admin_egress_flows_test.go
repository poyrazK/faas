// adr: 369 — egress flow log lookup.
package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestParseEgressFlowFilter(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	parse := func(query string) (state.EgressFlowFilter, *api.Problem) {
		return parseEgressFlowFilter(httptest.NewRequest(http.MethodGet, "/v1/admin/egress-flows?"+query, nil), now)
	}
	f, prob := parse("")
	if prob != nil || !f.To.Equal(now) || !f.From.Equal(now.Add(-24*time.Hour)) || f.Limit != 200 || f.Remote.IsValid() {
		t.Fatalf("defaults = %+v, %v", f, prob)
	}
	if f, prob = parse("remote=198.51.100.10"); prob != nil || f.Remote != netip.MustParsePrefix("198.51.100.10/32") {
		t.Fatalf("host remote = %v, %v", f.Remote, prob)
	}
	if f, prob = parse("remote=198.51.100.7/24&account_id=acct-1&limit=5"); prob != nil ||
		f.Remote != netip.MustParsePrefix("198.51.100.0/24") || f.AccountID != "acct-1" || f.Limit != 5 {
		t.Fatalf("cidr + account + limit = %+v, %v", f, prob)
	}
	for _, bad := range []string{
		"remote=not-an-ip", "from=yesterday", "from=2026-09-29T13:00:00Z",
		"from=2026-07-01T00:00:00Z", "limit=0", "limit=1001",
	} {
		if _, prob := parse(bad); prob == nil || prob.Code != api.CodeValidation {
			t.Errorf("%q: problem = %v, want validation", bad, prob)
		}
	}
}

func TestAdminEgressFlows_LookupByAddress(t *testing.T) {
	e := newIssueCreditEnv(t, api.ScopesAdminOnly, "ops@example.com", "ops@example.com")
	mem := e.store
	at := timeNow().UTC().Add(-time.Hour)
	if err := mem.InsertEgressFlows(context.Background(), []state.EgressFlowRecord{
		{ObservedAt: at, NodeName: "fsn-2", AccountID: "acct-1", AppID: "app-1", InstanceID: "i-1",
			RemoteIP: netip.MustParseAddr("198.51.100.10"), RemotePort: 443},
		{ObservedAt: at, NodeName: "fsn-2", AccountID: "acct-2", AppID: "app-2", InstanceID: "i-2",
			RemoteIP: netip.MustParseAddr("203.0.113.5"), RemotePort: 443},
	}); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/egress-flows?remote=198.51.100.0/24", nil)
	e.addAdminSession(t, req)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var resp api.EgressFlowLogResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Flows) != 1 || resp.Flows[0].AccountID != "acct-1" || resp.Flows[0].RemoteIP != "198.51.100.10" ||
		resp.Flows[0].RemotePort != 443 || resp.Flows[0].Node != "fsn-2" || resp.Truncated {
		t.Fatalf("response = %+v, want the one acct-1 flow", resp)
	}
}

func TestAdminEgressFlows_NonOperatorForbidden(t *testing.T) {
	e := newIssueCreditEnv(t, api.ScopesAdminOnly, "allowed@example.com", "intruder@example.com")
	rec := httptest.NewRecorder()
	e.s.requireOperator(e.s.listEgressFlows)(rec, httptest.NewRequest(http.MethodGet, "/v1/admin/egress-flows", nil), e.acct)
	assertProblem(t, rec, http.StatusForbidden, "admin_required")
}
