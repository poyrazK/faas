//go:build linux

package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apptaskproto"
)

type serviceProbeTestResolver struct {
	addresses []net.IPAddr
	err       error
	queries   int
}

func (r *serviceProbeTestResolver) LookupIPAddr(_ context.Context, host string) ([]net.IPAddr, error) {
	r.queries++
	if host != "billing.internal" {
		return nil, context.DeadlineExceeded
	}
	return r.addresses, r.err
}

func TestRunServiceBindingProbeUsesVerifiedTLSAndMarkedGatewayRoute(t *testing.T) {
	cert, caPEM := serviceProbeTestCertificate(t, "billing.internal")
	var serverCalls atomic.Int32
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serverCalls.Add(1)
		if r.Method != http.MethodHead || r.URL.Path != api.ServiceBindingProbePath || r.Host != "billing.internal" {
			t.Errorf("request = %s %s host=%q", r.Method, r.URL.Path, r.Host)
		}
		if got := r.Header.Get(api.ServiceBindingProbeRequestHeader); got != api.ServiceBindingProbeVersion {
			t.Errorf("probe marker = %q", got)
		}
		w.Header().Set(api.ServiceBindingProbeResponseHeader, api.ServiceBindingProbeVersion)
		w.Header().Set(api.ServiceBindingProbeStageHeader, "complete")
		w.WriteHeader(http.StatusNoContent)
	}))
	server.TLS = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS13}
	server.StartTLS()
	defer server.Close()

	resolver := &serviceProbeTestResolver{addresses: []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}}
	report := runServiceBindingProbe(context.Background(), "billing", caPEM, resolver, func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	})
	if !report.Passed() {
		t.Fatalf("report = %+v", report)
	}
	if report.HTTPStatus != http.StatusNoContent || resolver.queries != 1 || serverCalls.Load() != 1 {
		t.Fatalf("status=%d DNS queries=%d server calls=%d", report.HTTPStatus, resolver.queries, serverCalls.Load())
	}
	if !strings.Contains(report.TLS.Detail, "certificate verified") {
		t.Fatalf("TLS detail = %q", report.TLS.Detail)
	}
}

func TestRunServiceBindingProbeFailsClosedForWrongCAAndDoesNotFollowRedirects(t *testing.T) {
	cert, serverCAPEM := serviceProbeTestCertificate(t, "billing.internal")
	_, wrongCAPEM := serviceProbeTestCertificate(t, "other.internal")
	var calls atomic.Int32
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Location", "https://public.example/health")
		w.WriteHeader(http.StatusTemporaryRedirect)
	}))
	server.TLS = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS13}
	server.StartTLS()
	defer server.Close()
	resolver := &serviceProbeTestResolver{addresses: []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}}
	dial := func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	}
	wrongCAReport := runServiceBindingProbe(context.Background(), "billing", wrongCAPEM, resolver, dial)
	if wrongCAReport.TLS.Status != "failed" || wrongCAReport.Authorization.Status != "not_checked" {
		t.Fatalf("wrong CA report = %+v", wrongCAReport)
	}
	if calls.Load() != 0 {
		t.Fatalf("untrusted TLS reached handler %d times", calls.Load())
	}

	// With the right CA the request reaches the gateway, but a redirect is not
	// treated as a canary success and is never followed to another host.
	redirectReport := runServiceBindingProbe(context.Background(), "billing", serverCAPEM, resolver, dial)
	if redirectReport.TLS.Status != "passed" || redirectReport.Routing.Status != "not_checked" || redirectReport.Passed() {
		t.Fatalf("redirect report = %+v", redirectReport)
	}
	if calls.Load() != 1 {
		t.Fatalf("redirect was followed or handler was called more than once: %d", calls.Load())
	}
}

func TestRunServiceBindingProbeReportsDNSWhenCAIsMissing(t *testing.T) {
	resolver := &serviceProbeTestResolver{addresses: []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}}
	report := runServiceBindingProbe(context.Background(), "billing", nil, resolver, func(context.Context, string, string) (net.Conn, error) {
		t.Fatal("probe attempted an HTTPS connection without a CA bundle")
		return nil, nil
	})
	if report.DNS.Status != "passed" || report.TLS.Status != "failed" ||
		report.Authorization.Status != "not_checked" || report.Routing.Status != "not_checked" {
		t.Fatalf("report = %+v", report)
	}
}

func TestExecuteServiceBindingProbeCommandRejectsUnboundHTTPSURL(t *testing.T) {
	req := apptaskproto.Request{Command: []string{api.AppTaskServiceBindingProbeCommand, "billing"}}
	manifest := api.AppManifest{Env: map[string]string{api.ServiceBindingHTTPSEnvKey("billing"): "https://other.internal"}}
	var stdout strings.Builder
	result, err := executeServiceBindingProbeCommand(context.Background(), req, manifest, nil, nil, &stdout)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != apptaskproto.StatusFailed || !strings.Contains(stdout.String(), `"authorization":{"status":"failed"`) {
		t.Fatalf("result=%+v stdout=%s", result, stdout.String())
	}
}

func serviceProbeTestCertificate(t *testing.T, dnsName string) (tls.Certificate, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: dnsName},
		DNSNames:              []string{dnsName},
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	return cert, certPEM
}
