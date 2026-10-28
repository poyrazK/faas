// adr: 20261026174727
package mcpmanagement

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
)

const (
	testEntryURL     = "https://gregale.dev/cli-auth?secret=sealed-ticket"
	testEntryTicket  = "private-ticket-material"
	testExchangeCode = "cccccccccccccccccccccccccccccccccccccccccc0"
)

type secretEntryAPI struct {
	mu       sync.Mutex
	set      bool
	gone     bool
	requests []api.SecretEntryRequest
	polls    []api.SecretEntryStatusRequest
}

func (f *secretEntryAPI) serveHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	switch r.URL.Path {
	case "/v1/apps/alpha/secret-requests":
		var body api.SecretEntryRequest
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.requests = append(f.requests, body)
		w.WriteHeader(201)
		_ = json.NewEncoder(w).Encode(api.SecretEntryResponse{EntryURL: testEntryURL, Ticket: testEntryTicket, ExchangeCode: testExchangeCode, ExpiresAt: time.Now().Add(15 * time.Minute).UTC().Format(time.RFC3339Nano)})
	case "/v1/apps/alpha/secret-requests/status":
		var body api.SecretEntryStatusRequest
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.polls = append(f.polls, body)
		if f.gone {
			api.WriteProblem(w, api.NewProblem(410, api.CodeSecretEntryUnavailable, "Secret entry unavailable", "expired"))
			return
		}
		status := api.SecretEntryPending
		if f.set {
			status = api.SecretEntrySet
		}
		_ = json.NewEncoder(w).Encode(api.SecretEntryStatus{Status: status, App: "alpha", Key: "STRIPE_KEY", Scope: "default"})
	default:
		w.WriteHeader(404)
	}
}

// connectElicitingClient connects a client that answers URL elicitation with action.
func connectElicitingClient(t *testing.T, f *secretEntryAPI, action, version string, asked *[]*mcp.ElicitParams) (*mcp.ClientSession, context.Context) {
	t.Helper()
	backend := httptest.NewServer(http.HandlerFunc(f.serveHTTP))
	t.Cleanup(backend.Close)
	s, err := NewServer(api.NewClient(backend.URL, testToken), Options{APIBase: backend.URL, Token: testToken, AllowWrites: true})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	ct, st := mcp.NewInMemoryTransports()
	ss, err := s.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ss.Close() })
	client := mcp.NewClient(&mcp.Implementation{Name: "eliciting", Version: "1"}, &mcp.ClientOptions{
		Capabilities: &mcp.ClientCapabilities{Elicitation: &mcp.ElicitationCapabilities{URL: &mcp.URLElicitationCapabilities{}}},
		ElicitationHandler: func(_ context.Context, req *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
			*asked = append(*asked, req.Params)
			return &mcp.ElicitResult{Action: action}, nil
		},
	})
	cs, err := client.Connect(ctx, ct, &mcp.ClientSessionOptions{ProtocolVersion: version})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs, ctx
}

func assertNoPrivateMaterial(t *testing.T, out map[string]json.RawMessage) {
	t.Helper()
	data, _ := json.Marshal(out)
	if strings.Contains(string(data), testEntryTicket) || strings.Contains(string(data), testExchangeCode) {
		t.Fatalf("tool result exposed private status material: %s", data)
	}
}

func stringField(t *testing.T, out map[string]json.RawMessage, key string) string {
	t.Helper()
	var value string
	if err := json.Unmarshal(out[key], &value); err != nil {
		t.Fatalf("%s missing from %v", key, out)
	}
	return value
}

// Modern clients retry the call with the user's answer (SEP-2322); for older
// protocol versions the SDK elicits and re-invokes the tool itself.
func TestRequestSecretOpensTheEntryPageAndReportsSet(t *testing.T) {
	for _, version := range []string{"2026-07-28", "2025-11-25"} {
		t.Run(version, func(t *testing.T) { testRequestSecretOpensTheEntryPage(t, version) })
	}
}

