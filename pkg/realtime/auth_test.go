package realtime

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

type recordingJWTAuthorizer struct {
	token  string
	policy AuthPolicy
}

func (a *recordingJWTAuthorizer) Authorize(_ context.Context, token string, policy AuthPolicy) (string, error) {
	a.token, a.policy = token, policy
	return "user-123", nil
}

func TestNormalizeAuthPolicyKeepsLegacyStaticToken(t *testing.T) {
	policy, err := normalizeAuthPolicy(AuthPolicy{}, true)
	if err != nil {
		t.Fatal(err)
	}
	if policy.Mode != AuthModeStaticBearer {
		t.Fatalf("mode = %q, want %q", policy.Mode, AuthModeStaticBearer)
	}
}

func TestNormalizeAuthPolicyRejectsIncompleteJWT(t *testing.T) {
	_, err := normalizeAuthPolicy(AuthPolicy{Mode: AuthModeOIDCJWT}, false)
	if err == nil {
		t.Fatal("incomplete JWT policy unexpectedly accepted")
	}
}

func TestNormalizeAuthPolicyJWTClonesClaims(t *testing.T) {
	claims := map[string]string{"tenant": "acme"}
	policy, err := normalizeAuthPolicy(AuthPolicy{
		Mode:           AuthModeOIDCJWT,
		Issuer:         "https://issuer.example",
		JWKSURL:        "https://issuer.example/.well-known/jwks.json",
		Algorithms:     []string{"RS256"},
		RequiredClaims: claims,
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	claims["tenant"] = "mutated"
	if policy.RequiredClaims["tenant"] != "acme" {
		t.Fatalf("claims were not cloned: %v", policy.RequiredClaims)
	}
}

func TestManagerJWTAuthorizationUsesBearerAndPrincipal(t *testing.T) {
	authorizer := &recordingJWTAuthorizer{}
	m := NewManager(Config{JWTAuthorizer: authorizer}, nil)
	defer m.Close()
	err := m.RegisterEndpoint(Endpoint{
		ID: "private",
		ClientAuth: AuthPolicy{
			Mode:       AuthModeOIDCJWT,
			Issuer:     "https://issuer.example",
			JWKSURL:    "https://issuer.example/.well-known/jwks.json",
			Algorithms: []string{"RS256"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(m.Handler())
	defer server.Close()
	url := "ws" + server.URL[len("http"):]
	client, response, err := websocket.DefaultDialer.Dial(url+ManagedPathPrefix+"private", map[string][]string{
		"Authorization": {"Bearer jwt-token"},
	})
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	deadline := time.Now().Add(time.Second)
	for len(m.Snapshot()) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := len(m.Snapshot()); got != 1 || m.Snapshot()[0].Principal != "user-123" {
		t.Fatalf("snapshot = %+v, want one connection for user-123", m.Snapshot())
	}
	if authorizer.token != "jwt-token" {
		t.Fatalf("token = %q, want jwt-token", authorizer.token)
	}
}

func TestManagerAcceptsPreviousStaticBearerDuringGraceWindow(t *testing.T) {
	m := NewManager(Config{}, nil)
	defer m.Close()
	if err := m.RegisterEndpoint(Endpoint{
		ID:                         "rotating",
		AuthToken:                  "new-token",
		AuthTokenPrevious:          "old-token",
		AuthTokenPreviousExpiresAt: time.Now().Add(time.Minute),
		ClientAuth:                 AuthPolicy{Mode: AuthModeStaticBearer},
	}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(m.Handler())
	defer server.Close()
	url := "ws" + server.URL[len("http"):]

	for _, token := range []string{"new-token", "old-token"} {
		client, response, err := websocket.DefaultDialer.Dial(url+ManagedPathPrefix+"rotating", map[string][]string{
			"Authorization": {"Bearer " + token},
		})
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		if err != nil {
			t.Fatalf("dial with %s: %v", token, err)
		}
		_ = client.Close()
	}

	if err := m.RegisterEndpoint(Endpoint{
		ID:                         "expired",
		AuthToken:                  "new-token",
		AuthTokenPrevious:          "old-token",
		AuthTokenPreviousExpiresAt: time.Now().Add(-time.Second),
		ClientAuth:                 AuthPolicy{Mode: AuthModeStaticBearer},
	}); err != nil {
		t.Fatal(err)
	}
	_, response, err := websocket.DefaultDialer.Dial(url+ManagedPathPrefix+"expired", map[string][]string{
		"Authorization": {"Bearer old-token"},
	})
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
	if err == nil || response == nil || response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expired previous token dial = (%v, %v), want HTTP 401", err, response)
	}
}
