package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const testAccountID = "11111111-1111-1111-1111-111111111111"

func TestAccountsReadCommandsUseOperatorSession(t *testing.T) {
	now := time.Date(2026, time.September, 9, 12, 0, 0, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("faas_sid")
		if err != nil || cookie.Value != "opaque-session" {
			t.Errorf("session cookie = %v, %v", cookie, err)
		}
		if r.UserAgent() != operatorUserAgent {
			t.Errorf("User-Agent = %q", r.UserAgent())
		}
		switch r.URL.Path {
		case "/v1/admin/obs/tenants":
			if r.URL.Query().Get("limit") != "25" || r.URL.Query().Get("status") != "suspended" || r.URL.Query().Get("include_pii") != "1" {
				t.Errorf("list query = %q", r.URL.RawQuery)
			}
			writeTestJSON(w, http.StatusOK, api.ObsTenantListResponse{
				Items: []api.ObsTenantRow{{AccountID: testAccountID, Status: "suspended", Plan: "hobby", Email: "tenant@example.com"}},
			})
		case "/v1/admin/obs/tenants/" + testAccountID:
			writeTestJSON(w, http.StatusOK, api.ObsTenantDetailResponse{
				Account: api.ObsTenantRow{AccountID: testAccountID, Status: "active", Plan: "pro"},
				Apps:    []api.ObsTenantApp{{ID: "app-1", Slug: "orders", Status: "active", Deployments: 2}},
				APIKeys: api.ObsTenantCounts{Active: 1}, Sessions: api.ObsTenantCounts{Active: 2},
			})
		case "/v1/admin/obs/tenants/" + testAccountID + "/360":
			if r.URL.Query().Get("month") != "2026-09" {
				t.Errorf("360 query = %q", r.URL.RawQuery)
			}
			writeTestJSON(w, http.StatusOK, api.ObsTenant360Response{
				Account: api.ObsTenantRow{AccountID: testAccountID, Status: "active", Plan: "pro"},
				Usage:   api.ObsTenantUsage{Month: "2026-09", UsedGBHours: 12.5, Requests: 42},
				Billing: api.ObsTenantBilling{CurrentMonthOverageCents: 125},
			})
		case "/v1/admin/obs/tenants/" + testAccountID + "/activity":
			if r.URL.Query().Get("limit") != "20" {
				t.Errorf("activity query = %q", r.URL.RawQuery)
			}
			writeTestJSON(w, http.StatusOK, api.ObsTenantActivityResponse{
				AccountID: testAccountID, GeneratedAt: now, Limit: 20,
				Invocations: []api.ObsInvocationRow{{ID: "inv-1", AppSlug: "orders", State: "succeeded", CreatedAt: now}},
				AuditEvents: []api.ObsAuditActivityRow{{ID: "audit-1", At: now, Kind: "auth.login"}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	installTestOperatorSession(t, server.URL, "opaque-session")
	out, stderr, restore := captureOperatorIO()
	defer restore()

	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "list", args: []string{"list", "--limit", "25", "--status", "suspended", "--include-pii"}, want: "email=tenant@example.com"},
		{name: "show", args: []string{"show", "--account-id", testAccountID}, want: "app id=app-1 slug=orders"},
		{name: "360", args: []string{"360", "--account-id", testAccountID, "--month", "2026-09"}, want: "gb_hours=12.500"},
		{name: "activity", args: []string{"activity", "--account-id", testAccountID, "--limit", "20"}, want: "audit id=audit-1"},
	}
	for _, tc := range tests {
		out.Reset()
		stderr.Reset()
		if code := cmdAccountsDispatch(tc.args); code != 0 {
			t.Fatalf("%s exit = %d, stderr=%s", tc.name, code, stderr.String())
		}
		if !strings.Contains(out.String(), tc.want) {
			t.Errorf("%s stdout = %q, want substring %q", tc.name, out.String(), tc.want)
		}
	}
}

func TestAccountsMutationsSendSafetyAndTraceHeaders(t *testing.T) {
	const traceID = "4bf92f3577b34da6a3ce929d0e0e4736"
	for _, action := range []string{"suspend", "restore", "revoke-sessions"} {
		t.Run(action, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/v1/admin/ops/accounts/"+testAccountID+"/"+action {
					t.Errorf("request = %s %s", r.Method, r.URL.Path)
				}
				if r.URL.Query().Get("confirm") != "true" || r.URL.Query().Get("reason") != "incident_123" {
					t.Errorf("query = %q", r.URL.RawQuery)
				}
				if r.Header.Get("Idempotency-Key") == "" {
					t.Error("missing Idempotency-Key")
				}
				if got := r.Header.Get(operatorTraceIDHeader); got != traceID {
					t.Errorf("trace id = %q, want %q", got, traceID)
				}
				if cookie, err := r.Cookie("faas_sid"); err != nil || cookie.Value != "opaque-session" {
					t.Errorf("session cookie = %v, %v", cookie, err)
				}
				writeTestJSON(w, http.StatusOK, api.ObsAccountMutationResponse{
					Account: api.ObsTenantRow{AccountID: testAccountID, Status: "active"}, Action: action, RevokedSessions: 2,
				})
			}))
			defer server.Close()

			installTestOperatorSession(t, server.URL, "opaque-session")
			out, stderr, restore := captureOperatorIO()
			defer restore()
			code := cmdAccountsDispatch([]string{action, "--account-id", testAccountID, "--reason", "incident_123", "--trace-id", traceID, "--yes"})
			if code != 0 {
				t.Fatalf("exit = %d, stderr=%s", code, stderr.String())
			}
			if !strings.Contains(out.String(), "trace_id="+traceID) || !strings.Contains(out.String(), "action="+action) {
				t.Fatalf("stdout = %q", out.String())
			}
		})
	}
}

func TestAccountsMutationRequiresReasonAndConfirmation(t *testing.T) {
	_, stderr, restore := captureOperatorIO()
	defer restore()
	if code := cmdAccountsDispatch([]string{"suspend", "--account-id", testAccountID, "--yes"}); code != 2 {
		t.Fatalf("missing reason exit = %d", code)
	}
	if !strings.Contains(stderr.String(), "--reason is required") {
		t.Fatalf("missing reason stderr = %q", stderr.String())
	}
	stderr.Reset()
	if code := cmdAccountsDispatch([]string{"suspend", "--account-id", testAccountID, "--reason", "incident_123"}); code != 2 {
		t.Fatalf("missing confirmation exit = %d", code)
	}
	if !strings.Contains(stderr.String(), "--yes required") {
		t.Fatalf("missing confirmation stderr = %q", stderr.String())
	}
}

func TestAccountsMutationExplainsStepUp(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeTestJSON(w, http.StatusForbidden, api.Problem{Status: http.StatusForbidden, Code: api.CodeStepUpRequired})
	}))
	defer server.Close()
	installTestOperatorSession(t, server.URL, "opaque-session")
	_, stderr, restore := captureOperatorIO()
	defer restore()
	if code := cmdAccountsDispatch([]string{"restore", "--account-id", testAccountID, "--reason", "incident_123", "--yes"}); code != 1 {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(stderr.String(), "gregalectl auth step-up") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}
