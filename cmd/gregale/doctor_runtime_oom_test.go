package main_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// runtime-oom was always "skipped", so an OOM-killed app passed doctor
// (2026-11-12 agent feedback).

func TestDoctorRuntimeOOMWarnsOnAReportedKill(t *testing.T) {
	summary := api.AppOperationalSummary{Recommendations: []api.AppOperationalRecommendation{
		{Code: "production_health_unknown", Severity: "warning"},
		{Code: api.CodeAppRuntimeOOM, Severity: "error", Message: "2 out-of-memory kills in the last 24 hours", Next: "Raise its memory with `gregale app shop --ram 1024`"},
	}}
	got := main.DoctorRuntimeOOMFromSummary("shop", summary)
	if got.Status != "warn" || got.Code != api.CodeAppRuntimeOOM || !strings.Contains(got.Why, "2 out-of-memory kills") || !strings.Contains(got.Fix, "--ram 1024") {
		t.Fatalf("check = %+v", got)
	}
}

func TestDoctorRuntimeOOMIsOKWithoutAKill(t *testing.T) {
	got := main.DoctorRuntimeOOMFromSummary("shop", api.AppOperationalSummary{})
	if got.Name != "runtime-oom" || got.Status != "ok" {
		t.Fatalf("check = %+v", got)
	}
}

func TestDoctorRuntimeOOMReadsTheAppSummary(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/apps/shop/operational-summary" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(api.AppOperationalSummary{Recommendations: []api.AppOperationalRecommendation{
			{Code: api.CodeAppRuntimeOOM, Severity: "error", Message: "1 out-of-memory kill in the last 24 hours", Next: "raise it"},
		}})
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	if got := main.DoctorRuntimeOOMForApp(t.Context(), "shop"); got.Status != "warn" {
		t.Fatalf("check = %+v", got)
	}
	if got := main.DoctorRuntimeOOMForApp(t.Context(), "missing"); got.Status != "skipped" || !strings.Contains(got.Reason, "operational summary") {
		t.Fatalf("unreadable summary = %+v", got)
	}
	if got := main.DoctorRuntimeOOMForApp(t.Context(), ""); got.Status != "skipped" {
		t.Fatalf("no app = %+v", got)
	}
}

func TestReplaceDoctorCheckKeepsOrder(t *testing.T) {
	rep := main.DoctorReport{Checks: []main.DoctorCheck{{Name: "port-bind"}, main.DoctorCheckRuntimeOOM(), {Name: "dep-install"}}}
	main.ReplaceDoctorCheck(&rep, main.DoctorCheck{Name: "runtime-oom", Status: "ok"})
	if len(rep.Checks) != 3 || rep.Checks[1].Status != "ok" {
		t.Fatalf("checks = %+v", rep.Checks)
	}
}
