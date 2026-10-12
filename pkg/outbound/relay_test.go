package outbound

import (
	"bufio"
	"bytes"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/outbound/relaywire"
	"github.com/onebox-faas/faas/pkg/workloadidentity"
)

type relayFixture struct {
	addr    string
	signer  *workloadidentity.Signer
	headers chan http.Header
}

// startRelayFixture serves a managed OpenAI-format integration, a managed
// Anthropic integration and a token-auth integration behind a relay listener.
func startRelayFixture(t *testing.T) relayFixture {
	t.Helper()
	headers := make(chan http.Header, 8)
	provider := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headers <- r.Header.Clone()
		_, _ = w.Write([]byte("provider-ok"))
	}))
	t.Cleanup(provider.Close)
	managed := func(id, aiProvider string) Integration {
		integration, err := NewIntegration(id, provider.URL, "unused-token", []string{"app-1"}, 100, 10, 10, 5*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		integration.ProviderAuthMode = ProviderAuthManaged
		integration.AllowedMethods = []string{http.MethodPost}
		integration.AllowedPathPrefixes = []string{"/v1"}
		integration.AIProvider = aiProvider
		return integration
	}
	legacy, err := NewIntegration("legacy", provider.URL, "legacy-token", []string{"app-1"}, 100, 10, 10, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := NewStaticResolver([]Integration{managed("openai", api.OutboundAIProviderOpenAI), managed("anthropic", api.OutboundAIProviderAnthropic), legacy})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandler(resolver, NewMemoryBackend(), provider.Client())
	if err != nil {
		t.Fatal(err)
	}
	if err := handler.SetManagedAuthorizations(map[string]string{"openai": "Bearer sk-openai", "anthropic": "Bearer sk-ant"}); err != nil {
		t.Fatal(err)
	}
	signer, jwks := testWorkloadSigner(t, workloadidentity.DefaultIssuer)
	verifier, err := NewWorkloadIdentityVerifier(jwks, workloadidentity.DefaultIssuer)
	if err != nil {
		t.Fatal(err)
	}
	tcp, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	listener, err := NewRelayListener(tcp, verifier, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewRelayServer(handler, verifier, 5*time.Second, 30*time.Second, 30*time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	return relayFixture{addr: tcp.Addr().String(), signer: signer, headers: headers}
}

func (f relayFixture) assertion(t *testing.T, audience string) string {
	t.Helper()
	token, err := f.signer.Mint(time.Now(), "account-1", "app-1", "instance-1", audience)
	if err != nil {
		t.Fatal(err)
	}
	return token.AccessToken
}

// relayDo opens a relay connection, writes the preamble and one raw HTTP
// request exactly as a guest would, and returns the response.
func (f relayFixture) relayDo(t *testing.T, assertion, rawRequest string) (*http.Response, error) {
	t.Helper()
	conn, err := net.Dial("tcp", f.addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	if err := relaywire.Write(conn, relaywire.Preamble{Assertion: assertion}); err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(conn, rawRequest); err != nil {
		return nil, err
	}
	return http.ReadResponse(bufio.NewReader(conn), nil)
}

func relayPost(path, extraHeaders string) string {
	return "POST " + path + " HTTP/1.1\r\nHost: 127.0.0.1:2774\r\nContent-Length: 2\r\n" + extraHeaders + "\r\n{}"
}

func TestRelayAttributesRequestsToTheConnectionApp(t *testing.T) {
	f := startRelayFixture(t)
	spoofed := "Authorization: Bearer placeholder\r\n" + AppHeader + ": app-2\r\n" + WorkloadIdentityHeader + ": forged\r\n"
	resp, err := f.relayDo(t, f.assertion(t, workloadidentity.OutboundRelayAudience), relayPost("/i/openai/v1/chat/completions", spoofed))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || string(body) != "provider-ok" {
		t.Fatalf("relay response = %d %q", resp.StatusCode, body)
	}
	got := <-f.headers
	if got.Get("Authorization") != "Bearer sk-openai" || got.Get(AppHeader) != "" || got.Get(WorkloadIdentityHeader) != "" {
		t.Fatalf("provider headers = %v", got)
	}
}

func TestRelaySendsAnthropicCredentialAsAPIKey(t *testing.T) {
	f := startRelayFixture(t)
	resp, err := f.relayDo(t, f.assertion(t, workloadidentity.OutboundRelayAudience),
		relayPost("/i/anthropic/v1/messages", "x-api-key: placeholder\r\nAuthorization: Bearer placeholder\r\nanthropic-version: 2023-06-01\r\n"))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	got := <-f.headers
	if resp.StatusCode != http.StatusOK || got.Get("X-Api-Key") != "sk-ant" || got.Get("Authorization") != "" || got.Get("Anthropic-Version") != "2023-06-01" {
		t.Fatalf("status=%d provider headers=%v", resp.StatusCode, got)
	}
}

func TestRelayRejectsTokenAuthenticatedIntegrations(t *testing.T) {
	f := startRelayFixture(t)
	resp, err := f.relayDo(t, f.assertion(t, workloadidentity.OutboundRelayAudience),
		relayPost("/i/legacy/v1/items", TokenHeader+": legacy-token\r\n"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusForbidden || !bytes.Contains(body, []byte("outbound_relay_not_supported")) {
		t.Fatalf("legacy via relay = %d %s", resp.StatusCode, body)
	}
}

func TestRelayClosesConnectionsWithoutAValidAssertion(t *testing.T) {
	f := startRelayFixture(t)
	for name, assertion := range map[string]string{
		"integration audience": f.assertion(t, identityAudiencePrefix+"openai"),
		"garbage":              "not-a-token",
	} {
		t.Run(name, func(t *testing.T) {
			resp, err := f.relayDo(t, assertion, relayPost("/i/openai/v1/chat/completions", ""))
			if err == nil {
				_ = resp.Body.Close()
				t.Fatalf("connection served a response: %d", resp.StatusCode)
			}
		})
	}
	select {
	case got := <-f.headers:
		t.Fatalf("provider was called: %v", got)
	default:
	}
}

func TestVerifyRefusesRelayAudienceForIntegrations(t *testing.T) {
	signer, jwks := testWorkloadSigner(t, workloadidentity.DefaultIssuer)
	verifier, err := NewWorkloadIdentityVerifier(jwks, workloadidentity.DefaultIssuer)
	if err != nil {
		t.Fatal(err)
	}
	relayToken, err := signer.Mint(time.Now(), "account-1", "app-1", "instance-1", workloadidentity.OutboundRelayAudience)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := verifier.Verify(relayToken.AccessToken, strings.TrimPrefix(workloadidentity.OutboundRelayAudience, identityAudiencePrefix)); err == nil {
		t.Fatal("relay assertion accepted as an integration assertion")
	}
	if identity, err := verifier.VerifyRelay(relayToken.AccessToken); err != nil || identity.AppID != "app-1" {
		t.Fatalf("VerifyRelay = %+v, %v", identity, err)
	}
	integrationToken := testWorkloadToken(t, signer, time.Now(), "app-1", "integration-1")
	if _, err := verifier.VerifyRelay(integrationToken); err == nil {
		t.Fatal("integration assertion accepted as a relay assertion")
	}
}

func TestRelayWirePreambleBounds(t *testing.T) {
	var buf bytes.Buffer
	if err := relaywire.Write(&buf, relaywire.Preamble{Assertion: "token"}); err != nil {
		t.Fatal(err)
	}
	buf.WriteString("GET / HTTP/1.1\r\n")
	got, err := relaywire.Read(&buf)
	if err != nil || got.Assertion != "token" || !strings.HasPrefix(buf.String(), "GET /") {
		t.Fatalf("round trip = %+v, %v, rest=%q", got, err, buf.String())
	}
	if err := relaywire.Write(io.Discard, relaywire.Preamble{}); err == nil {
		t.Fatal("empty assertion written")
	}
	oversized := []byte{0, 1, 0, 0}
	if _, err := relaywire.Read(bytes.NewReader(oversized)); err == nil {
		t.Fatal("oversized preamble length accepted")
	}
}
