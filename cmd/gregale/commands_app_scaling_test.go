// adr: 20261104104521
package main_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestAppScalingReadApplyRecoverCLI(t *testing.T) {
	main.ResetJSONOut(t)
	(*main.JsonOutput) = true
	const requestID = "4ed5f921-9eae-4bd0-a7ab-f851d38f745b"
	reads, writes, recoveries := 0, 0, 0
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("environment") != "staging" {
			t.Errorf("environment lost: %s", r.URL)
		}
		switch {
		case r.Method == http.MethodPost:
			var req api.ApplyAppScalingRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Error(err)
			}
			if req.RequestID != requestID || req.ExpectedRevision == nil || *req.ExpectedRevision != 3 || req.MinInstances == nil || *req.MinInstances != 0 {
				t.Errorf("request: %+v", req)
			}
			writes++
		case r.URL.Path == "/v1/apps/alpha/scaling/requests/"+requestID:
			recoveries++
		case r.URL.Path == "/v1/apps/alpha/scaling":
			reads++
		default:
			t.Errorf("unexpected route: %s", r.URL)
		}
		main.WriteJSONTest(w, api.AppScalingReceipt{RequestID: requestID})
	}))
	defer backend.Close()
	t.Setenv("FAAS_API", backend.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_x")
	for _, args := range [][]string{
		{"app", "alpha", "scaling", "--environment", "staging"},
		{"app", "alpha", "scaling", "--environment", "staging", "--expected-app-id", requestID, "--request-id", requestID, "--expected-revision", "3", "--expected-environment-id", requestID, "--min", "0"},
		{"app", "alpha", "scaling", "--environment", "staging", "--receipt", requestID},
	} {
		if code := main.Run(args); code != 0 {
			t.Fatalf("%v exit %d", args, code)
		}
	}
	if reads != 1 || writes != 1 || recoveries != 1 {
		t.Fatalf("calls: read %d write %d recover %d", reads, writes, recoveries)
	}
}
