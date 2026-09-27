// adr: 206
package gateway

import (
	"bufio"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
)

func assertionProxy(t *testing.T, caller ServiceCaller, mint ServiceCallerMinter, seen *http.Header) *ServiceProxy {
	t.Helper()
	return NewServiceProxy(ServiceProxyConfig{
		Provider: staticProvider{endpoints: []ServiceEndpoint{{InstanceID: "i", NodeID: "n", Port: 8080}}},
		Resolve: func(context.Context, string, string) (ServiceTarget, bool, error) {
			return ServiceTarget{AppID: "app-target"}, true, nil
		},
		Authorize:           func(context.Context, string, string) (ServiceCaller, error) { return caller, nil },
		MintCallerAssertion: mint,
		Forward: func(Target) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				*seen = r.Header.Clone()
				w.WriteHeader(http.StatusOK)
			})
		},
		EndpointTTL: time.Minute,
		Now:         func() time.Time { return time.Unix(100, 0) },
	})
}

func assertionCall(t *testing.T, proxy *ServiceProxy) int {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "http://gateway/v1/internal/services/orders/health", nil)
	req.Header.Set(ServiceProxyCallerAppHeader, "app-client")
	rec := httptest.NewRecorder()
	proxy.ServeHTTP(rec, req)
	return rec.Code
}

// The minter must receive the identity the authorizer verified, and the target
// must receive the resulting assertion.
func TestServiceProxyAttachesCallerAssertion(t *testing.T) {
	var got ServiceCallerMintInput
	var seen http.Header
	proxy := assertionProxy(t,
		ServiceCaller{AppID: "app-caller", AccountID: "acct-1", InstanceID: "inst-9", PreviewOfSlug: "public-api"},
		func(in ServiceCallerMintInput) (string, error) {
			got = in
			return "signed-token", nil
		}, &seen)

	if code := assertionCall(t, proxy); code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if seen.Get(ServiceCallerAssertionHeader) != "signed-token" {
		t.Errorf("%s = %q, want signed-token", ServiceCallerAssertionHeader, seen.Get(ServiceCallerAssertionHeader))
	}
	if got.CallerAppID != "app-caller" || got.TargetAppID != "app-target" {
		t.Errorf("mint input caller/target = %q/%q, want app-caller/app-target", got.CallerAppID, got.TargetAppID)
	}
	if got.AccountID != "acct-1" || got.CallerInstanceID != "inst-9" {
		t.Errorf("mint input account/instance = %q/%q, want acct-1/inst-9", got.AccountID, got.CallerInstanceID)
	}
	// The preview marker and the assertion must agree; a target that trusts
	// one and not the other would see two different stories about one call.
	if got.CallerEnv != "preview" {
		t.Errorf("mint input CallerEnv = %q, want preview", got.CallerEnv)
	}
}

// The real VMMD forwarder strips platform-owned x-faas-* metadata by default.
// The assertion minted by ServiceProxy is the one exception; a caller's
// forged values must be replaced before that trusted context is set.
func TestServiceProxyCallerAssertionCrossesVMMDHTTPBridge(t *testing.T) {
	cli := &stubVmmdClient{resp: &vmmdpb.ForwardHTTPResponseInit{Status: http.StatusOK}}
	proxy := NewServiceProxy(ServiceProxyConfig{
		Provider: staticProvider{endpoints: []ServiceEndpoint{{InstanceID: "i", NodeID: "n", Port: 8080}}},
		Resolve: func(context.Context, string, string) (ServiceTarget, bool, error) {
			return ServiceTarget{AppID: "app-target"}, true, nil
		},
		Authorize: func(context.Context, string, string) (ServiceCaller, error) {
			return ServiceCaller{AppID: "app-caller", AccountID: "acct-1"}, nil
		},
		MintCallerAssertion: func(ServiceCallerMintInput) (string, error) { return "signed-token", nil },
		Forward:             ForwardingReverseProxy(&stubLookup{cli: cli}, nil),
		EndpointTTL:         time.Minute,
		Now:                 func() time.Time { return time.Unix(100, 0) },
	})

	req := httptest.NewRequest(http.MethodGet, "http://gateway/v1/internal/services/orders/health", nil)
	req.Header.Set(ServiceProxyCallerAppHeader, "app-client")
	req.Header.Add(ServiceCallerAssertionHeader, "forged.first.token")
	req.Header.Add(ServiceCallerAssertionHeader, "forged.second.token")
	rec := httptest.NewRecorder()
	proxy.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if len(cli.calls) != 1 {
		t.Fatalf("VMMD received %d requests, want one", len(cli.calls))
	}
	var assertions []string
	for _, header := range cli.calls[0].GetHeaders() {
		if strings.EqualFold(header.GetName(), ServiceCallerAssertionHeader) {
			assertions = append(assertions, header.GetValue())
		}
	}
	if len(assertions) != 1 || assertions[0] != "signed-token" {
		t.Fatalf("VMMD caller assertion headers = %q, want exactly [signed-token]", assertions)
	}
}

