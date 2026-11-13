package dashboards

// adr: 20261104142830 — an account reads at most DashboardDataReadsPerAccount
// dashboards at once; another read is refused with 429 dashboard_data_busy
// until one finishes, and other accounts are unaffected.

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestDashboardDataReadsAreBoundedPerAccount(t *testing.T) {
	svc, acct := newService(true)
	acct.Plan = api.PlanPro
	svc.Reads = NewReadSlots(1)
	host := svc.Host.(fakeHost)
	host.sources.metrics = api.AppMetricsResponse{Source: "prometheus", RequestCount: 900}
	w := call(t, svc.Create, acct, http.MethodPost, "", `{"name":"busy","widgets":[
		{"title":"requests","kind":"app_metric","app":"shop","metric":"request_count"}]}`)
	var created api.Dashboard
	if w.Code != http.StatusCreated || json.Unmarshal(w.Body.Bytes(), &created) != nil {
		t.Fatalf("create = %d %s", w.Code, w.Body.String())
	}

	release, err := svc.Reads.acquire(acct.ID) // a read already in flight
	if err != nil {
		t.Fatal(err)
	}
	w = call(t, svc.Data, acct, http.MethodGet, created.ID, "")
	var p api.Problem
	if w.Code != http.StatusTooManyRequests || json.Unmarshal(w.Body.Bytes(), &p) != nil || p.Code != "dashboard_data_busy" || w.Header().Get("Retry-After") == "" {
		t.Fatalf("read past the cap = %d %s; want 429 dashboard_data_busy with Retry-After", w.Code, w.Body.String())
	}
	if other, err := svc.Reads.acquire("another-account"); err != nil {
		t.Fatalf("another account was refused: %v", err)
	} else {
		other()
	}
	release()
	if w = call(t, svc.Data, acct, http.MethodGet, created.ID, ""); w.Code != http.StatusOK {
		t.Fatalf("read after the slot freed = %d %s", w.Code, w.Body.String())
	}
}