func testRequestSecretOpensTheEntryPage(t *testing.T, version string) {
	f := &secretEntryAPI{}
	var asked []*mcp.ElicitParams
	cs, ctx := connectElicitingClient(t, f, "accept", version, &asked)
	out := callTool(t, ctx, cs, "gregale_request_secret", map[string]any{"app": "alpha", "key": "STRIPE_KEY", "reason": "payments"}, false)
	assertNoPrivateMaterial(t, out)
	if stringField(t, out, "elicitation") != "opened" || stringField(t, out, "status") != "pending" || out["entry_url"] != nil {
		t.Fatalf("request = %v", out)
	}
	if len(asked) != 1 || asked[0].Mode != "url" || asked[0].URL != testEntryURL || asked[0].ElicitationID != stringField(t, out, "request_id") || !strings.Contains(asked[0].Message, "cannot read it through Gregale") {
		t.Fatalf("elicitation = %+v", asked)
	}
	if len(f.requests) != 1 || f.requests[0].Key != "STRIPE_KEY" || f.requests[0].Reason != "payments" {
		t.Fatalf("API request = %+v", f.requests)
	}
	id := stringField(t, out, "request_id")
	if status := callTool(t, ctx, cs, "gregale_get_secret_request", map[string]any{"request_id": id, "wait_seconds": 0}, false); stringField(t, status, "status") != "pending" {
		t.Fatalf("status = %v", status)
	}
	f.mu.Lock()
	f.set = true
	f.mu.Unlock()
	status := callTool(t, ctx, cs, "gregale_get_secret_request", map[string]any{"request_id": id, "wait_seconds": 0}, false)
	assertNoPrivateMaterial(t, status)
	if stringField(t, status, "status") != "set" {
		t.Fatalf("status = %v", status)
	}
	f.mu.Lock()
	f.gone = true // a set secret stays set after its request expires
	f.mu.Unlock()
	if again := callTool(t, ctx, cs, "gregale_get_secret_request", map[string]any{"request_id": id, "wait_seconds": 0}, false); stringField(t, again, "status") != "set" {
		t.Fatalf("status after expiry = %v", again)
	}
	if f.polls[0].Ticket != testEntryTicket || f.polls[0].ExchangeCode != testExchangeCode {
		t.Fatalf("status call did not carry the private material: %+v", f.polls[0])
	}
}

func TestRequestSecretGivesTheLinkWhenTheClientCannotOpenIt(t *testing.T) {
	f := &secretEntryAPI{}
	backend := httptest.NewServer(http.HandlerFunc(f.serveHTTP))
	t.Cleanup(backend.Close)
	readOnly, ctx := connectConfiguredTestServer(t, backend, Options{APIBase: backend.URL, Token: testToken}, "2026-07-28")
	if listedTools(t, ctx, readOnly)["gregale_request_secret"] {
		t.Fatal("a read-only connection lists gregale_request_secret")
	}
	cs, ctx := connectConfiguredTestServer(t, backend, Options{APIBase: backend.URL, Token: testToken, AllowWrites: true}, "2026-07-28")
	out := callTool(t, ctx, cs, "gregale_request_secret", map[string]any{"app": "alpha", "key": "STRIPE_KEY", "reason": "payments"}, false)
	assertNoPrivateMaterial(t, out)
	if stringField(t, out, "elicitation") != "unsupported" || stringField(t, out, "entry_url") != testEntryURL {
		t.Fatalf("fallback = %v", out)
	}
}

func TestRequestSecretDeclinedAndExpired(t *testing.T) {
	f := &secretEntryAPI{}
	var asked []*mcp.ElicitParams
	cs, ctx := connectElicitingClient(t, f, "decline", "2026-07-28", &asked)
	out := callTool(t, ctx, cs, "gregale_request_secret", map[string]any{"app": "alpha", "key": "STRIPE_KEY", "reason": "payments"}, false)
	if stringField(t, out, "elicitation") != "decline" || stringField(t, out, "entry_url") != testEntryURL {
		t.Fatalf("declined = %v", out)
	}
	f.mu.Lock()
	f.gone = true
	f.mu.Unlock()
	if status := callTool(t, ctx, cs, "gregale_get_secret_request", map[string]any{"request_id": stringField(t, out, "request_id"), "wait_seconds": 0}, false); stringField(t, status, "status") != "expired" {
		t.Fatalf("expired = %v", status)
	}
	if unknown := callTool(t, ctx, cs, "gregale_get_secret_request", map[string]any{"request_id": "0123", "wait_seconds": 0}, true); toolCode(t, unknown) != "unknown_request" {
		t.Fatalf("unknown = %v", unknown)
	}
}

func TestRequestSecretRejectsBadInput(t *testing.T) {
	f := &secretEntryAPI{}
	var asked []*mcp.ElicitParams
	cs, ctx := connectElicitingClient(t, f, "accept", "2026-07-28", &asked)
	for _, args := range []map[string]any{
		{"app": "alpha", "key": "", "reason": "x"},
		{"app": "alpha", "key": "STRIPE_KEY", "reason": ""},
		{"app": "alpha", "key": "STRIPE_KEY", "reason": strings.Repeat("r", 281)},
		{"app": "../alpha", "key": "STRIPE_KEY", "reason": "x"},
	} {
		if out := callTool(t, ctx, cs, "gregale_request_secret", args, true); toolCode(t, out) != "invalid_argument" {
			t.Errorf("%v accepted: %v", args, out)
		}
	}
	if len(f.requests) != 0 || len(asked) != 0 {
		t.Fatal("invalid input reached the API or the user")
	}
	if out := callTool(t, ctx, cs, "gregale_get_secret_request", map[string]any{"request_id": "x", "wait_seconds": 46}, true); toolCode(t, out) != "invalid_argument" {
		t.Fatalf("wait bound = %v", out)
	}
}
