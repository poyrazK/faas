package ciapi_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/apid/ciapi"
	"github.com/onebox-faas/faas/pkg/ci"
)

func TestCIVisibilitySeparatesAdmissionFromRuntimeDrain(t *testing.T) {
	for _, test := range []struct {
		name   string
		config ci.Config
		path   string
		pass   bool
	}{
		{"disabled metadata", ci.Config{}, "/v1/internal/ci/capabilities", false},
		{"disabled root", ci.Config{}, "/v1/internal/ci", false},
		{"disabled redirect", ci.Config{}, "/v1/internal/ci//runs", false},
		{"enabled metadata", ci.Config{Enabled: true}, "/v1/internal/ci/capabilities", true},
		{"disabled transport", ci.Config{Enabled: true}, "/v1/internal/ci/runtime/run/control", false},
		{"draining runtime", ci.Config{TransportEnabled: true}, "/v1/internal/ci/runtime/run/result", true},
		{"draining metadata stays hidden", ci.Config{TransportEnabled: true}, "/v1/internal/ci/capabilities", false},
		{"draining downloads stay hidden", ci.Config{TransportEnabled: true}, "/v1/internal/ci/executions/run/jobs/test/artifacts/report", false},
		{"unrelated API", ci.Config{}, "/v1/capabilities", true},
		{"similar prefix", ci.Config{}, "/v1/internal/circuit", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			called := false
			handler := ciapi.HideDisabledRoutes(func() ci.Config { return test.config }, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				called = true
				w.WriteHeader(http.StatusNoContent)
			}))
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPatch, test.path, nil))
			if called != test.pass {
				t.Fatalf("reached next handler = %v, want %v", called, test.pass)
			}
			if test.pass {
				return
			}
			var problem struct{ Code, Detail string }
			if err := json.Unmarshal(recorder.Body.Bytes(), &problem); err != nil {
				t.Fatal(err)
			}
			if recorder.Code != http.StatusNotFound || problem.Code != "not_found" || problem.Detail != "no such API route" {
				t.Fatalf("disabled route revealed CI: %d %s", recorder.Code, recorder.Body)
			}
			if recorder.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("disabled route response must not be cached")
			}
		})
	}
}
