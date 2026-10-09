package main

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestGetSLOReportsBudgetFromHourlyRows(t *testing.T) {
	e := setup(t, api.PlanPro)
	createApp(t, e, "shop")
	rec := e.do(t, http.MethodPost, "/v1/apps/shop/slos", api.CreateSLORequest{Name: "up", SLI: "availability", ObjectivePct: 99.9, WindowDays: 30}, nil)
	var created api.SLOResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	// The SLO was created this hour, so its window starts now; record the
	// current hour's row as if it had already completed. 9995 good of
	// 10000 against a 99.9% objective leaves half the budget.
	hour := time.Now().UTC().Truncate(time.Hour)
	if err := e.store.UpsertSLOHour(context.Background(), created.ID, hour, 9995, 10000); err != nil {
		t.Fatal(err)
	}

	rec = e.do(t, http.MethodGet, "/v1/apps/shop/slos/"+created.ID, nil, nil)
	var got api.SLOResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || got.Status == nil {
		t.Fatalf("get: %d %s", rec.Code, rec.Body)
	}
	st := got.Status
	if st.Good != 9995 || st.Total != 10000 || st.HoursRecorded != 1 {
		t.Fatalf("status counts = %+v", st)
	}
	if st.AttainmentPct == nil || math.Abs(*st.AttainmentPct-99.95) > 1e-9 {
		t.Fatalf("attainment = %v, want 99.95", st.AttainmentPct)
	}
	if st.BudgetRemainingPct == nil || math.Abs(*st.BudgetRemainingPct-50) > 1e-9 {
		t.Fatalf("budget remaining = %v, want 50", st.BudgetRemainingPct)
	}
	// No Prometheus in tests: burn rates are absent and the source says why.
	if st.BurnRate1h != nil || !strings.HasPrefix(st.Source, "degraded") {
		t.Fatalf("burn %v source %q; want nil and degraded", st.BurnRate1h, st.Source)
	}
}

func TestGetSLOWithoutTrafficHasNoBudgetFigures(t *testing.T) {
	e := setup(t, api.PlanHobby)
	createApp(t, e, "quiet")
	rec := e.do(t, http.MethodPost, "/v1/apps/quiet/slos", api.CreateSLORequest{Name: "fast", SLI: "latency", LatencyThresholdMS: 100, ObjectivePct: 99, WindowDays: 7}, nil)
	var created api.SLOResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	rec = e.do(t, http.MethodGet, "/v1/apps/quiet/slos/"+created.ID, nil, nil)
	var got api.SLOResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || got.Status == nil {
		t.Fatalf("get: %d %s", rec.Code, rec.Body)
	}
	if got.Status.AttainmentPct != nil || got.Status.BudgetRemainingPct != nil {
		t.Fatalf("no traffic must not report a budget: %+v", got.Status)
	}
	var _ state.SLOBudgetStore = e.store
}
