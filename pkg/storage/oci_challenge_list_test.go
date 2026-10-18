// adr: 054
package storage

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOCIStorageSelectsBearerChallenge(t *testing.T) {
	for _, tc := range []struct {
		name   string
		fields []string
	}{
		{"separate fields", []string{`Basic realm="Registry login"`, "{bearer}"}},
		{"combined field", []string{`Basic realm="Registry login", {bearer}`}},
		{"later scheme parameters", []string{`{bearer}, Basic realm="Registry login", scope="unrelated"`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var server *httptest.Server
			server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if req.URL.Path == "/token" {
					user, password, ok := req.BasicAuth()
					if !ok || user != "user" || password != "password" {
						t.Error("token exchange omitted registry credentials")
					}
					if req.URL.Query().Get("service") != "Registry" || req.URL.Query().Get("scope") != "repository:faas/apps:pull" {
						t.Errorf("token exchange mixed authentication challenges: %v", req.URL.Query())
						http.Error(w, "wrong challenge parameters", http.StatusBadRequest)
						return
					}
					_, _ = io.WriteString(w, `{"token":"registry-token"}`)
					return
				}
				if req.Header.Get("Authorization") != "Bearer registry-token" {
					bearer := fmt.Sprintf(`Bearer realm=%q, service="Registry", scope="repository:faas/apps:pull"`, server.URL+"/token")
					for _, field := range tc.fields {
						w.Header().Add("WWW-Authenticate", strings.ReplaceAll(field, "{bearer}", bearer))
					}
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				_, _ = io.WriteString(w, `{"tags":[]}`)
			}))
			t.Cleanup(server.Close)
			backend, err := NewOCIRegistryStorageBackend(WithRegistry(server.URL), WithHTTPClient(server.Client()), WithCredentials("user", "password"))
			if err != nil {
				t.Fatal(err)
			}
			response, err := backend.doBearerRequest(t.Context(), bearerReqOpts{
				Method: http.MethodGet, URL: server.URL + "/v2/faas/apps/tags/list", Scope: "repository:faas/apps:pull",
			})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = response.Body.Close() }()
			if response.StatusCode != http.StatusOK {
				t.Errorf("authenticated response status = %d, want 200", response.StatusCode)
			}
		})
	}
}
