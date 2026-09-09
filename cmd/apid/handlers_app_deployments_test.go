package main

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestListAppDeploymentsPaginates(t *testing.T) {
	e := setup(t, api.PlanPro)
	appID := mustSeedApp(t, e, "history-app")
	base := time.Now().UTC().Add(-time.Minute)
	for i := 0; i < 3; i++ {
		_, err := e.store.CreateDeployment(context.Background(), state.Deployment{
			AppID: appID, ImageDigest: "sha256:" + repeat(string(rune('a'+i)), 64),
			Kind: state.DeploymentKindImage, Status: state.DeployBuilding,
			CreatedAt: base.Add(time.Duration(i) * time.Second),
		})
		if err != nil {
			t.Fatalf("CreateDeployment(%d): %v", i, err)
		}
	}

	page1 := e.do(t, http.MethodGet, "/v1/apps/history-app/deployments?limit=2", nil, nil)
	if page1.Code != http.StatusOK {
		t.Fatalf("page 1 status %d: %s", page1.Code, page1.Body)
	}
	var first api.DeploymentListResponse
	if err := json.Unmarshal(page1.Body.Bytes(), &first); err != nil {
		t.Fatalf("decode page 1: %v", err)
	}
	if len(first.Items) != 2 || first.Items[0].CreatedAt <= first.Items[1].CreatedAt {
		t.Fatalf("page 1 items = %+v, want newest first", first.Items)
	}
	if first.NextBefore == "" {
		t.Fatal("page 1 missing next_before")
	}

	page2 := e.do(t, http.MethodGet, "/v1/apps/history-app/deployments?limit=2&before="+first.NextBefore, nil, nil)
	if page2.Code != http.StatusOK {
		t.Fatalf("page 2 status %d: %s", page2.Code, page2.Body)
	}
	var second api.DeploymentListResponse
	if err := json.Unmarshal(page2.Body.Bytes(), &second); err != nil {
		t.Fatalf("decode page 2: %v", err)
	}
	if len(second.Items) != 1 || second.Items[0].CreatedAt >= first.Items[1].CreatedAt {
		t.Fatalf("page 2 items = %+v, want the remaining oldest row", second.Items)
	}
	if second.NextBefore != "" {
		t.Fatalf("page 2 next_before = %q, want empty", second.NextBefore)
	}
}

func TestListAppDeploymentsNeverDeployedIsEmpty(t *testing.T) {
	e := setup(t, api.PlanPro)
	mustSeedApp(t, e, "history-empty")

	rec := e.do(t, http.MethodGet, "/v1/apps/history-empty/deployments", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var got api.DeploymentListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.Items == nil || len(got.Items) != 0 || got.NextBefore != "" {
		t.Fatalf("empty response = %+v, want items=[] and no cursor", got)
	}
}

func TestListAppDeploymentsForeignAppReturns404(t *testing.T) {
	e := setup(t, api.PlanPro)
	foreign, err := e.store.CreateAccount(context.Background(), "foreign-history@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	mustSeedAppFor(t, e.store, foreign.ID, "foreign-history")

	rec := e.do(t, http.MethodGet, "/v1/apps/foreign-history/deployments", nil, nil)
	assertProblem(t, rec, http.StatusNotFound, api.CodeNotFound)
}

func TestListAppDeploymentsBadCursor(t *testing.T) {
	e := setup(t, api.PlanPro)
	mustSeedApp(t, e, "history-bad-cursor")

	rec := e.do(t, http.MethodGet, "/v1/apps/history-bad-cursor/deployments?before=not-a-timestamp", nil, nil)
	assertProblem(t, rec, http.StatusBadRequest, api.CodeValidation)
}
