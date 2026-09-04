package e2e_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestE2E_Workflows_PlanCap_FreePlanRejects(t *testing.T) {
	if os.Getenv("FAAS_SKIP_PG_TESTS") != "" {
		t.Skip("FAAS_SKIP_PG_TESTS set")
	}
	pool := pgtest.Open(t)
	if pool == nil {
		t.Skip("pgtest.Open returned nil")
	}
	ctx := context.Background()
	if err := dbMigrateUp(t, pool); err != nil {
		t.Fatalf("dbMigrateUp: %v", err)
	}

	h := e2etest.StartWithEnv(t, pool, e2etest.APID, nil)
	key := h.SeedAccount(ctx, api.PlanFree, "wf-free-test")

	slug := "wffreeapp"
	body, status := doReq(t, h, key, http.MethodPost, "/v1/apps", api.CreateAppRequest{
		Slug: slug, Type: string(state.AppTypeApp),
	})
	if status != http.StatusCreated {
		t.Fatalf("create app: status=%d body=%s", status, body)
	}

	assertProblem(t, h, key, http.MethodPost,
		fmt.Sprintf("/v1/apps/%s/workflows/signup/runs", slug),
		map[string]any{"user_id": "u1"},
		http.StatusPaymentRequired, api.CodePlanWorkflowsNotAllowed)
}

func TestE2E_Workflows_Lifecycle(t *testing.T) {
	if os.Getenv("FAAS_SKIP_PG_TESTS") != "" {
		t.Skip("FAAS_SKIP_PG_TESTS set")
	}
	pool := pgtest.Open(t)
	if pool == nil {
		t.Skip("pgtest.Open returned nil")
	}
	ctx := context.Background()
	if err := dbMigrateUp(t, pool); err != nil {
		t.Fatalf("dbMigrateUp: %v", err)
	}

	h := e2etest.StartWithEnv(t, pool, e2etest.APID, nil)
	key := h.SeedAccount(ctx, api.PlanHobby, "wf-hobby-test")

	slug := "wfhobbyapp"
	body, status := doReq(t, h, key, http.MethodPost, "/v1/apps", api.CreateAppRequest{
		Slug: slug, Type: string(state.AppTypeApp),
	})
	if status != http.StatusCreated {
		t.Fatalf("create app: status=%d body=%s", status, body)
	}

	// 1. Trigger workflow run
	body, status = doReq(t, h, key, http.MethodPost,
		fmt.Sprintf("/v1/apps/%s/workflows/order_pipeline/runs", slug),
		map[string]any{"order_id": "ord_123"})
	if status != http.StatusCreated {
		t.Fatalf("trigger workflow: status=%d want 201; body=%s", status, body)
	}

	var runResp api.WorkflowRunResponse
	if err := json.Unmarshal(body, &runResp); err != nil {
		t.Fatalf("unmarshal run response: %v", err)
	}
	if runResp.WorkflowName != "order_pipeline" {
		t.Fatalf("expected workflow order_pipeline, got %s", runResp.WorkflowName)
	}

	// 2. List runs
	body, status = doReq(t, h, key, http.MethodGet,
		fmt.Sprintf("/v1/apps/%s/workflows/runs", slug), nil)
	if status != http.StatusOK {
		t.Fatalf("list workflow runs: status=%d want 200; body=%s", status, body)
	}
	var listResp api.ListWorkflowRunsResponse
	if err := json.Unmarshal(body, &listResp); err != nil {
		t.Fatalf("unmarshal list response: %v", err)
	}
	if listResp.Total < 1 {
		t.Fatalf("expected >= 1 run in list, got %d", listResp.Total)
	}

	// 3. Get run by ID
	body, status = doReq(t, h, key, http.MethodGet,
		fmt.Sprintf("/v1/workflows/runs/%s", runResp.ID), nil)
	if status != http.StatusOK {
		t.Fatalf("get workflow run: status=%d want 200; body=%s", status, body)
	}

	// 4. Cancel run
	body, status = doReq(t, h, key, http.MethodPost,
		fmt.Sprintf("/v1/workflows/runs/%s/cancel", runResp.ID), nil)
	if status != http.StatusOK {
		t.Fatalf("cancel workflow run: status=%d want 200; body=%s", status, body)
	}
	var cancelResp api.WorkflowRunResponse
	if err := json.Unmarshal(body, &cancelResp); err != nil {
		t.Fatalf("unmarshal cancel response: %v", err)
	}
	if cancelResp.Status != state.WorkflowRunStatusFailed {
		t.Fatalf("expected status failed after cancel, got %s", cancelResp.Status)
	}
}
