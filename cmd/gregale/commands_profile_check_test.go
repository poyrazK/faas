package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestProfileCheckJSONReportsAccountAndEnvironmentOverrides(t *testing.T) {
	setupConnectionProfiles(t)
	token := testAPIKey('a')
	t.Setenv("FAAS_TOKEN", token)
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != http.MethodGet || r.URL.Path != "/v1/account" {
			t.Errorf("unexpected mutation/request: %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer "+token {
			t.Error("credential not sent correctly")
		}
		_, _ = w.Write([]byte(`{"id":"acct-1","email":"me@example.com","plan":"free","status":"active","tax_id":"private-tax-id"}`))
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	path, _ := cliConfigPath()
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	previousJSON := jsonOutput
	jsonOutput = true
	t.Cleanup(func() { jsonOutput = previousJSON })
	var out bytes.Buffer
	if exit := captureStdoutSwap(t, &out, func() int { return run([]string{"--profile", "staging", "profile", "check"}) }); exit != 0 {
		t.Fatalf("exit=%d output=%s", exit, out.String())
	}
	var report profileCheckReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if !report.OK || report.ExitCode != 0 || report.Authentication != "authenticated" || report.EndpointReachable == nil || !*report.EndpointReachable {
		t.Fatalf("report=%+v", report)
	}
	if report.Connection.Profile != "staging" || report.Connection.ProfileSource != "flag:--profile" || report.Connection.APIBase != server.URL || report.Connection.APISource != "env:FAAS_API" || report.CredentialSource != "env:FAAS_TOKEN" {
		t.Fatalf("connection=%+v credential_source=%s", report.Connection, report.CredentialSource)
	}
	if report.Account == nil || report.Account.ID != "acct-1" || report.Account.Email != "me@example.com" {
		t.Fatalf("account=%+v", report.Account)
	}
	if requests != 1 {
		t.Fatalf("requests=%d", requests)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("check changed saved configuration")
	}
	if profileOverride != "" {
		t.Fatal("check leaked its profile override")
	}
	if strings.Contains(out.String(), token) || strings.Contains(out.String(), "private-tax-id") {
		t.Fatal("check exposed credential or unrelated billing data")
	}
}

func TestProfileCheckReportsAuthenticationAndAPIProblems(t *testing.T) {
	for _, tc := range []struct {
		name, code, authentication, diagnostic string
		status, exit                           int
		missing                                bool
	}{
		{"missing", api.CodeUnauthorized, "missing", "credentials_missing", 401, 2, true},
		{"rejected", api.CodeUnauthorized, "rejected", "credentials_rejected", 401, 2, false},
		{"expired", api.CodeAPIKeyExpired, "expired", "credentials_expired", 401, 2, false},
		{"revoked", api.CodeAPIKeyRevoked, "revoked", "credentials_revoked", 401, 2, false},
		{"forbidden", "forbidden", "forbidden", "access_denied", 403, 2, false},
		{"server-error", "internal_error", "unknown", "api_error", 503, 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setupConnectionProfiles(t)
			token := testAPIKey('b')
			if !tc.missing {
				t.Setenv("FAAS_TOKEN", token)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.missing && r.Header.Get("Authorization") != "" {
					t.Error("missing credential inherited authentication")
				}
				w.Header().Set("Content-Type", "application/problem+json")
				w.WriteHeader(tc.status)
				_ = json.NewEncoder(w).Encode(api.Problem{Status: tc.status, Code: tc.code, Detail: "reflected credential: " + token})
			}))
			defer server.Close()
			t.Setenv("FAAS_API", server.URL)
			old := jsonOutput
			jsonOutput = true
			t.Cleanup(func() { jsonOutput = old })
			var out bytes.Buffer
			exit := captureStdoutSwap(t, &out, func() int { return cmdProfile([]string{"check"}) })
			var report profileCheckReport
			if err := json.Unmarshal(out.Bytes(), &report); err != nil {
				t.Fatal(err)
			}
			if exit != tc.exit || report.ExitCode != tc.exit || report.OK || report.Authentication != tc.authentication || report.Diagnostic == nil || report.Diagnostic.Code != tc.diagnostic || report.Diagnostic.HTTPStatus != tc.status {
				t.Fatalf("exit=%d report=%+v diagnostic=%+v", exit, report, report.Diagnostic)
			}
			if report.EndpointReachable == nil || !*report.EndpointReachable {
				t.Fatal("HTTP response did not establish endpoint reachability")
			}
			if !tc.missing && tc.status == 401 && !strings.Contains(report.Diagnostic.Hint, "FAAS_TOKEN") {
				t.Fatal("hint does not explain overriding credential")
			}
			if strings.Contains(out.String(), token) {
				t.Fatal("server-reflected credential leaked")
			}
		})
	}
}

