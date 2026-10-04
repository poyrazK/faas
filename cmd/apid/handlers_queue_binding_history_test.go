package main

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestQueueBindingHTTPHistoryRequiresExplicitSelectorAndScope(t *testing.T) {
	e := setup(t, api.PlanPro)
	ctx := context.Background()
	app, err := e.store.CreateApp(ctx, state.App{AccountID: e.acct.ID, Slug: "history-worker", WorkloadClass: state.WorkloadClassWorker})
	if err != nil {
		t.Fatal(err)
	}
	base := "/v1/apps/history-worker/queue-bindings"
	request := api.CreateQueueBindingRequest{Name: "orders", QueueName: "orders", Mode: "push", WorkloadClass: "worker", MaxConcurrency: 2}
	rec := e.do(t, http.MethodPost, base, request, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	var retired api.QueueBindingResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &retired); err != nil {
		t.Fatal(err)
	}
	if rec = e.do(t, http.MethodDelete, base+"/"+retired.ID, nil, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("retire: %d %s", rec.Code, rec.Body.String())
	}
	held, err := e.store.QueueBindingHistoryByID(ctx, e.acct.ID, app.ID, retired.ID)
	if err != nil || held.RetiredAt == nil {
		t.Fatalf("retained history: %+v %v", held, err)
	}
	request.Name, request.QueueName = "next", "next"
	if rec = e.do(t, http.MethodPost, base, request, nil); rec.Code != http.StatusCreated {
		t.Fatalf("active neighbor: %d %s", rec.Code, rec.Body.String())
	}
	for _, tc := range []struct {
		selector string
		count    int
	}{{"", 1}, {"?include_retired=false", 1}, {"?include_retired=true", 2}} {
		t.Run(tc.selector, func(t *testing.T) {
			rec := e.do(t, http.MethodGet, base+tc.selector, nil, nil)
			var rows []api.QueueBindingResponse
			if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &rows) != nil || len(rows) != tc.count {
				t.Fatalf("history: %d %s", rec.Code, rec.Body.String())
			}
			for _, row := range rows {
				if row.ID == retired.ID && (row.RetiredAt == nil || !row.RetiredAt.Equal(*held.RetiredAt)) {
					t.Fatalf("retirement identity/timestamp lost: %+v", row)
				}
			}
		})
	}
	rec = e.do(t, http.MethodGet, base+"?include_retired=all", nil, nil)
	assertProblem(t, rec, http.StatusBadRequest, api.CodeValidation)
	rec = e.do(t, http.MethodGet, base+"?include_retired=true", nil, map[string]string{"Authorization": ""})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated history: %d", rec.Code)
	}
	key, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.CreateAPIKey(ctx, e.acct.ID, hash, "write-only", []string{"deploy:write"}); err != nil {
		t.Fatal(err)
	}
	rec = e.do(t, http.MethodGet, base+"?include_retired=true", nil, map[string]string{"Authorization": "Bearer " + key})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("write-only history: %d %s", rec.Code, rec.Body.String())
	}
	other, err := e.store.CreateAccount(ctx, "history-neighbor@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.CreateApp(ctx, state.App{AccountID: other.ID, Slug: "other-history-worker"}); err != nil {
		t.Fatal(err)
	}
	rec = e.do(t, http.MethodGet, "/v1/apps/other-history-worker/queue-bindings?include_retired=true", nil, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("cross-tenant history: %d %s", rec.Code, rec.Body.String())
	}
	if _, err := e.store.CreateApp(ctx, state.App{AccountID: e.acct.ID, Slug: "same-tenant-neighbor"}); err != nil {
		t.Fatal(err)
	}
	rec = e.do(t, http.MethodGet, "/v1/apps/same-tenant-neighbor/queue-bindings?include_retired=true", nil, nil)
	if rec.Code != http.StatusOK || rec.Body.String() != "[]\n" {
		t.Fatalf("history crossed app scope: %d %s", rec.Code, rec.Body.String())
	}
}