func TestRawRequestHeadForwardsOnlyTrustedServiceCallerAssertion(t *testing.T) {
	for _, tc := range []struct {
		name    string
		trusted bool
		want    string
	}{
		{name: "untrusted header is stripped"},
		{name: "trusted service assertion is forwarded", trusted: true, want: "signed-token"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "http://service.internal/", nil)
			req.Header.Set("Connection", "Upgrade")
			req.Header.Set("Upgrade", "websocket")
			req.Header.Set(ServiceCallerAssertionHeader, "signed-token")
			if tc.trusted {
				req = req.WithContext(withTrustedServiceCallerAssertion(req.Context()))
			}

			head, err := rawRequestHead(req)
			if err != nil {
				t.Fatalf("build request head: %v", err)
			}
			parsed, err := http.ReadRequest(bufio.NewReader(strings.NewReader(string(head))))
			if err != nil {
				t.Fatalf("parse request head: %v", err)
			}
			if got := parsed.Header.Get(ServiceCallerAssertionHeader); got != tc.want {
				t.Errorf("forwarded assertion = %q, want %q", got, tc.want)
			}
		})
	}
}

// Nothing verifies assertions yet, so a signing failure must not cost a
// working call — that would trade the mesh for a feature with no consumer.
func TestServiceProxyForwardsWhenMintFails(t *testing.T) {
	var seen http.Header
	proxy := assertionProxy(t, ServiceCaller{AppID: "app-caller"},
		func(ServiceCallerMintInput) (string, error) { return "", errors.New("key unavailable") }, &seen)

	if code := assertionCall(t, proxy); code != http.StatusOK {
		t.Fatalf("status = %d, want 200 despite a mint failure", code)
	}
	if got := seen.Get(ServiceCallerAssertionHeader); got != "" {
		t.Errorf("%s = %q, want unset after a mint failure", ServiceCallerAssertionHeader, got)
	}
}

// A nil minter is the default (feature off): the hop must be byte-identical to
// the pre-ADR-206 behaviour.
func TestServiceProxyWithoutMinterAttachesNothing(t *testing.T) {
	var seen http.Header
	proxy := assertionProxy(t, ServiceCaller{AppID: "app-caller"}, nil, &seen)

	if code := assertionCall(t, proxy); code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if got := seen.Get(ServiceCallerAssertionHeader); got != "" {
		t.Errorf("%s = %q, want unset when minting is off", ServiceCallerAssertionHeader, got)
	}
}

// The assertion header is platform-owned. A guest that sends one inbound must
// not have it reach the target, or the signature guarantee is worthless.
func TestServiceProxyStripsSpoofedAssertion(t *testing.T) {
	var seen http.Header
	proxy := assertionProxy(t, ServiceCaller{AppID: "app-caller"}, nil, &seen)

	req := httptest.NewRequest(http.MethodGet, "http://gateway/v1/internal/services/orders/health", nil)
	req.Header.Set(ServiceProxyCallerAppHeader, "app-client")
	req.Header.Set(ServiceCallerAssertionHeader, "forged.jwt.value")
	proxy.ServeHTTP(httptest.NewRecorder(), req)

	if got := seen.Get(ServiceCallerAssertionHeader); got != "" {
		t.Errorf("%s = %q, want the forged value stripped", ServiceCallerAssertionHeader, got)
	}
}
