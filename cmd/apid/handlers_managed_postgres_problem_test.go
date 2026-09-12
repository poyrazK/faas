package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

func TestManagedPostgresNotConfiguredProblemExplainsPreviewGate(t *testing.T) {
	recorder := httptest.NewRecorder()
	managedPostgresNotConfiguredProblem(recorder)

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusServiceUnavailable)
	}
	var problem api.Problem
	if err := json.NewDecoder(recorder.Body).Decode(&problem); err != nil {
		t.Fatal(err)
	}
	if problem.Code != "managed_postgres_unavailable" {
		t.Fatalf("code = %q", problem.Code)
	}
	for _, want := range []string{"operator preview", "not configured", "qualify and configure a provider"} {
		if !strings.Contains(problem.Detail, want) {
			t.Errorf("detail %q does not contain %q", problem.Detail, want)
		}
	}
	if problem.DocsURL != "https://gregale.dev/docs/managed-postgres" {
		t.Fatalf("docs_url = %q", problem.DocsURL)
	}
}

func TestManagedPostgresProviderUnavailableProblemExplainsRetry(t *testing.T) {
	recorder := httptest.NewRecorder()
	managedPostgresProblem(recorder, managedpostgres.ErrUnavailable)

	var problem api.Problem
	if err := json.NewDecoder(recorder.Body).Decode(&problem); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"operator preview", "temporarily unavailable", "retry later"} {
		if !strings.Contains(problem.Detail, want) {
			t.Errorf("detail %q does not contain %q", problem.Detail, want)
		}
	}
}
