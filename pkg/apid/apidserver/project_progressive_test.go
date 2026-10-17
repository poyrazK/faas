package apidserver

import (
	"encoding/json"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http/httptest"
	"testing"
)

func TestProgressiveProjectAdmissionGuards(t *testing.T) {
	for _, tc := range []struct {
		name, mode, health, progressive string
		config                          bool
		status                          int
	}{
		{"default_disabled", "atomic-config-verify", "", "required", true, 503},
		{"missing_configuration", "atomic-verify", "", "required", false, 400},
		{"missing_worker_health", "atomic-config-verify-workers", "", "required", true, 400},
		{"invalid_policy", "atomic-config-verify", "", "yes", true, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/scan-project", nil)
			r.Header.Set(api.ProjectSafeReleaseHeader, tc.mode)
			r.Header.Set(api.ProjectProgressiveHeader, tc.progressive)
			if tc.health != "" {
				r.Header.Set(api.ProjectWorkerHealthHeader, tc.health)
			}
			req := &scanPlanRequest{NoTriggers: true}
			if tc.config {
				req.ReleaseConfig = json.RawMessage(`{"env":{"demo":{}}}`)
			}
			problem := (&server{}).prepareSafeProjectRelease(r, state.Account{}, req, false)
			if problem == nil || problem.Status != tc.status {
				t.Fatalf("guard=%+v wanted status %d", problem, tc.status)
			}
		})
	}
}

func TestStabilizationAdmissionRequiresProgressive(t *testing.T) {
	for _, header := range []string{"required", "invalid"} {
		request := httptest.NewRequest("POST", "/scan-project", nil)
		request.Header.Set(api.ProjectStabilizationHeader, header)
		problem := (&server{}).prepareSafeProjectRelease(request, state.Account{}, &scanPlanRequest{}, false)
		if problem == nil || problem.Status != 400 {
			t.Fatalf("unguarded stabilization: %+v", problem)
		}
	}
}
