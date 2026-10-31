//go:build linux

// adr: 20261019200059 — HTTP callers probe over the plaintext service proxy.
package main

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

type serviceHostTestResolver struct{ queries []string }

func (r *serviceHostTestResolver) LookupIPAddr(_ context.Context, host string) ([]net.IPAddr, error) {
	r.queries = append(r.queries, host)
	if host != "billing.svc.gregale" {
		return nil, context.DeadlineExceeded
	}
	return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
}

func TestRunServiceBindingHTTPProbeUsesPlaintextProxyHost(t *testing.T) {
	for _, tc := range []struct {
		name   string
		stage  string
		passed bool
	}{
		{name: "confirmed", stage: "complete", passed: true},
		{name: "undeclared binding", stage: "binding"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodHead || r.URL.Path != api.ServiceBindingProbePath || r.Host != "billing.svc.gregale:10081" || r.TLS != nil {
					t.Errorf("request = %s %s host=%q tls=%v", r.Method, r.URL.Path, r.Host, r.TLS != nil)
				}
				if r.Header.Get(api.ServiceBindingProbeRequestHeader) != api.ServiceBindingProbeVersion {
					t.Error("probe marker missing")
				}
				w.Header().Set(api.ServiceBindingProbeResponseHeader, api.ServiceBindingProbeVersion)
				w.Header().Set(api.ServiceBindingProbeStageHeader, tc.stage)
				if tc.stage == "complete" {
					w.WriteHeader(http.StatusNoContent)
					return
				}
				w.WriteHeader(http.StatusForbidden)
			}))
			defer server.Close()
			resolver := &serviceHostTestResolver{}
			report := runServiceBindingHTTPProbe(context.Background(), "billing", "", nil, resolver, func(ctx context.Context, network, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
			})
			if report.Passed() != tc.passed || report.Transport != api.ServiceBindingTransportHTTP || report.URL != "http://billing.svc.gregale:10081" ||
				report.TLS.Status != "not_checked" || len(resolver.queries) != 1 {
				t.Fatalf("report = %+v queries=%v", report, resolver.queries)
			}
			if !tc.passed && report.Authorization.Status != "failed" {
				t.Fatalf("binding rejection not reported: %+v", report.Authorization)
			}
		})
	}
}

func TestRunServiceBindingHTTPProbeReportsDNSFailure(t *testing.T) {
	report := runServiceBindingHTTPProbe(context.Background(), "ledger", "", nil, &serviceHostTestResolver{}, nil)
	if report.Passed() || report.DNS.Status != "failed" || report.Error == "" {
		t.Fatalf("report = %+v", report)
	}
}
