package main_test

import (
	"net/http"
	"strings"
	"testing"
)

// Prod hunt r65: an unfiltered `workflow list` with no runs suggested
// running the same command again.
func TestWorkflowListEmptyUnfilteredSuggestsARun(t *testing.T) {
	r63Server(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"runs":[],"total":0}`))
	})
	code, out := r63Run(t, "workflow", "list", "--app", "web")
	if code != 0 || !strings.Contains(out, "workflow run --app web WORKFLOW_NAME") || strings.Contains(out, "  gregale workflow list --app web\n") {
		t.Fatalf("workflow list = %d:\n%s", code, out)
	}
}

// Prod hunt r65: `data-api status <app>` with the JWT variable unset said
// "Invalid application JWT: configure the application JWT in
// access-token-env", naming neither the variable nor that it was unset.
func TestDataAPIStatusNamesTheUnsetTokenVariable(t *testing.T) {
	r63Server(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
	})
	t.Setenv("GREGALE_DATA_API_ACCESS_TOKEN", "")
	code, out := r63Run(t, "data-api", "status", "web")
	if code == 0 || !strings.Contains(out, "GREGALE_DATA_API_ACCESS_TOKEN, which is unset") {
		t.Fatalf("data-api status = %d:\n%s", code, out)
	}
	t.Setenv("MY_JWT", "fp_live_notajwt")
	code, out = r63Run(t, "data-api", "status", "web", "--access-token-env", "MY_JWT")
	if code == 0 || !strings.Contains(out, "MY_JWT does not hold an application JWT") {
		t.Fatalf("data-api status with an account key = %d:\n%s", code, out)
	}
}
