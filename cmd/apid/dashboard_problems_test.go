package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestWriteDashboardUnauthorizedNegotiatesBrowserPage(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/dashboard/apps/demo/delete", nil)
	request.Header.Set("Accept", "text/html")

	writeDashboardUnauthorized(recorder, request)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", recorder.Code)
	}
	if got := recorder.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/html") {
		t.Fatalf("Content-Type = %q, want text/html", got)
	}
	body := recorder.Body.String()
	for _, want := range []string{"Sign in required", "Sign in again", api.CodeUnauthorized} {
		if !strings.Contains(body, want) {
			t.Errorf("browser response missing %q: %s", want, body)
		}
	}
	if strings.Contains(body, "missing or has expired") {
		t.Errorf("browser response exposed Problem.Detail: %s", body)
	}
}

func TestWriteDashboardBadRequestReturnsProblemJSON(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/dashboard/projects/not-valid/preview", nil)

	writeDashboardBadRequest(recorder, request, "The project slug is invalid.")

	var problem api.Problem
	if err := json.Unmarshal(recorder.Body.Bytes(), &problem); err != nil {
		t.Fatalf("decode problem: %v", err)
	}
	if problem.Status != http.StatusBadRequest || problem.Code != api.CodeBadRequest {
		t.Errorf("problem = %#v, want 400/%s", problem, api.CodeBadRequest)
	}
	if problem.Detail != "The project slug is invalid." || problem.Hint == "" {
		t.Errorf("problem is not actionable: %#v", problem)
	}
}
