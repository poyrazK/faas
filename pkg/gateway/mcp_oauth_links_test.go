package gateway

// adr: 20261124094745 — a signed-in MCP caller links its OAuth identity to
// a consumer key it holds; the gateway verifies the token, apid the key.

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apidgrpc"
)

type fakeOAuthLinker struct {
	calls                       int
	issuer, subject, email, key string
	id                          string
	err                         error
}

func (f *fakeOAuthLinker) LinkOAuthConsumer(_ context.Context, _, _, issuer, subject, email, key string) (string, error) {
	f.calls++
	f.issuer, f.subject, f.email, f.key = issuer, subject, email, key
	return f.id, f.err
}

func TestMCPOAuthConsumerLink(t *testing.T) {
	p := &api.MCPResourcePolicy{Resource: "https://app.example/mcp", Scopes: []string{"mcp:read"}, ToolScopes: map[string][]string{"read": {}}}
	rule := &EdgeRuleJWTResolved{Issuer: "https://issuer.example", MCP: p}
	app := App{ID: "app-1", AccountID: "acct-1"}
	handler := func(l *fakeOAuthLinker) (*Handler, *fakeOAuthEnroller) {
		h := &Handler{apiMonetization: monetizationOn, jwtVerifier: &countingJWTVerifier{onVerify: func(context.Context, string, *EdgeRuleJWTResolved) (*JWTClaims, error) {
			return &JWTClaims{Subject: "u1", Exp: time.Now().Add(time.Minute), Custom: map[string]string{"scope": "mcp:read", "email": "ada@example.com"}}, nil
		}}}
		enroller := &fakeOAuthEnroller{id: "enrolled", active: true}
		h.WithOAuthConsumers(enroller)
		if l != nil {
			h.WithOAuthConsumerLinks(l)
		}
		return h, enroller
	}
	send := func(h *Handler, method, path, body string) (*httptest.ResponseRecorder, *http.Request, bool) {
		r := httptest.NewRequest(method, "https://app.example"+path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer jwt")
		w := httptest.NewRecorder()
		return w, r, h.applyMCPResourcePolicy(w, r, app, rule)
	}
	linkPath := p.ConsumerLinkPath()
	if linkPath != "/.well-known/gregale-consumer-link/mcp" {
		t.Fatalf("link path = %q", linkPath)
	}

	linker := &fakeOAuthLinker{id: "keyed"}
	h, enroller := handler(linker)
	// The caller's first call enrolls it.
	if _, r, _ := send(h, "POST", "/mcp", `{"method":"tools/call","params":{"name":"read"}}`); authenticatedFrom(r.Context()).ConsumerID != "enrolled" {
		t.Fatal("first call was not attributed to the enrolled consumer")
	}
	w, _, handled := send(h, "POST", linkPath, `{"key":"ck_secret"}`)
	if !handled || w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"consumer_id":"keyed"`) {
		t.Fatalf("link: handled=%v status=%d body=%s", handled, w.Code, w.Body)
	}
	if linker.key != "ck_secret" || linker.issuer != "https://issuer.example" || linker.subject != "u1" || linker.email != "ada@example.com" {
		t.Fatalf("linker = %+v", linker)
	}
	// This gateway bills the next call to the key's consumer at once.
	if _, r, _ := send(h, "POST", "/mcp", `{"method":"tools/call","params":{"name":"read"}}`); authenticatedFrom(r.Context()).ConsumerID != "keyed" ||
		enroller.calls != 1 {
		t.Fatalf("after link: consumer=%q enroll calls=%d", authenticatedFrom(r.Context()).ConsumerID, enroller.calls)
	}

	if w, _, handled := send(h, "GET", linkPath, ""); !handled || w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET: handled=%v status=%d", handled, w.Code)
	}
	if w, _, _ := send(h, "POST", linkPath, `{}`); w.Code != http.StatusBadRequest {
		t.Fatalf("no key: status=%d", w.Code)
	}
	for name, tc := range map[string]struct {
		linker *fakeOAuthLinker
		status int
		code   string
	}{
		"off":          {&fakeOAuthLinker{err: apidgrpc.ErrOAuthConsumersOff}, http.StatusNotFound, api.CodeNotFound},
		"wrong key":    {&fakeOAuthLinker{err: apidgrpc.ErrOAuthLinkKeyRejected}, http.StatusForbidden, api.CodeConsumerLinkKeyRejected},
		"other key":    {&fakeOAuthLinker{err: apidgrpc.ErrOAuthLinkConflict}, http.StatusConflict, api.CodeConsumerLinkConflict},
		"apid down":    {&fakeOAuthLinker{err: errors.New("unavailable")}, http.StatusServiceUnavailable, api.CodeConsumerEnrollmentUnavailable},
		"not wired up": {nil, http.StatusNotFound, api.CodeNotFound},
	} {
		h, _ := handler(tc.linker)
		w, _, handled := send(h, "POST", linkPath, `{"key":"ck_secret"}`)
		if !handled || w.Code != tc.status || !strings.Contains(w.Body.String(), tc.code) {
			t.Errorf("%s: handled=%v status=%d body=%s", name, handled, w.Code, w.Body)
		}
	}

	// Without a valid token the link path is an MCP 401 like the endpoint.
	h, _ = handler(&fakeOAuthLinker{id: "keyed"})
	h.jwtVerifier = &countingJWTVerifier{onVerify: func(context.Context, string, *EdgeRuleJWTResolved) (*JWTClaims, error) {
		return nil, errors.New("bad token")
	}}
	if w, _, handled := send(h, "POST", linkPath, `{"key":"ck_secret"}`); !handled || w.Code != http.StatusUnauthorized {
		t.Fatalf("bad token: handled=%v status=%d", handled, w.Code)
	}
	// A consumer-key policy has no link path.
	keyRule := &EdgeRuleJWTResolved{MCP: &api.MCPResourcePolicy{Resource: "https://app.example/mcp", Scopes: []string{"mcp:read"},
		ToolScopes: map[string][]string{"read": {}}, Credential: api.MCPCredentialConsumerKey}}
	r := httptest.NewRequest("POST", "https://app.example"+linkPath, strings.NewReader(`{"key":"ck_secret"}`))
	if handler, _ := handler(&fakeOAuthLinker{}); handler.applyMCPResourcePolicy(httptest.NewRecorder(), r, app, keyRule) {
		t.Fatal("a consumer-key policy answered the link path")
	}
}
