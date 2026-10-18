package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

type hostingOIDCRoundTrip func(*http.Request) (*http.Response, error)

func (f hostingOIDCRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func hostingOIDCJSON(value any) *http.Response {
	b, _ := json.Marshal(value)
	return &http.Response{StatusCode: 200, ContentLength: -1, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(b)))}
}

func TestHostingOIDCClientRenewsWithoutReallocatingOrLeakingCredentials(t *testing.T) {
	// adr: 202610171028 — refresh before requests, retain original CI intent, never replay failed writes.
	synctest.Test(t, func(t *testing.T) {
		site := "11111111-1111-4111-8111-111111111111"
		trust := "22222222-2222-4222-8222-222222222222"
		run := "33333333-3333-4333-8333-333333333333"
		client, err := NewGitHubHostingOIDCClient("https://api.example.com", site, trust, "https://runner.actions.githubusercontent.com/job?api-version=1&audience=wrong", "github-request-secret")
		if err != nil {
			t.Fatal(err)
		}
		transport := client.http.Transport.(*hostingOIDCTransport)
		exchanges, githubRequests, writes := 0, 0, 0
		failExchange := false
		issued := ""
		transport.source.http.Transport = hostingOIDCRoundTrip(func(r *http.Request) (*http.Response, error) {
			if r.URL.Host == "runner.actions.githubusercontent.com" {
				githubRequests++
				if r.Header.Get("Authorization") != "Bearer github-request-secret" || r.URL.Query().Get("audience") != HostingOIDCAudience(site) {
					t.Fatal("GitHub token destination/audience", r.URL)
				}
				return hostingOIDCJSON(map[string]string{"value": "github-signed-jwt"}), nil
			}
			exchanges++
			if r.URL.String() != "https://api.example.com/v1/internal/hosting/oidc/exchange" || r.Header.Get("Authorization") != "" || r.Header.Get("Idempotency-Key") != "" {
				t.Fatal("exchange forwarded credential or replay key")
			}
			var req HostingOIDCExchangeRequest
			if json.NewDecoder(r.Body).Decode(&req) != nil || req.Assertion != "github-signed-jwt" || req.SiteID != site || req.TrustID != trust {
				t.Fatal("wrong exchange")
			}
			if failExchange {
				return &http.Response{StatusCode: 401, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("github-signed-jwt github-request-secret"))}, nil
			}
			issued = "fp_host_" + strings.Repeat(string(rune('a'+exchanges)), 64)
			return hostingOIDCJSON(HostingAutomationCredentialResponse{Token: issued, Credential: HostingAutomationCredential{SiteID: site, OIDCTrustID: trust, Operations: []string{"stage", "preview"}, ExpiresAt: time.Now().Add(5 * time.Minute)}}), nil
		})
		transport.next = hostingOIDCRoundTrip(func(r *http.Request) (*http.Response, error) {
			if r.Header.Get("Authorization") != "Bearer "+issued {
				t.Fatal("old token used")
			}
			if r.Method == "PUT" {
				writes++
				if !strings.HasSuffix(r.URL.Path, "/ci/"+run) {
					t.Fatal("intent changed on renewal")
				}
				return hostingOIDCJSON(HostingCIRun{ID: run, SiteID: site}), nil
			}
			return hostingOIDCJSON(HostingCapabilities{}), nil
		})
		if _, err = client.CreateHostingCIRun(t.Context(), site, run, HostingCIRunRequest{}); err != nil {
			t.Fatal(err)
		}
		if _, err = client.GetHostingCapabilities(t.Context()); err != nil || exchanges != 1 {
			t.Fatal("unnecessary exchange", err, exchanges)
		}
		time.Sleep(4*time.Minute + 40*time.Second)
		if _, err = client.CreateHostingCIRun(t.Context(), site, run, HostingCIRunRequest{}); err != nil || exchanges != 2 || githubRequests != 2 || writes != 2 {
			t.Fatal("renewal", err, exchanges, writes)
		}
		time.Sleep(5 * time.Minute)
		failExchange = true
		if _, err = client.CreateHostingCIRun(t.Context(), site, run, HostingCIRunRequest{}); err == nil || strings.Contains(err.Error(), "github-signed-jwt") || strings.Contains(err.Error(), "github-request-secret") || writes != 2 {
			t.Fatal("failed renewal leaked secrets or replayed write", err, writes)
		}
		if _, err = client.GetHostingSite(t.Context(), trust); err == nil {
			t.Fatal("cross-site token forwarding")
		}
	})
}

