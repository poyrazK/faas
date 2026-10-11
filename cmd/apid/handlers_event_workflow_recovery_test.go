package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestWorkflowRecoveryJobHTTP(t *testing.T) {
	e := setup(t, api.PlanPro)
	mustSeedApp(t, e, "workflow-recovery")
	path := "/v1/apps/workflow-recovery/event-recoveries"
	ordinary := e.do(t, http.MethodPost, path, api.EventRecoveryRequest{}, nil)
	if ordinary.Code != http.StatusAccepted {
		t.Fatalf("ordinary=%d %s", ordinary.Code, ordinary.Body.String())
	}
	req := api.EventRecoveryRequest{RequestID: uuid.NewString(), WorkflowRouting: &api.EventWorkflowRecoveryPlan{Name: "paid", PublishedVersion: 1, Source: "billing"}}
	for i := 0; i < api.AutomationRecoveryBatchMaxRuns; i++ {
		req.WorkflowRouting.Events = append(req.WorkflowRouting.Events, api.EventWorkflowRecoveryTarget{EventID: fmt.Sprintf("%02d%s", i, strings.Repeat("e", 230)), SubscriptionID: strings.Repeat("s", 240)})
	}
	data, _ := json.Marshal(req)
	if len(data) <= 4<<10 {
		t.Fatal("fixture must exercise expanded body bound")
	}
	rec := e.do(t, http.MethodPost, path, req, nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("missing targets=%d %s", rec.Code, rec.Body.String())
	}
	var problem api.Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil || problem.Code != api.CodeEventRecoveryChanged {
		t.Fatalf("problem=%+v err=%v", problem, err)
	}
	req.EventSource = "other"
	rec = e.do(t, http.MethodPost, path, req, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("mixed filters=%d %s", rec.Code, rec.Body.String())
	}
}
