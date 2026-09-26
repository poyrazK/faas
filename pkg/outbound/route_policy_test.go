package outbound

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func managedRouteIntegration(t *testing.T, origin string) Integration {
	t.Helper()
	i, err := NewIntegration("integration-1", origin, "unused-gateway-token", []string{"app-1"}, 100, 1, 1, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	i.ProviderAuthMode = ProviderAuthManaged
	i.AllowedMethods = []string{http.MethodGet}
	i.AllowedPathPrefixes = []string{"/v1/customers"}
	return i
}

func TestManagedRoutePolicyRequiresExplicitCanonicalPermissions(t *testing.T) {
	good := managedRouteIntegration(t, "https://api.example.test")
	if err := good.Validate(); err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(*Integration){
		"missing methods":  func(i *Integration) { i.AllowedMethods = nil },
		"missing paths":    func(i *Integration) { i.AllowedPathPrefixes = nil },
		"unknown method":   func(i *Integration) { i.AllowedMethods = []string{"TRACE"} },
		"lowercase method": func(i *Integration) { i.AllowedMethods = []string{"get"} },
		"duplicate method": func(i *Integration) { i.AllowedMethods = []string{"GET", "GET"} },
		"encoded prefix":   func(i *Integration) { i.AllowedPathPrefixes = []string{"/v1/%63ustomers"} },
		"dot segment":      func(i *Integration) { i.AllowedPathPrefixes = []string{"/v1/../customers"} },
		"double slash":     func(i *Integration) { i.AllowedPathPrefixes = []string{"/v1//customers"} },
		"trailing slash":   func(i *Integration) { i.AllowedPathPrefixes = []string{"/v1/customers/"} },
		"duplicate prefix": func(i *Integration) { i.AllowedPathPrefixes = []string{"/v1/customers", "/v1/customers"} },
		"ambiguous origin": func(i *Integration) {
			i.Origin.RawPath = "/%61pi"
			i.Origin.Path = "/api"
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			i := good
			mutate(&i)
			if err := i.Validate(); err == nil {
				t.Fatal("accepted unsafe managed route policy")
			}
		})
	}
	legacy := good
	legacy.ProviderAuthMode = ProviderAuthApplication
	if err := legacy.Validate(); err == nil {
		t.Fatal("silently ignored route policy on legacy integration")
	}
}

func TestManagedRoutePolicyRejectsBeforeAdmissionOrProviderCall(t *testing.T) {
	providerCalls := 0
	provider := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		providerCalls++
		if r.Header.Get("Authorization") != "Bearer provider-secret" {
			t.Errorf("provider authorization = %q", r.Header.Get("Authorization"))
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer provider.Close()
	integration := managedRouteIntegration(t, provider.URL)
	resolver, err := NewStaticResolver([]Integration{integration})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandler(resolver, NewMemoryBackend(), provider.Client())
	if err != nil {
		t.Fatal(err)
	}
	if err := handler.SetManagedAuthorizations(map[string]string{integration.ID: "Bearer provider-secret"}); err != nil {
		t.Fatal(err)
	}
	signer, jwks := testWorkloadSigner(t, "https://identity.gregale.dev")
	handler.IdentityVerifier, err = NewWorkloadIdentityVerifier(jwks, "https://identity.gregale.dev")
	if err != nil {
		t.Fatal(err)
	}
	token := testWorkloadToken(t, signer, time.Now(), "app-1", integration.ID)
	requests := []struct {
		method, path string
	}{
		{http.MethodPost, "/v1/customers"},
		{http.MethodGet, "/v1/customers-extra"},
		{http.MethodGet, "/v1/%63ustomers"},
		{http.MethodGet, "/v1/customers/%2e%2e/charges"},
		{http.MethodGet, "/v1/customers//charges"},
		{http.MethodGet, "/v1/customers;role=admin"},
		{http.MethodGet, "/v1/customers\\admin"},
	}
	for _, tc := range requests {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, "http://gateway.test/i/"+integration.ID+tc.path, nil)
			req.Header.Set(WorkloadIdentityHeader, token)
			resp := httptest.NewRecorder()
			handler.ServeHTTP(resp, req)
			if resp.Code != http.StatusForbidden || providerCalls != 0 {
				t.Fatalf("denied request status=%d provider calls=%d", resp.Code, providerCalls)
			}
		})
	}
	override := httptest.NewRequest(http.MethodGet, "http://gateway.test/i/"+integration.ID+"/v1/customers", nil)
	override.Header.Set(WorkloadIdentityHeader, token)
	override.Header.Set("X-HTTP-Method-Override", http.MethodDelete)
	overrideResponse := httptest.NewRecorder()
	handler.ServeHTTP(overrideResponse, override)
	if overrideResponse.Code != http.StatusForbidden || providerCalls != 0 {
		t.Fatalf("method override status=%d provider calls=%d", overrideResponse.Code, providerCalls)
	}
	queryOverride := httptest.NewRequest(http.MethodGet, "http://gateway.test/i/"+integration.ID+"/v1/customers?_method=DELETE", nil)
	queryOverride.Header.Set(WorkloadIdentityHeader, token)
	queryResponse := httptest.NewRecorder()
	handler.ServeHTTP(queryResponse, queryOverride)
	if queryResponse.Code != http.StatusForbidden || providerCalls != 0 {
		t.Fatalf("query override status=%d provider calls=%d", queryResponse.Code, providerCalls)
	}
	// A one-token burst proves the rejected calls did not consume admission.
	req := httptest.NewRequest(http.MethodGet, "http://gateway.test/i/"+integration.ID+"/v1/customers/cus_123?expand=balance", nil)
	req.Header.Set(WorkloadIdentityHeader, token)
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)
	if resp.Code != http.StatusNoContent || providerCalls != 1 {
		t.Fatalf("allowed request status=%d provider calls=%d", resp.Code, providerCalls)
	}
}

