package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const (
	testGithubDeliveryID   = "11111111-1111-1111-1111-111111111111"
	testGithubDeploymentID = "22222222-2222-2222-2222-222222222222"
	testGithubTraceID      = "4bf92f3577b34da6a3ce929d0e0e4736"
)

func TestGithubStatusUsesAuthenticatedAPI(t *testing.T) {
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/admin/ops/github/recovery" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if r.URL.Query().Get("status") != "dead" || r.URL.Query().Get("limit") != "25" {
			t.Errorf("query = %q", r.URL.RawQuery)
		}
		if cookie, err := r.Cookie("faas_sid"); err != nil || cookie.Value != "opaque-session" {
			t.Errorf("session cookie = %v, %v", cookie, err)
		}
		writeTestJSON(w, http.StatusOK, api.GithubRecoveryStatusResponse{
			Deliveries: []api.GithubWebhookDeliveryRecord{{
				DeliveryID: testGithubDeliveryID, EventType: "push", Status: "dead", UpdatedAt: now,
			}},
			CheckUpdates: []api.GithubCheckUpdateRecord{{
				DeploymentID: testGithubDeploymentID, Generation: 3, Status: "dead", UpdatedAt: now,
			}},
		})
	}))
	defer server.Close()
	installTestOperatorSession(t, server.URL, "opaque-session")
	out, stderr, restore := captureOperatorIO()
	defer restore()
	if code := cmdGithubDispatch([]string{"status", "--status", "dead", "--limit", "25"}); code != 0 {
		t.Fatalf("exit = %d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(out.String(), "deliveries=1 check_updates=1") {
		t.Fatalf("stdout = %q", out.String())
	}
}

func TestGithubRetriesUseAuthenticatedAPI(t *testing.T) {
	for _, tc := range []struct {
		name   string
		action string
		flag   string
		id     string
		path   string
		kind   string
	}{
		{name: "delivery", action: "retry-delivery", flag: "--delivery-id", id: testGithubDeliveryID, path: "/v1/admin/ops/github/deliveries/" + testGithubDeliveryID + "/retry", kind: "delivery"},
		{name: "check", action: "retry-check", flag: "--deployment-id", id: testGithubDeploymentID, path: "/v1/admin/ops/github/check-updates/" + testGithubDeploymentID + "/retry", kind: "check_update"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != tc.path {
					t.Errorf("request = %s %s", r.Method, r.URL.Path)
				}
				if r.URL.Query().Get("confirm") != "true" || r.URL.Query().Get("reason") != "github_incident_123" {
					t.Errorf("query = %q", r.URL.RawQuery)
				}
				if r.Header.Get("Idempotency-Key") == "" || r.Header.Get(operatorTraceIDHeader) != testGithubTraceID {
					t.Errorf("headers = %+v", r.Header)
				}
				writeTestJSON(w, http.StatusOK, api.GithubRecoveryRetryResponse{
					OK: true, Kind: tc.kind, TargetID: tc.id, Status: "pending",
				})
			}))
			defer server.Close()
			installTestOperatorSession(t, server.URL, "opaque-session")
			out, stderr, restore := captureOperatorIO()
			defer restore()
			args := []string{tc.action, tc.flag, tc.id, "--reason", "github_incident_123", "--trace-id", testGithubTraceID, "--yes"}
			if code := cmdGithubDispatch(args); code != 0 {
				t.Fatalf("exit = %d stderr=%s", code, stderr.String())
			}
			if !strings.Contains(out.String(), "trace_id="+testGithubTraceID) {
				t.Fatalf("stdout = %q", out.String())
			}
		})
	}
}

func TestGithubRetryRequiresSafetyInputs(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{name: "id", args: []string{"retry-delivery", "--reason", "incident_123", "--yes"}, want: "must be a UUID"},
		{name: "reason", args: []string{"retry-delivery", "--delivery-id", testGithubDeliveryID, "--yes"}, want: "--reason is required"},
		{name: "confirmation", args: []string{"retry-delivery", "--delivery-id", testGithubDeliveryID, "--reason", "incident_123"}, want: "--yes required"},
		{name: "trace", args: []string{"retry-delivery", "--delivery-id", testGithubDeliveryID, "--reason", "incident_123", "--trace-id", "bad", "--yes"}, want: "32 lowercase hex"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, stderr, restore := captureOperatorIO()
			defer restore()
			if code := cmdGithubDispatch(tc.args); code != 2 {
				t.Fatalf("exit = %d stderr=%s", code, stderr.String())
			}
			if !strings.Contains(stderr.String(), tc.want) {
				t.Fatalf("stderr = %q, want %q", stderr.String(), tc.want)
			}
		})
	}
}
