//go:build linux

package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apptaskproto"
)

// adr: 249
func TestRunServiceBindingSmokeUsesVerifiedTLSAndPinnedDeployment(t *testing.T) {
	cert, caPEM := serviceProbeTestCertificate(t, "billing.internal")
	const targetDeployment = "d6e281f3-f5b2-436c-b4ad-8529a956609c"
	var serverCalls atomic.Int32
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serverCalls.Add(1)
		if r.Method != http.MethodGet || r.URL.Path != "/health" || r.URL.RawQuery != "token=do-not-report" || r.Host != "billing.internal" {
			t.Errorf("request = %s %s?%s host=%q", r.Method, r.URL.Path, r.URL.RawQuery, r.Host)
		}
		if got := r.Header.Get(api.TargetDeploymentHeader); got != targetDeployment {
			t.Errorf("target deployment header = %q, want %q", got, targetDeployment)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("do-not-report-response-body"))
	}))
	server.TLS = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS13}
	server.StartTLS()
	defer server.Close()
	resolver := &serviceProbeTestResolver{addresses: []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}}
	report := runServiceBindingSmoke(context.Background(), "billing", targetDeployment, "/health?token=do-not-report", http.StatusOK, caPEM, resolver, func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	})
	if !report.Passed || report.HTTPStatus != http.StatusOK || report.Path != "/health" || report.TargetDeploymentID != targetDeployment {
		t.Fatalf("report = %+v", report)
	}
	if resolver.queries != 1 || serverCalls.Load() != 1 {
		t.Fatalf("DNS queries=%d server calls=%d, want one each", resolver.queries, serverCalls.Load())
	}
	serialized, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(serialized), "do-not-report") {
		t.Fatalf("report contains query or response-body data: %s", serialized)
	}
}

// adr: 249
func TestRunServiceBindingSmokeDoesNotFollowRedirectsOrAcceptUntrustedTLS(t *testing.T) {
	cert, caPEM := serviceProbeTestCertificate(t, "billing.internal")
	_, wrongCAPEM := serviceProbeTestCertificate(t, "other.internal")
	const targetDeployment = "d6e281f3-f5b2-436c-b4ad-8529a956609c"
	var serverCalls atomic.Int32
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		serverCalls.Add(1)
		w.Header().Set("Location", "https://outside.example/health")
		w.WriteHeader(http.StatusTemporaryRedirect)
	}))
	server.TLS = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS13}
	server.StartTLS()
	defer server.Close()
	resolver := &serviceProbeTestResolver{addresses: []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}}
	dial := func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	}
	wrongCA := runServiceBindingSmoke(context.Background(), "billing", targetDeployment, "/health", 0, wrongCAPEM, resolver, dial)
	if wrongCA.Passed || wrongCA.HTTPStatus != 0 || serverCalls.Load() != 0 {
		t.Fatalf("wrong-CA report = %+v; handler calls=%d", wrongCA, serverCalls.Load())
	}
	redirect := runServiceBindingSmoke(context.Background(), "billing", targetDeployment, "/health", 0, caPEM, resolver, dial)
	if redirect.Passed || redirect.HTTPStatus != http.StatusTemporaryRedirect || !strings.Contains(redirect.Error, "unexpected HTTP status") {
		t.Fatalf("redirect report = %+v", redirect)
	}
	if serverCalls.Load() != 1 {
		t.Fatalf("redirect was followed or untrusted TLS reached handler: calls=%d", serverCalls.Load())
	}
}

// adr: 249
func TestExecuteAppTaskCommandReservesServiceBindingSmokeOperation(t *testing.T) {
	var stdout strings.Builder
	result, err := executeAppTaskCommand(context.Background(), apptaskproto.Request{
		Command: []string{api.AppTaskServiceBindingSmokeCommand},
	}, api.AppManifest{}, nil, nil, &stdout, &strings.Builder{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != apptaskproto.StatusFailed || result.FailureCode != "service_binding_smoke_failed" {
		t.Fatalf("result = %+v, want reserved platform smoke failure", result)
	}
	var report api.ServiceBindingSmokeReport
	if err := json.Unmarshal([]byte(stdout.String()), &report); err != nil {
		t.Fatalf("decode platform report: %v; output=%q", err, stdout.String())
	}
	if report.Passed || report.Error == "" {
		t.Fatalf("report = %+v, want validation failure", report)
	}
}
