package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestAPIContractFallback(t *testing.T) {
	e := setup(t, api.PlanPro)
	tests := []struct {
		name       string
		method     string
		path       string
		auth       bool
		wantStatus int
		wantCode   string
		wantAllow  string
	}{
		{name: "anonymous unknown", method: http.MethodGet, path: "/v1/not-a-real-route", wantStatus: http.StatusNotFound, wantCode: api.CodeNotFound},
		{name: "authenticated unknown", method: http.MethodGet, path: "/v1/not-a-real-route", auth: true, wantStatus: http.StatusNotFound, wantCode: api.CodeNotFound},
		{name: "known path wrong method", method: http.MethodPost, path: "/v1/account", wantStatus: http.StatusMethodNotAllowed, wantCode: api.CodeMethodNotAllowed, wantAllow: "DELETE, GET, HEAD"},
		{name: "escaped slash", method: http.MethodGet, path: "/v1/not%2Fa%2Froute", wantStatus: http.StatusNotFound, wantCode: api.CodeNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			if tt.auth {
				req.Header.Set("Authorization", "Bearer "+e.key)
			}
			rec := httptest.NewRecorder()
			e.h.ServeHTTP(rec, req)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
			}
			if got := rec.Header().Get("Content-Type"); got != "application/problem+json" {
				t.Fatalf("content-type=%q", got)
			}
			if got := rec.Header().Get("Allow"); got != tt.wantAllow {
				t.Fatalf("allow=%q, want %q", got, tt.wantAllow)
			}
			var p api.Problem
			if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
				t.Fatal(err)
			}
			if p.Code != tt.wantCode || p.Status != tt.wantStatus {
				t.Fatalf("problem=%+v", p)
			}
		})
	}
}

func TestAPIContractFallback_PreservesNonAPIResponse(t *testing.T) {
	e := setup(t, api.PlanPro)
	rec := e.do(t, http.MethodGet, "/not-a-real-browser-route", nil, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d", rec.Code)
	}
	if !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/plain") {
		t.Fatalf("content-type=%q", rec.Header().Get("Content-Type"))
	}
}

func TestInternalMetricsRoutesAreAbsentFromPublicHandler(t *testing.T) {
	e := setup(t, api.PlanPro)
	paths := []string{
		computeMetricsDiscoveryPath,
		vmmdMetricsDiscoveryPath,
		imagedMetricsDiscoveryPath,
		builderdMetricsDiscoveryPath,
		promtailMetricsDiscoveryPath,
	}
	for _, path := range paths {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.RemoteAddr = "127.0.0.1:12345"
		rec := httptest.NewRecorder()
		e.h.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s status=%d, want 404", path, rec.Code)
		}
		if strings.Contains(rec.Body.String(), "targets") {
			t.Errorf("%s disclosed discovery response: %s", path, rec.Body)
		}
	}
}
