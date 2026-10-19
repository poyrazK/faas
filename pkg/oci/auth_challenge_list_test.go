// adr: 062
package oci

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRegistryPullSelectsBearerChallenge(t *testing.T) {
	for _, tc := range []struct {
		name   string
		fields []string
	}{
		{"separate fields", []string{`Basic realm="Registry login"`, "{bearer}"}},
		{"combined field", []string{`Basic realm="Registry login", {bearer}`}},
		{"bare scheme", []string{`Negotiate, {bearer}`}},
		{"token68 scheme", []string{`Negotiate dG9rZW4=, {bearer}`}},
		{"later scheme parameters", []string{`{bearer}, Basic realm="Registry login", scope="unrelated"`}},
		{"quoted decoy", []string{`Basic realm="use Bearer, service=decoy", {bearer}`}},
		{"escaped quoted decoy", []string{`Basic realm="text \", Bearer realm=\"decoy\", service=\"decoy\"", {bearer}`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				user, password, ok := req.BasicAuth()
				if !ok || user != "user" || password != "password" {
					t.Error("token exchange omitted registry credentials")
				}
				if req.URL.Query().Get("service") != "Registry" || req.URL.Query().Get("scope") != "repository:Org/App:pull,push" {
					t.Errorf("token exchange mixed authentication challenges: %v", req.URL.Query())
					http.Error(w, "wrong challenge parameters", http.StatusBadRequest)
					return
				}
				_, _ = io.WriteString(w, `{"token":"registry-token"}`)
			}))
			t.Cleanup(tokenServer.Close)
			const digest = "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
			registry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if req.Header.Get("Authorization") != "Bearer registry-token" {
					bearer := fmt.Sprintf(`Bearer realm=%q, service="Registry", scope="repository:Org/App:pull,push"`, tokenServer.URL)
					for _, field := range tc.fields {
						w.Header().Add("WWW-Authenticate", strings.ReplaceAll(field, "{bearer}", bearer))
					}
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				w.Header().Set("Docker-Content-Digest", digest)
				w.Header().Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
				_, _ = io.WriteString(w, `{"schemaVersion":2}`)
			}))
			t.Cleanup(registry.Close)
			client := NewRegistryClient(WithEndpoint("http", strings.TrimPrefix(registry.URL, "http://")))
			auth := &BasicAuth{Username: "user", Password: "password"}
			got, err := client.PullDigestWithAuth(t.Context(), "example.com/org/app:latest", auth)
			if err != nil || got != digest {
				t.Errorf("pull with multiple challenges = %q, %v; want %s", got, err, digest)
			}
			if _, _, err := client.fetchManifestJSONWithAuth(t.Context(), registry.URL+"/v2/org/app/manifests/latest", auth); err != nil {
				t.Errorf("manifest with multiple challenges: %v", err)
			}
			ref, err := ParseReference("example.com/org/app:latest")
			if err != nil {
				t.Fatal(err)
			}
			_, body, err := client.openBlobNetworkWithAuth(t.Context(), ref, digest, auth)
			if err != nil {
				t.Errorf("blob with multiple challenges: %v", err)
			} else {
				_, _ = io.Copy(io.Discard, body)
				_ = body.Close()
			}
		})
	}
}
