package main

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestAccountUsage_ComputeProjection(t *testing.T) {
	e := setup(t, api.PlanPro)
	rec := e.do(t, http.MethodGet, "/v1/account/usage?month=2026-09", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var out api.AccountUsageResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if out.Month != "2026-09" || out.Compute.Month != "2026-09" {
		t.Fatalf("month projection = %+v", out)
	}
	if out.ObjectStorage != nil || out.ManagedPostgres != nil {
		t.Fatalf("disabled services should be omitted: %+v", out)
	}
}

func TestAccountUsage_IncludesConfiguredObjectStorage(t *testing.T) {
	e := setup(t, api.PlanHobby)
	e.s.WithObjectStorage(objectRegistry(t, &fakeObjectProvider{}, &fakeObjectProvider{}, "external"))
	rec := e.do(t, http.MethodGet, "/v1/account/usage", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var out api.AccountUsageResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if out.ObjectStorage == nil {
		t.Fatal("configured object storage missing from account usage")
	}
	if out.ObjectStorage.Usage.Fresh {
		t.Fatal("empty inventory must remain stale")
	}

	rec = e.do(t, http.MethodGet, "/v1/account/usage?month=2026-08", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("historical status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var historical api.AccountUsageResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &historical); err != nil {
		t.Fatalf("decode historical response: %v", err)
	}
	if historical.ObjectStorage != nil {
		t.Fatal("historical compute usage must not carry a current-month object-storage view")
	}
}

func TestAccountUsage_BadMonth(t *testing.T) {
	e := setup(t, api.PlanPro)
	rec := e.do(t, http.MethodGet, "/v1/account/usage?month=not-a-month", nil, nil)
	assertProblem(t, rec, http.StatusBadRequest, api.CodeValidation)
}
