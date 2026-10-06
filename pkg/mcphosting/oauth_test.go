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
		if r.Header.Get("Authorization") != "" {
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
