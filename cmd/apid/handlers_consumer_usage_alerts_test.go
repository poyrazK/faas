package main

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 849
func TestAPIConsumerPlanAlertThresholdsAndUsageAlerts(t *testing.T) {
	e := setup(t, api.PlanHobby)
	mustSeedApp(t, e, "consumer-alerts")
	created := e.do(t, http.MethodPost, "/v1/apps/consumer-alerts/consumers", api.CreateAPIConsumerRequest{
		ExternalRef: "alert-customer", Name: "Alert Customer",
	}, nil)
	var consumer api.APIConsumerResponse
	if err := json.Unmarshal(created.Body.Bytes(), &consumer); err != nil || created.Code != http.StatusCreated {
		t.Fatalf("create consumer: %d %s", created.Code, created.Body)
	}
	plans := "/v1/apps/consumer-alerts/consumer-plans"
	if res := e.do(t, http.MethodPost, plans, api.CreateAPIConsumerPlanRequest{Name: "nolimit", AlertThresholdsPercent: []int32{80}}, nil); res.Code != http.StatusUnprocessableEntity {
		t.Fatalf("alerts without a monthly limit: %d %s, want 422", res.Code, res.Body)
	}
	res := e.do(t, http.MethodPost, plans, api.CreateAPIConsumerPlanRequest{Name: "starter", MaxUnitsPerMonth: 10, AlertThresholdsPercent: []int32{100, 50}}, nil)
	var plan api.APIConsumerPlanResponse
	if err := json.Unmarshal(res.Body.Bytes(), &plan); err != nil || res.Code != http.StatusCreated || !slices.Equal(plan.AlertThresholdsPercent, []int32{50, 100}) {
		t.Fatalf("create plan: %d %s", res.Code, res.Body)
	}
	if res := e.do(t, http.MethodPut, plans+"/"+plan.ID, api.UpdateAPIConsumerPlanLimitsRequest{}, nil); res.Code != http.StatusUnprocessableEntity {
		t.Fatalf("dropping the monthly limit under alerts: %d %s, want 422", res.Code, res.Body)
	}
	res = e.do(t, http.MethodPut, plans+"/"+plan.ID, api.UpdateAPIConsumerPlanLimitsRequest{MaxUnitsPerMonth: 10}, nil)
	if err := json.Unmarshal(res.Body.Bytes(), &plan); err != nil || res.Code != http.StatusOK || !slices.Equal(plan.AlertThresholdsPercent, []int32{50, 100}) {
		t.Fatalf("update keeping alerts: %d %s", res.Code, res.Body)
	}

	policy := state.APIConsumerPlanPolicy{PlanID: plan.ID, AppID: consumer.AppID, MaxUnitsPerMonth: 10, AlertThresholdsPercent: plan.AlertThresholdsPercent}
	for range 6 {
		if _, err := e.store.AdmitAPIConsumerPlanRequest(context.Background(), e.acct.ID, consumer.ID, policy, 1); err != nil {
			t.Fatal(err)
		}
	}
	alertsPath := "/v1/apps/consumer-alerts/consumers/" + consumer.ID + "/usage-alerts"
	res = e.do(t, http.MethodGet, alertsPath, nil, nil)
	var alerts api.APIConsumerUsageAlertListResponse
	if err := json.Unmarshal(res.Body.Bytes(), &alerts); err != nil || res.Code != http.StatusOK {
		t.Fatalf("list alerts: %d %s", res.Code, res.Body)
	}
	if len(alerts.Alerts) != 1 || alerts.Alerts[0].ThresholdPercent != 50 || alerts.Alerts[0].UsedUnits != 5 || alerts.Alerts[0].PlanID != plan.ID {
		t.Fatalf("alerts = %+v, want one 50%% alert at 5 units", alerts.Alerts)
	}
	if res := e.do(t, http.MethodGet, "/v1/apps/consumer-alerts/consumers/nope/usage-alerts", nil, nil); res.Code != http.StatusNotFound {
		t.Fatalf("unknown consumer: %d %s, want 404", res.Code, res.Body)
	}
}
