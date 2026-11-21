package main_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestCmdRunEnvironmentForwardsExactSystemPackagePins(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var request api.CreateExecutionEnvironmentRequest
		if r.Method != http.MethodPost || r.URL.Path != "/v1/execution-environments" || json.NewDecoder(r.Body).Decode(&request) != nil {
			t.Error("unexpected environment build request", r.Method, r.URL.Path)
		}
		if request.Runtime != api.ExecutionRuntimePython313 || request.Requirements != "" || !reflect.DeepEqual(request.SystemPackages, []string{"python-3.13=3.13.15-r0", "poppler-utils=26.08.0-r0"}) {
			t.Errorf("system-only recipe changed: %+v", request)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(api.ExecutionEnvironmentResponse{ID: "environment", Runtime: request.Runtime, Status: "queued"})
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fixture-token")
	t.Setenv("FAAS_CAPABILITY_FLAGS_RELEASE_ALL", "1")
	code, _, stderr := runSourceRunCLI(t, "run", "environments", "build", "--runtime", "python313", "--system-package", "python-3.13=3.13.15-r0", "--system-package", "poppler-utils=26.08.0-r0", "--json")
	if code != 0 || calls != 1 {
		t.Fatal("system environment build failed", code, calls, stderr)
	}
}
