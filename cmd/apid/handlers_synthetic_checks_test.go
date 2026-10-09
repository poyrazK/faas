package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestSyntheticCheckLifecycle(t *testing.T) {
	e := setup(t, api.PlanPro)
	createApp(t, e, "shop")
	req := api.CreateSyntheticCheckRequest{Name: "health", Path: "/healthz", IntervalSeconds: 300}
	rec := e.do(t, http.MethodPost, "/v1/apps/shop/synthetics", req, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", rec.Code, rec.Body)
	}
	var created api.SyntheticCheckResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Method != http.MethodGet || created.TimeoutMS != api.SyntheticCheckDefaultTimeoutMS || !created.Enabled || !strings.HasSuffix(created.URL, "/healthz") || !strings.Contains(created.URL, "shop") {
		t.Fatalf("created = %+v", created)
	}
	if rec := e.do(t, http.MethodPost, "/v1/apps/shop/synthetics", req, nil); rec.Code != http.StatusConflict {
		t.Fatalf("duplicate = %d, want 409", rec.Code)
	}
	rec = e.do(t, http.MethodPatch, "/v1/apps/shop/synthetics/"+created.ID, api.UpdateSyntheticCheckRequest{Enabled: false}, nil)
	var paused api.SyntheticCheckResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &paused); err != nil || paused.Enabled {
		t.Fatalf("pause = %d %s", rec.Code, rec.Body)
	}
	rec = e.do(t, http.MethodGet, "/v1/apps/shop/synthetics", nil, nil)
	var list []api.SyntheticCheckResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil || len(list) != 1 || list[0].Enabled {
		t.Fatalf("list = %d %s", rec.Code, rec.Body)
	}
	if rec := e.do(t, http.MethodDelete, "/v1/apps/shop/synthetics/"+created.ID, nil, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("delete = %d", rec.Code)
	}
	if rec := e.do(t, http.MethodGet, "/v1/apps/shop/synthetics/"+created.ID, nil, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("get after delete = %d", rec.Code)
	}
}

func TestSyntheticCheckGates(t *testing.T) {
	free := setup(t, api.PlanFree)
	createApp(t, free, "tiny")
	req := api.CreateSyntheticCheckRequest{Name: "health", Path: "/", IntervalSeconds: 300}
	if rec := free.do(t, http.MethodPost, "/v1/apps/tiny/synthetics", req, nil); rec.Code != http.StatusPaymentRequired {
		t.Fatalf("free = %d, want 402", rec.Code)
	}
	e := setup(t, api.PlanHobby)
	createApp(t, e, "shop")
	bad := req
	bad.Path = "//evil.example/"
	if rec := e.do(t, http.MethodPost, "/v1/apps/shop/synthetics", bad, nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("foreign host path = %d, want 400", rec.Code)
	}
	for i := 0; i < api.MaxSyntheticChecksPerApp; i++ {
		r := req
		r.Name = fmt.Sprintf("c-%d", i)
		if rec := e.do(t, http.MethodPost, "/v1/apps/shop/synthetics", r, nil); rec.Code != http.StatusCreated {
			t.Fatalf("create %d = %d %s", i, rec.Code, rec.Body)
		}
	}
	over := req
	over.Name = "one-too-many"
	if rec := e.do(t, http.MethodPost, "/v1/apps/shop/synthetics", over, nil); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("over cap = %d, want 422", rec.Code)
	}
}
