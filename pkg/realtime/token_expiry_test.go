package realtime

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// A v1 OIDC socket never re-checks its token, so it ends when the token
// expires rather than living out the endpoint's MaxConnectionAge on it.
func TestOIDCSocketEndsWhenItsTokenExpires(t *testing.T) {
	m := NewManager(Config{JWTAuthorizer: &recordingJWTAuthorizer{}}, nil)
	defer m.Close()
	if err := m.RegisterEndpoint(Endpoint{Preview: true, ID: "private", MaxConnectionAge: time.Hour,
		ClientAuth: AuthPolicy{Mode: AuthModeOIDCJWT, Issuer: "https://issuer.example",
			JWKSURL: "https://issuer.example/.well-known/jwks.json", Algorithms: []string{"RS256"}},
	}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(m.Handler())
	defer server.Close()
	exp := time.Now().Add(1500 * time.Millisecond).Truncate(time.Second).Add(time.Second)
	payload := base64.RawURLEncoding.EncodeToString(fmt.Appendf(nil, `{"sub":"user-123","exp":%d}`, exp.Unix()))
	client, response, err := websocket.DefaultDialer.Dial("ws"+server.URL[len("http"):]+ManagedPathPrefix+"private",
		map[string][]string{"Authorization": {"Bearer e30." + payload + ".sig"}})
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
	if snap := m.Snapshot(); len(snap) != 1 || !snap[0].Expires.Equal(exp.UTC()) {
		t.Fatalf("connection expires %+v, want the token's exp %s", snap, exp.UTC())
	}
	_ = client.SetReadDeadline(time.Now().Add(10 * time.Second))
	for {
		if _, _, err = client.ReadMessage(); err != nil {
			break
		}
	}
	var closed *websocket.CloseError
	if !errors.As(err, &closed) || closed.Code != websocket.ClosePolicyViolation || closed.Text != "connection lifetime exceeded" {
		t.Fatalf("socket ended with %v, want a lifetime close at the token's expiry", err)
	}
	if time.Now().After(exp.Add(5 * time.Second)) {
		t.Fatal("socket outlived its token by more than a few seconds")
	}
}

func TestVerifiedTokenExpiry(t *testing.T) {
	enc := func(s string) string { return "e30." + base64.RawURLEncoding.EncodeToString([]byte(s)) + ".sig" }
	if got := verifiedTokenExpiry(enc(`{"exp":1700000000}`)); !got.Equal(time.Unix(1_700_000_000, 0)) {
		t.Fatalf("exp = %s", got)
	}
	for _, token := range []string{"not-a-jwt", enc(`{}`), enc(`{"exp":-1}`), enc(`not json`)} {
		if got := verifiedTokenExpiry(token); !got.IsZero() {
			t.Fatalf("%q: exp = %s, want none", token, got)
		}
	}
}
