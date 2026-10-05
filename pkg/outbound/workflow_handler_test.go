package outbound

import (
	"context"
	"github.com/google/uuid"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type workflowAuthorizerStub struct {
	identity WorkflowIdentity
	err      error
	calls    int
}

func TestWorkflowGatewayDoesNotReplayDroppedProviderConnection(t *testing.T) {
	var calls atomic.Int32
	provider := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor != 1 {
			t.Error("workflow provider connection allows implicit HTTP/2 replays")
		}
		if calls.Add(1) == 2 {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			_ = conn.Close()
			return
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	provider.EnableHTTP2 = true
	provider.StartTLS()
	defer provider.Close()
	appID := uuid.NewString()
	integration, err := NewIntegration(uuid.NewString(), provider.URL, "unused", []string{appID}, 100, 10, 10, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	integration.AccountID = uuid.NewString()
	integration.OwnerKind = IntegrationOwnerCustomer
	integration.ProviderAuthMode = ProviderAuthManaged
	integration.CredentialSource = CredentialSourceCustomerSealed
	integration.AllowedMethods = []string{"POST"}
	integration.AllowedPathPrefixes = []string{"/v1"}
	integration.BindingAppIDs = map[string]struct{}{appID: {}}
	resolver, err := NewStaticResolver([]Integration{integration})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandler(resolver, NewMemoryBackend(), provider.Client())
	if err != nil {
		t.Fatal(err)
	}
	handler.WorkflowAuthorizer = &workflowAuthorizerStub{identity: WorkflowIdentity{AccountID: integration.AccountID, AppID: appID, RunID: uuid.NewString(), StepName: "send", Attempt: 1}}
	handler.CredentialResolver = &changingCredential{value: "Bearer provider-secret"}
	call := func() int {
		r := httptest.NewRequest("POST", Prefix+integration.ID+"/v1/contacts", nil)
		r.Header.Set(WorkflowIdentityHeader, "private-assertion")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w.Code
	}
	if status := call(); status != 200 {
		t.Fatalf("warmup failed: %d", status)
	}
	if status := call(); status != 502 || calls.Load() != 2 {
		t.Fatalf("dropped connection was replayed: status=%d provider_calls=%d", status, calls.Load())
	}
}

func (a *workflowAuthorizerStub) AuthorizeWorkflow(_ context.Context, _, _, _, _ string, _ []byte) (WorkflowIdentity, error) {
	a.calls++
	return a.identity, a.err
}
func TestWorkflowGatewayUsesBindingsAndSingleProviderAttempt(t *testing.T) {
	calls := 0
	status := 200
	responseBody := `{"created":true}`
	provider := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get(WorkflowIdentityHeader) != "" || r.Header.Get("Authorization") != "Bearer provider-secret" {
			t.Error("host identity or provider credential boundary failed")
		}
		if r.Header.Get("Idempotency-Key") == "spoofed" {
			t.Error("trusted caller key was not derived from the attempt")
		}
		w.Header().Set("Set-Cookie", "secret-cookie")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(responseBody))
	}))
	defer provider.Close()
	appID := uuid.NewString()
	integration, err := NewIntegration(uuid.NewString(), provider.URL, "unused", []string{appID}, 100, 10, 10, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	integration.AccountID = uuid.NewString()
	integration.OwnerKind = IntegrationOwnerCustomer
	integration.ProviderAuthMode = ProviderAuthManaged
	integration.CredentialSource = CredentialSourceCustomerSealed
	integration.AllowedMethods = []string{"POST"}
	integration.AllowedPathPrefixes = []string{"/v1"}
	integration.CustomerAppRoutes = map[string]RoutePolicy{appID: {AllowedMethods: []string{"POST"}, AllowedPathPrefixes: []string{"/v1/contacts"}}}
	integration.OperatorAppIDs = nil
	integration.BindingAppIDs = map[string]struct{}{appID: {}}
	integration.MaxRetries = 2
	resolver, err := NewStaticResolver([]Integration{integration})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandler(resolver, NewMemoryBackend(), provider.Client())
	if err != nil {
		t.Fatal(err)
	}
	authorizer := &workflowAuthorizerStub{identity: WorkflowIdentity{AccountID: integration.AccountID, AppID: appID, RunID: uuid.NewString(), StepName: "crm", Attempt: 1}}
	handler.WorkflowAuthorizer = authorizer
	handler.CredentialResolver = &changingCredential{value: "Bearer provider-secret"}
	call := func(path string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", Prefix+integration.ID+path, strings.NewReader(`{}`))
		r.Header.Set(WorkflowIdentityHeader, "private-assertion")
		r.Header.Set("Idempotency-Key", "spoofed")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	if w := call("/v1/contacts"); w.Code != 200 || w.Header().Get("Set-Cookie") != "" || calls != 1 {
		t.Fatalf("success=%d calls=%d body=%s", w.Code, calls, w.Body.String())
	}
	if w := call("/v1/admin"); w.Code != 403 || calls != 1 {
		t.Fatalf("binding bypass status=%d calls=%d", w.Code, calls)
	}
	status = 503
	if w := call("/v1/contacts"); w.Code != 503 || calls != 2 {
		t.Fatalf("gateway stacked retries status=%d calls=%d", w.Code, calls)
	}
	authorizer.err = ErrWorkflowNotAuthorized
	if w := call("/v1/contacts"); w.Code != 403 || calls != 2 {
		t.Fatalf("revoked request=%d calls=%d", w.Code, calls)
	}
	authorizer.err = nil
	status = 200
	responseBody = `{"authorization":"Bearer provider-secret"}`
	if w := call("/v1/contacts"); w.Code != 502 || strings.Contains(w.Body.String(), "provider-secret") {
		t.Fatalf("reflected credential leaked: %d %s", w.Code, w.Body.String())
	}
	if w := call("/v1/contacts?q=1"); w.Code != 403 {
		t.Fatalf("unbound query accepted: %d", w.Code)
	}
}
