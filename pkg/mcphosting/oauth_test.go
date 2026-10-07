package mcphosting

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOAuthDiscoveryDoesNotForwardTokens(t *testing.T) {
	var endpoint string
	wrongResource := false
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" && (r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer gregale-invalid-bearer-probe") {
			t.Error("token sent to public metadata probe")
		}
		if r.Method == http.MethodPost {
			w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer resource_metadata="%s/.well-known/oauth-protected-resource/mcp"`, endpoint))
			w.WriteHeader(401)
			return
		}
		resource := endpoint + "/mcp"
		if wrongResource {
			resource = "https://other.example/mcp"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"resource": resource, "authorization_servers": []string{"https://issuer.example"}, "scopes_supported": []string{"mcp:tools"}})
	}))
	defer s.Close()
	endpoint = s.URL
	c, err := NewClient(endpoint+"/mcp", "client-secret", ProtocolVersion)
	if err != nil {
		t.Fatal(err)
	}
	c.ExpectedAuth = &AuthConfig{Issuer: "https://issuer.example", Scopes: []string{"mcp:tools"}}
	if err := c.VerifyOAuth(context.Background()); err != nil {
		t.Fatal(err)
	}
	wrongResource = true
	if err := c.VerifyOAuth(context.Background()); err == nil {
		t.Fatal("accepted metadata for another resource")
	}
}

func TestOAuthDiscoveryRejectsServerAcceptingMalformedBearer(t *testing.T) {
	for _, rejects := range []bool{false, true} {
		t.Run(fmt.Sprint(rejects), func(t *testing.T) {
			var endpoint string
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					if r.Header.Get("Authorization") != "" {
						t.Error("credential reached metadata")
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"resource": endpoint + "/mcp", "authorization_servers": []string{"https://issuer.example"}})
					return
				}
				if r.Header.Get("Authorization") == "" || rejects {
					w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer resource_metadata="%s/.well-known/oauth-protected-resource/mcp"`, endpoint))
					w.WriteHeader(401)
					return
				}
				w.WriteHeader(200)
			}))
			defer s.Close()
			endpoint = s.URL
			c, _ := NewClient(endpoint+"/mcp", "real-client-token", ProtocolVersion)
			if err := c.VerifyOAuth(context.Background()); (err == nil) != rejects {
				t.Fatalf("rejects=%v error=%v", rejects, err)
			}
		})
	}
}

func TestOAuthCandidateKeepsCanonicalAudience(t *testing.T) {
	const canonical = "https://production.example/mcp"
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="https://production.example/.well-known/oauth-protected-resource/mcp"`)
			w.WriteHeader(401)
			return
		}
		if r.URL.Path != "/.well-known/oauth-protected-resource/mcp" || r.Header.Get("Authorization") != "" {
			t.Errorf("unsafe metadata request: %s", r.URL)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"resource": canonical, "authorization_servers": []string{"https://issuer.example"}})
	}))
	defer s.Close()
	c, _ := NewClient(s.URL+"/mcp", "candidate-client-token", ProtocolVersion)
	c.ExpectedAuth = &AuthConfig{Resource: canonical, Issuer: "https://issuer.example"}
	if err := c.VerifyOAuth(context.Background()); err != nil {
		t.Fatal(err)
	}
}