func TestHostingOIDCRejectsUntrustedEndpoints(t *testing.T) {
	site := "11111111-1111-4111-8111-111111111111"
	trust := "22222222-2222-4222-8222-222222222222"
	for _, u := range []string{"http://runner.actions.githubusercontent.com/job", "https://evil.example/job", "https://actions.githubusercontent.com.evil.example/job", "https://user:secret@runner.actions.githubusercontent.com/job", "https://runner.actions.githubusercontent.com:444/job", "https://runner.actions.githubusercontent.com/job#fragment"} {
		if _, err := NewGitHubHostingOIDCClient("https://api.example.com", site, trust, u, "secret"); err == nil {
			t.Fatal("untrusted GitHub token destination", u)
		}
	}
	for _, base := range []string{"http://api.example.com", "https://user:secret@api.example.com", "https://api.example.com/path", "https://api.example.com?token=secret"} {
		if _, err := NewGitHubHostingOIDCClient(base, site, trust, "https://runner.actions.githubusercontent.com/job", "secret"); err == nil {
			t.Fatal("untrusted API origin", base)
		}
	}
}

func TestHostingOIDCClientNeverFollowsIdentityExchangeOrAPIRedirects(t *testing.T) {
	site := "11111111-1111-4111-8111-111111111111"
	trust := "22222222-2222-4222-8222-222222222222"
	for _, redirectAt := range []string{"github", "exchange", "api"} {
		t.Run(redirectAt, func(t *testing.T) {
			client, err := NewGitHubHostingOIDCClient("https://api.example.com", site, trust, "https://runner.actions.githubusercontent.com/job", "github-secret")
			if err != nil {
				t.Fatal(err)
			}
			transport := client.http.Transport.(*hostingOIDCTransport)
			hits := 0
			redirect := func() *http.Response {
				return &http.Response{StatusCode: 302, ContentLength: -1, Header: http.Header{"Location": []string{"https://attacker.example/steal"}}, Body: io.NopCloser(strings.NewReader("private-jwt"))}
			}
			transport.source.http.Transport = hostingOIDCRoundTrip(func(r *http.Request) (*http.Response, error) {
				hits++
				if r.URL.Host == "attacker.example" {
					t.Fatal("identity credential followed redirect")
				}
				if strings.HasSuffix(r.URL.Host, ".actions.githubusercontent.com") {
					if redirectAt == "github" {
						return redirect(), nil
					}
					return hostingOIDCJSON(map[string]string{"value": "private-jwt"}), nil
				}
				if redirectAt == "exchange" {
					return redirect(), nil
				}
				return hostingOIDCJSON(HostingAutomationCredentialResponse{Token: "fp_host_" + strings.Repeat("a", 64), Credential: HostingAutomationCredential{SiteID: site, OIDCTrustID: trust, Operations: []string{"stage", "preview"}, ExpiresAt: time.Now().Add(time.Minute)}}), nil
			})
			transport.next = hostingOIDCRoundTrip(func(r *http.Request) (*http.Response, error) {
				hits++
				if r.URL.Host != "api.example.com" {
					t.Fatal("Gregale credential followed redirect")
				}
				return redirect(), nil
			})
			if _, err = client.GetHostingCapabilities(t.Context()); err == nil {
				t.Fatal("redirect accepted")
			}
			want := map[string]int{"github": 1, "exchange": 2, "api": 3}[redirectAt]
			if hits != want {
				t.Fatal("unexpected redirect requests", hits, want)
			}
		})
	}
}
