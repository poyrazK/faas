// adr: 430 — canaries exercise identity, route enforcement, budgets and provider status.
package bindingprobe

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/outbound"
)

type outboundProbeVerifier struct{}

type outboundProbeCredential struct{ missing bool }

func (c outboundProbeCredential) Authorization(context.Context, string) (string, error) {
	if c.missing {
		return "", errors.New("PRIVATE_CREDENTIAL_ERROR")
	}
	return "Bearer PRIVATE_PROVIDER_KEY", nil
}

func (outboundProbeVerifier) Verify(token, id string) (outbound.WorkloadIdentity, error) {
	if token != "PRIVATE_ASSERTION" {
		return outbound.WorkloadIdentity{}, outbound.ErrInvalidWorkloadIdentity
	}
	return outbound.WorkloadIdentity{AccountID: "account", AppID: "app", InstanceID: "task"}, nil
}
func TestOutboundProbeUsesManagedGatewayAndNeverLeaksProviderMaterial(t *testing.T) {
	for _, tc := range []struct {
		name              string
		status            int
		route             string
		missingCredential bool
		wantPass          bool
	}{
		{"success", 200, "/health", false, true}, {"credential rejected", 401, "/health", false, false}, {"route denied", 200, "/other", false, false}, {"missing credential", 200, "/health", true, false}, {"redirect", 302, "/health", false, false}, {"budget exhausted", 200, "/health", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var providerCalls atomic.Int32
			provider := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				providerCalls.Add(1)
				if r.Header.Get("Authorization") != "Bearer PRIVATE_PROVIDER_KEY" || r.Header.Get(outbound.WorkloadIdentityHeader) != "" || r.Header.Get("Cache-Control") == "" {
					t.Errorf("incorrect forwarding: %+v", r.Header)
				}
				w.Header().Set("Location", "https://PRIVATE_REDIRECT.invalid/")
				w.Header().Set("X-Private", "PRIVATE_HEADER")
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte("PRIVATE_BODY"))
			}))
			defer provider.Close()
			id := uuid.NewString()
			integration, err := outbound.NewIntegration(id, provider.URL, "legacy-unused", []string{"app"}, 100, 10, 10, time.Second)
			if err != nil {
				t.Fatal(err)
			}
			integration.ProviderAuthMode = outbound.ProviderAuthManaged
			integration.CredentialSource = outbound.CredentialSourceCustomerSealed
			integration.AllowedMethods = []string{"GET"}
			integration.AllowedPathPrefixes = []string{tc.route}
			integration.ResponseCacheTTLSeconds = 300
			if tc.name == "budget exhausted" {
				limit := int64(1)
				integration.DailyRequestLimit = &limit
			}
			resolver, err := outbound.NewStaticResolver([]outbound.Integration{integration})
			if err != nil {
				t.Fatal(err)
			}
			handler, err := outbound.NewHandler(resolver, outbound.NewMemoryBackend(), provider.Client())
			if err != nil {
				t.Fatal(err)
			}
			handler.IdentityVerifier = outboundProbeVerifier{}
			handler.CredentialResolver = outboundProbeCredential{missing: tc.missingCredential}
			gateway := httptest.NewTLSServer(handler)
			defer gateway.Close()
			identity := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Query().Get("audience") != "gregale:outbound:"+id {
					t.Error("incorrect audience")
				}
				_, _ = w.Write([]byte(`{"access_token":"PRIVATE_ASSERTION","token_type":"Bearer","expires_in":60}`))
			}))
			defer identity.Close()
			spec := api.OutboundBindingProbeSpec{IntegrationID: id, GatewayURL: gateway.URL, Policy: api.OutboundBindingProbePolicy{Method: "GET", Path: "/health", ExpectedStatus: 200}}
			for attempt := range 2 {
				result := outboundProbe(context.Background(), spec, gateway.Client(), identity.URL)
				raw, _ := json.Marshal(result)
				wantPass := tc.wantPass && !(tc.name == "budget exhausted" && attempt == 1)
				if result.Passed() != wantPass || strings.Contains(string(raw), "PRIVATE_") {
					t.Fatalf("unsafe or wrong result=%s", raw)
				}
				if tc.name == "budget exhausted" && attempt == 1 && result.Response.Detail != "http_429" {
					t.Fatalf("budget not enforced: %+v", result)
				}
			}
			if tc.name == "budget exhausted" && providerCalls.Load() != 1 {
				t.Fatalf("over-budget probe reached provider: %d", providerCalls.Load())
			}
			if tc.wantPass && tc.name != "budget exhausted" && providerCalls.Load() != 2 {
				t.Fatalf("probe used response cache: provider calls=%d", providerCalls.Load())
			}
			if (tc.route != "/health" || tc.missingCredential) && providerCalls.Load() != 0 {
				t.Fatalf("rejected request reached provider: %d", providerCalls.Load())
			}
		})
	}
}
func TestOutboundProbeRejectsUnsafeConfigurationIdentityTLSAndTimeout(t *testing.T) {
	id := uuid.NewString()
	gateway := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	defer gateway.Close()
	identity := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"access_token":"PRIVATE_ASSERTION","token_type":"Bearer","expires_in":60}`))
	}))
	defer identity.Close()
	spec := api.OutboundBindingProbeSpec{IntegrationID: id, GatewayURL: gateway.URL, Policy: api.OutboundBindingProbePolicy{Method: "HEAD", Path: "/health", ExpectedStatus: 204}}
	for _, tc := range []struct {
		name   string
		mutate func(*api.OutboundBindingProbeSpec)
	}{
		{"write method", func(s *api.OutboundBindingProbeSpec) { s.Policy.Method = "POST" }}, {"auth expected", func(s *api.OutboundBindingProbeSpec) { s.Policy.ExpectedStatus = 401 }}, {"query", func(s *api.OutboundBindingProbeSpec) { s.Policy.Path = "/health?PRIVATE_QUERY" }}, {"origin escape", func(s *api.OutboundBindingProbeSpec) { s.Policy.Path = "//evil.invalid" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bad := spec
			tc.mutate(&bad)
			r := outboundProbe(context.Background(), bad, gateway.Client(), identity.URL)
			if r.Passed() || r.Configuration.Status != "failed" {
				t.Fatalf("accepted invalid spec: %+v", r)
			}
		})
	}
	r := outboundProbe(context.Background(), spec, &http.Client{}, identity.URL)
	if r.Passed() || r.Gateway.Status != "failed" {
		t.Fatalf("accepted untrusted TLS: %+v", r)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if r := outboundProbe(ctx, spec, gateway.Client(), identity.URL); r.Passed() {
		t.Fatal("accepted cancelled request")
	}
	slowGateway := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer slowGateway.Close()
	slowSpec := spec
	slowSpec.GatewayURL = slowGateway.URL
	deadline, stop := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer stop()
	if r := outboundProbe(deadline, slowSpec, slowGateway.Client(), identity.URL); r.Passed() || deadline.Err() == nil {
		t.Fatalf("accepted timed-out request: %+v", r)
	}
	badIdentity := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"access_token":"PRIVATE_ASSERTION","token_type":"Bearer","expires_in":60,"error":"PRIVATE_ERROR"}`))
	}))
	defer badIdentity.Close()
	r = outboundProbe(context.Background(), spec, gateway.Client(), badIdentity.URL)
	if r.Passed() || r.Identity.Status != "failed" {
		t.Fatalf("accepted failed identity: %+v", r)
	}
}