type profileCheckTransport func(*http.Request) (*http.Response, error)

func (f profileCheckTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestProfileCheckNetworkAndCancellationDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		name, code string
		err        error
		exit       int
	}{
		{"dns", "dns_failed", &net.DNSError{Err: "no such host", Name: "invalid.example"}, 3},
		{"connection", "connection_failed", &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("refused")}, 3},
		{"timeout", "timeout", context.DeadlineExceeded, 124},
		{"cancelled", "cancelled", context.Canceled, 130},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := NewClient("https://invalid.example", testAPIKey('a'))
			client.HTTPClient().Transport = profileCheckTransport(func(r *http.Request) (*http.Response, error) { return nil, tc.err })
			report := checkProfileConnection(t.Context(), client, &connectionContext{Profile: "default", APIBase: "https://invalid.example"}, "profile:default", true)
			if report.ExitCode != tc.exit || report.Diagnostic == nil || report.Diagnostic.Code != tc.code {
				t.Fatalf("report=%+v diagnostic=%+v", report, report.Diagnostic)
			}
		})
	}
}

func TestProfileCheckTLSAndMalformedResponses(t *testing.T) {
	setupConnectionProfiles(t)
	tlsServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"id":"acct"}`)) }))
	defer tlsServer.Close()
	client := NewClient(tlsServer.URL, testAPIKey('a'))
	report := checkProfileConnection(t.Context(), client, &connectionContext{Profile: "default", APIBase: tlsServer.URL}, "profile:default", true)
	if report.ExitCode != 3 || report.Diagnostic.Code != "tls_failed" {
		t.Fatalf("TLS report=%+v diagnostic=%+v", report, report.Diagnostic)
	}
	for _, body := range []string{"not JSON", "{}", "null"} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) }))
		client := NewClient(server.URL, testAPIKey('a'))
		report := checkProfileConnection(t.Context(), client, &connectionContext{Profile: "default", APIBase: server.URL}, "profile:default", true)
		server.Close()
		if report.ExitCode != 1 || report.Diagnostic.Code != "invalid_response" || report.EndpointReachable == nil || !*report.EndpointReachable {
			t.Fatalf("body=%q report=%+v diagnostic=%+v", body, report, report.Diagnostic)
		}
	}
}

func TestProfileCheckRejectsUsageAndSecretBearingEndpoint(t *testing.T) {
	setupConnectionProfiles(t)
	for _, args := range [][]string{{"--timeout", "0s"}, {"--timeout", "-1s"}, {"unexpected"}} {
		if exit := cmdProfile(append([]string{"check"}, args...)); exit != 1 {
			t.Fatalf("invalid arguments %v: exit=%d", args, exit)
		}
	}
	t.Setenv("FAAS_API", "https://user:secret-password@invalid.example")
	old := jsonOutput
	jsonOutput = true
	t.Cleanup(func() { jsonOutput = old })
	stderr, restore := captureStderr(t)
	defer restore()
	if exit := cmdProfile([]string{"check"}); exit != 1 {
		t.Fatalf("invalid endpoint exit=%d", exit)
	}
	if strings.Contains(stderr.String(), "secret-password") {
		t.Fatal("invalid endpoint exposed embedded credential")
	}
}
