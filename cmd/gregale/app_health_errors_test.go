package main_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// prod 2026-11-19: `app health --errors --json` on an app that never failed
// printed a problem whose type was "".
func TestNoFailedDeploymentProblemHasAType(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"slug":"demo","id":"11111111-1111-1111-1111-111111111111","items":[]}`)
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	code, stdout, stderr := runCLI(t, "app", "health", "demo", "--errors", "--json")
	if code != 1 {
		t.Fatalf("exit %d: %s%s", code, stdout, stderr)
	}
	var problem struct {
		Type string `json:"type"`
		Code string `json:"code"`
	}
	if err := json.Unmarshal([]byte(stderr), &problem); err != nil || problem.Type != "about:blank" || problem.Code != "not_found" {
		t.Fatalf("problem = %+v (%v): %s", problem, err, stderr)
	}
}
