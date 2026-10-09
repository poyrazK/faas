package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestSLOLifecycle(t *testing.T) {
	e := setup(t, api.PlanPro)
	createApp(t, e, "shop")
	req := api.CreateSLORequest{Name: "checkout-latency", SLI: "latency", LatencyThresholdMS: 250, ObjectivePct: 99.5, WindowDays: 30}

	rec := e.do(t, http.MethodPost, "/v1/apps/shop/slos", req, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", rec.Code, rec.Body)
	}
	var created api.SLOResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.ObjectivePct != 99.5 || created.LatencyThresholdMS != 250 || created.ID == "" {
		t.Fatalf("created = %+v", created)
	}
	if rec := e.do(t, http.MethodPost, "/v1/apps/shop/slos", req, nil); rec.Code != http.StatusConflict {
		t.Fatalf("duplicate name = %d, want 409", rec.Code)
	}

	rec = e.do(t, http.MethodGet, "/v1/apps/shop/slos", nil, nil)
	var list []api.SLOResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil || len(list) != 1 || list[0].ID != created.ID {
		t.Fatalf("list = %d %s (%v)", rec.Code, rec.Body, err)
	}
	if rec := e.do(t, http.MethodGet, "/v1/apps/shop/slos/"+created.ID, nil, nil); rec.Code != http.StatusOK {
		t.Fatalf("get = %d", rec.Code)
	}
	if rec := e.do(t, http.MethodDelete, "/v1/apps/shop/slos/"+created.ID, nil, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("delete = %d %s", rec.Code, rec.Body)
	}
	if rec := e.do(t, http.MethodGet, "/v1/apps/shop/slos/"+created.ID, nil, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("get after delete = %d, want 404", rec.Code)
	}
}

func TestSLOGatesAndLimits(t *testing.T) {
	free := setup(t, api.PlanFree)
	createApp(t, free, "tiny")
	avail := api.CreateSLORequest{Name: "up", SLI: "availability", ObjectivePct: 99.9, WindowDays: 7}
	if rec := free.do(t, http.MethodPost, "/v1/apps/tiny/slos", avail, nil); rec.Code != http.StatusPaymentRequired {
		t.Fatalf("free plan = %d, want 402", rec.Code)
	}

	e := setup(t, api.PlanHobby)
	createApp(t, e, "shop")
	bad := avail
	bad.ObjectivePct = 100
	if rec := e.do(t, http.MethodPost, "/v1/apps/shop/slos", bad, nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("objective 100 = %d, want 400", rec.Code)
	}
	for i := 0; i < api.MaxSLOsPerApp; i++ {
		req := avail
		req.Name = fmt.Sprintf("slo-%d", i)
		if rec := e.do(t, http.MethodPost, "/v1/apps/shop/slos", req, nil); rec.Code != http.StatusCreated {
			t.Fatalf("create %d = %d %s", i, rec.Code, rec.Body)
		}
	}
	over := avail
	over.Name = "one-too-many"
	if rec := e.do(t, http.MethodPost, "/v1/apps/shop/slos", over, nil); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("over cap = %d, want 422", rec.Code)
	}
	if rec := e.do(t, http.MethodDelete, "/v1/apps/shop/slos/00000000-0000-0000-0000-000000000000", nil, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("delete unknown = %d, want 404", rec.Code)
	}
}
