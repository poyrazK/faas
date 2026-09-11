package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type failingPingStore struct {
	state.Store
}

func (f *failingPingStore) Ping(_ context.Context) error {
	return errors.New("connection refused")
}

type failingDebugRegressionReadinessStore struct {
	state.Store
}

func (f *failingDebugRegressionReadinessStore) CheckDebugRegressionReadiness(_ context.Context) error {
	return errors.New("undefined table")
}

func TestReadyz_Healthy(t *testing.T) {
	e := setup(t, api.PlanFree)
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("content-type = %q, want application/json", ct)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"status":"ready"`) || !strings.Contains(body, `"database":"ok"`) {
		t.Errorf("body = %q, want status:ready and database:ok", body)
	}
}

func TestReadyz_UnhealthyWhenStoreFails(t *testing.T) {
	e := setup(t, api.PlanFree)
	e.s.store = &failingPingStore{Store: e.s.store}

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"status":"unhealthy"`) || !strings.Contains(body, `"database":"unreachable"`) {
		t.Errorf("body = %q, want status:unhealthy and database:unreachable", body)
	}
}

func TestReadyz_UnhealthyWhenDebuggerRegressionReadFails(t *testing.T) {
	e := setup(t, api.PlanFree)
	e.s.store = &failingDebugRegressionReadinessStore{Store: e.s.store}

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"status":"unhealthy"`) || !strings.Contains(body, `"database":"ok"`) || !strings.Contains(body, `"debugger":"unavailable"`) {
		t.Errorf("body = %q, want debugger dependency failure", body)
	}
}