func TestStaticResolverCopiesRoutePermissions(t *testing.T) {
	i := managedRouteIntegration(t, "https://api.example.test")
	resolver, err := NewStaticResolver([]Integration{i})
	if err != nil {
		t.Fatal(err)
	}
	i.AllowedMethods[0] = http.MethodPost
	i.AllowedPathPrefixes[0] = "/admin"
	resolved, err := resolver.Integration(t.Context(), i.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !resolved.AllowsRequest(http.MethodGet, "/v1/customers") || resolved.AllowsRequest(http.MethodPost, "/admin") {
		t.Fatal("input slices changed stored route policy")
	}
	resolved.AllowedMethods[0] = http.MethodDelete
	resolved.AllowedPathPrefixes[0] = "/delete"
	again, err := resolver.Integration(t.Context(), i.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !again.AllowsRequest(http.MethodGet, "/v1/customers") || again.AllowsRequest(http.MethodDelete, "/delete") {
		t.Fatal("returned slices changed stored route policy")
	}
}

func TestBindingRoutePolicyIsNarrowerThanOperatorCeiling(t *testing.T) {
	ceiling := RoutePolicy{AllowedMethods: []string{"GET", "POST"}, AllowedPathPrefixes: []string{"/v1"}}
	good := RoutePolicy{AllowedMethods: []string{"GET"}, AllowedPathPrefixes: []string{"/v1/customers"}}
	if err := ValidateBindingRoutePolicy(ceiling, good); err != nil {
		t.Fatal(err)
	}
	for name, policy := range map[string]RoutePolicy{
		"wider method":   {AllowedMethods: []string{"DELETE"}, AllowedPathPrefixes: []string{"/v1"}},
		"wider path":     {AllowedMethods: []string{"GET"}, AllowedPathPrefixes: []string{"/admin"}},
		"sibling path":   {AllowedMethods: []string{"GET"}, AllowedPathPrefixes: []string{"/v10"}},
		"encoded path":   {AllowedMethods: []string{"GET"}, AllowedPathPrefixes: []string{"/v1/%63ustomers"}},
		"empty methods":  {AllowedPathPrefixes: []string{"/v1"}},
		"duplicate path": {AllowedMethods: []string{"GET"}, AllowedPathPrefixes: []string{"/v1", "/v1"}},
	} {
		t.Run(name, func(t *testing.T) {
			if err := ValidateBindingRoutePolicy(ceiling, policy); err == nil {
				t.Fatal("accepted invalid customer route policy")
			}
		})
	}
}

func TestAppRoutePolicyIntersectsCeilingAndCustomerNarrowing(t *testing.T) {
	i := managedRouteIntegration(t, "https://api.example.test")
	i.AppIDs = map[string]struct{}{"customer": {}, "operator": {}}
	i.OperatorAppIDs = map[string]struct{}{"operator": {}}
	i.CustomerAppRoutes = map[string]RoutePolicy{
		"customer": {AllowedMethods: []string{"GET"}, AllowedPathPrefixes: []string{"/v1/customers/safe"}},
		"operator": {AllowedMethods: []string{"GET"}, AllowedPathPrefixes: []string{"/v1/customers/safe"}},
	}
	if !i.AllowsAppRequest("customer", "GET", "/v1/customers/safe/123") ||
		i.AllowsAppRequest("customer", "GET", "/v1/customers/unsafe") ||
		!i.AllowsAppRequest("operator", "GET", "/v1/customers/unsafe") ||
		i.AllowsAppRequest("operator", "GET", "/admin") ||
		i.AllowsAppRequest("unbound", "GET", "/v1/customers/safe") {
		t.Fatal("operator ceiling or customer route intersection incorrect")
	}
}
