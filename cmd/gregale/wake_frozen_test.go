package main_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// Production 2026-11-05 (rc.324, #6639): `app wake --wait` on a frozen app
// timed out although schedd thawed the VM in 30 ms. A thawed VM keeps its
// own wake_id, so the wait now also succeeds on the instance the 202 names.
func TestCmdWakeWaitResolvesOnTheResumedFrozenInstance(t *testing.T) {
	for _, tc := range []struct {
		name      string
		instances []api.InstanceResponse
		wantCode  int
		wantID    string
	}{
		{
			name:      "frozen instance thawed under its own wake id",
			instances: []api.InstanceResponse{{ID: "frozen-1", State: "running", WakeID: "wake-own"}},
			wantID:    "frozen-1",
		},
		{
			name: "frozen instance parked, a boot under the wake id took over",
			instances: []api.InstanceResponse{
				{ID: "fresh-2", State: "running", WakeID: "wake-new"},
				{ID: "frozen-1", State: "parked", WakeID: "wake-own"},
			},
			wantID: "fresh-2",
		},
		{
			name:      "the wake's own boot failed",
			instances: []api.InstanceResponse{{ID: "frozen-1", State: "frozen", WakeID: "wake-own"}, {ID: "fresh-2", State: "failed", WakeID: "wake-new"}},
			wantCode:  1,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodPost && r.URL.Path == "/v1/apps/demo/wake":
					main.WriteJSONTestStatus(w, http.StatusAccepted, api.AppWakeResponse{WakeID: "wake-new", InstanceID: "frozen-1"})
				case r.URL.Path == "/v1/apps/demo/instances":
					main.WriteJSONTest(w, tc.instances)
				default:
					http.NotFound(w, r)
				}
			}))
			defer srv.Close()
			t.Setenv("FAAS_API", srv.URL)
			t.Setenv("FAAS_TOKEN", "fp_live_x")
			var stdout bytes.Buffer
			oldOut := (*main.OsStdout)
			(*main.OsStdout) = &stdout
			defer func() { (*main.OsStdout) = oldOut }()
			main.ResetJSONOut(t)
			(*main.JsonOutput) = true

			code := main.CmdWake([]string{"demo", "--wait", "--timeout", "2s", "--poll-interval", "100ms"})
			if code != tc.wantCode {
				t.Fatalf("wake demo --wait = %d, want %d\n%s", code, tc.wantCode, stdout.String())
			}
			if tc.wantCode != 0 {
				return
			}
			var receipt map[string]string
			if err := json.Unmarshal(stdout.Bytes(), &receipt); err != nil {
				t.Fatalf("output is not JSON: %v\n%s", err, stdout.String())
			}
			if receipt["status"] != "running" || receipt["instance_id"] != tc.wantID || receipt["wake_id"] != "wake-new" {
				t.Fatalf("receipt = %#v, want running %s for wake-new", receipt, tc.wantID)
			}
		})
	}
}
