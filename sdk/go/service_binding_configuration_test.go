package faas

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestServiceBindingConfigurationWire(t *testing.T) {
	scopes := ServiceCallerScopes{"frontend": {Methods: []string{"GET"}, PathPrefixes: []string{"/api"}}}
	scopeJSON, err := json.Marshal(scopes)
	if err != nil {
		t.Fatal(err)
	}
	callers := []string{"frontend"}
	transport := ServiceBindingTransportHTTPS
	fingerprint := strings.Repeat("a", 64)
	for _, tc := range []struct{ name, callers, scopes string }{
		{"omitted", "", ""},
		{"unrestricted", "null", "null"},
		{"deny all", "[]", "{}"},
		{"granted", `["frontend"]`, string(scopeJSON)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.Path != "/v1/apps/demo" || r.Method != http.MethodPatch {
					t.Errorf("request=%s %s", r.Method, r.URL.Path)
				}
				var body map[string]json.RawMessage
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				for key, want := range map[string]string{"allowed_service_callers": tc.callers, "allowed_service_call_scopes": tc.scopes, "service_binding_transport": `"https"`, "expected_service_binding_fingerprint": `"` + fingerprint + `"`} {
					if string(body[key]) != want {
						t.Errorf("%s=%s want %s", key, body[key], want)
					}
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"allowed_service_callers":["frontend"],"allowed_service_call_scopes":{"frontend":{"methods":["GET"],"path_prefixes":["/api"]}},"service_binding_transport":"https"}`))
			}))
			defer server.Close()
			client, err := NewClient(server.URL, "fixture")
			if err != nil {
				t.Fatal(err)
			}
			app, err := client.UpdateApp(context.Background(), "demo", UpdateAppRequest{
				AllowedServiceCallers: json.RawMessage(tc.callers), AllowedServiceCallScopes: json.RawMessage(tc.scopes), ServiceBindingTransport: &transport, ExpectedServiceBindingFingerprint: &fingerprint,
			})
			if err != nil {
				t.Fatal(err)
			}
			if app.AllowedServiceCallers == nil || len(*app.AllowedServiceCallers) != 1 || app.AllowedServiceCallScopes == nil || (*app.AllowedServiceCallScopes)["frontend"].PathPrefixes[0] != "/api" || app.ServiceBindingTransport != transport {
				t.Fatalf("response=%+v", app)
			}
			if calls != 1 {
				t.Fatalf("calls=%d", calls)
			}
		})
	}
	t.Run("create", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost || r.URL.Path != "/v1/apps" {
				t.Errorf("request=%s %s", r.Method, r.URL.Path)
			}
			var body map[string]json.RawMessage
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			for key, want := range map[string]string{"allowed_service_callers": `["frontend"]`, "allowed_service_call_scopes": string(scopeJSON), "service_binding_transport": `"https"`} {
				if string(body[key]) != want {
					t.Errorf("%s=%s want %s", key, body[key], want)
				}
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{}`))
		}))
		defer server.Close()
		client, err := NewClient(server.URL, "fixture")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = client.CreateApp(context.Background(), CreateAppRequest{Slug: "demo", AllowedServiceCallers: &callers, AllowedServiceCallScopes: &scopes, ServiceBindingTransport: &transport}); err != nil {
			t.Fatal(err)
		}
	})
}

func TestServiceBindingConfigurationReadPolicyStates(t *testing.T) {
	for _, tc := range []struct {
		name, fields string
		restricted   bool
	}{
		{"omitted", ``, false},
		{"null", `"allowed_service_callers":null,"allowed_service_call_scopes":null`, false},
		{"empty", `"allowed_service_callers":[],"allowed_service_call_scopes":{}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/v1/apps/demo" {
					t.Errorf("request=%s %s", r.Method, r.URL.Path)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{` + tc.fields + `}`))
			}))
			defer server.Close()
			client, err := NewClient(server.URL, "fixture")
			if err != nil {
				t.Fatal(err)
			}
			app, err := client.GetApp(context.Background(), "demo")
			if err != nil {
				t.Fatal(err)
			}
			if (app.AllowedServiceCallers != nil) != tc.restricted || (app.AllowedServiceCallScopes != nil) != tc.restricted {
				t.Fatalf("response=%+v", app)
			}
		})
	}
}
