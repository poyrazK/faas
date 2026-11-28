package main_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// ADR-045 amendment 1: --job and --cron send the failure rule's filter.
func TestAlertCreateFailureFilters(t *testing.T) {
	for _, tc := range []struct {
		name        string
		args        []string
		job, cronID string
	}{
		{"job", []string{"--metric", "job_failures", "--job", "nightly-export"}, "nightly-export", ""},
		{"cron", []string{"--metric", "failed_invocations", "--failure-source", "cron", "--cron", "3f2c9d1e-0b7a-4c55-9f1e-2d6a8b4c7e10"}, "", "3f2c9d1e-0b7a-4c55-9f1e-2d6a8b4c7e10"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			main.ResetJSONOut(t)
			body := `{"id":"0123456789abcdef0123456789abcdef","name":"r","metric":"job_failures","window_spec":"1h","threshold":0,"comparison":"gt","enabled":true}`
			f := main.AuthedFakeAPI(t, body, http.StatusOK)
			args := append([]string{"--app", "demo", "--name", "r", "--comparison", "gt", "--threshold", "0", "--window-spec", "1h",
				"--webhook-url", "https://x", "--webhook-secret", "s"}, tc.args...)
			if code := main.CmdAlertAdd(args); code != 0 {
				t.Fatalf("exit = %d", code)
			}
			var request api.CreateAlertRuleRequest
			if err := json.Unmarshal(*f.SawBody(), &request); err != nil {
				t.Fatal(err)
			}
			if request.JobName != tc.job || request.CronID != tc.cronID {
				t.Fatalf("job_name=%q cron_id=%q, want %q %q", request.JobName, request.CronID, tc.job, tc.cronID)
			}
		})
	}
}

// alert update sends --job only when given, and --job= clears it.
func TestAlertUpdateFailureFilters(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want *string
	}{
		{"untouched", []string{"--name", "renamed"}, nil},
		{"changed", []string{"--job", "weekly"}, alertFilterPtr("weekly")},
		{"cleared", []string{"--job="}, alertFilterPtr("")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			main.ResetJSONOut(t)
			body := `{"id":"0123456789abcdef0123456789abcdef","name":"r","metric":"job_failures","window_spec":"1h","threshold":0,"comparison":"gt","enabled":true}`
			f := main.AuthedFakeAPI(t, body, http.StatusOK)
			args := append([]string{"--app", "demo"}, tc.args...)
			if code := main.CmdAlertUpdate(append(args, "0123456789abcdef0123456789abcdef")); code != 0 {
				t.Fatalf("exit = %d", code)
			}
			var request api.UpdateAlertRuleRequest
			if err := json.Unmarshal(*f.SawBody(), &request); err != nil {
				t.Fatal(err)
			}
			if (request.JobName == nil) != (tc.want == nil) || tc.want != nil && *request.JobName != *tc.want {
				t.Fatalf("job_name = %v, want %v", request.JobName, tc.want)
			}
		})
	}
}

func alertFilterPtr(s string) *string { return &s }
