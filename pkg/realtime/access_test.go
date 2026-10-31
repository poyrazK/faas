package realtime

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type testAccessReader struct {
	mu       sync.Mutex
	snapshot state.ManagedRealtimeAccess
}

func (r *testAccessReader) ReadRealtimeAccess(context.Context, string, string) (state.ManagedRealtimeAccess, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.snapshot, nil
}
func testAccessToken(issued time.Time) string {
	return "e30." + base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf(`{"iat":%d}`, issued.Unix()))) + ".test"
}

func TestAccessTokenAndScopedChannelRules(t *testing.T) {
	now := time.Now().UTC()
	for _, payload := range []string{`{}`, `{"iat":"123"}`, `{"iat":0}`, `{"iat":1.5}`, fmt.Sprintf(`{"iat":%d}`, now.Add(time.Hour).Unix())} {
		if _, err := verifiedTokenIssuedAt("e30." + base64.RawURLEncoding.EncodeToString([]byte(payload)) + ".test"); err == nil {
			t.Fatalf("accepted iat: %s", payload)
		}
	}
	issued, err := verifiedTokenIssuedAt(testAccessToken(now.Add(-time.Minute)))
	if err != nil {
		t.Fatal(err)
	}
	snapshot := state.ManagedRealtimeAccess{DeniedChannels: []string{"document"}, SessionsRevokedBefore: &now}
	if !sessionRevoked(snapshot, issued) || sessionRevoked(snapshot, now.Add(time.Second)) {
		t.Fatal("cutoff comparison")
	}
	scope, err := api.RealtimeActivityScopeChannel("document", "editors")
	if err != nil {
		t.Fatal(err)
	}
	if !channelDenied(snapshot, "document") || !channelDenied(snapshot, scope) || channelDenied(snapshot, "unrelated") {
		t.Fatal("scope deny inheritance")
	}
}

func TestAccessRevocationClosesIdleSessionAndRejectsReconnection(t *testing.T) {
	reader := &testAccessReader{}
	m := NewManager(Config{JWTAuthorizer: &recordingJWTAuthorizer{}, AccessPreview: true, AccessReader: reader}, nil)
	defer m.Close()
	if err := m.RegisterEndpoint(Endpoint{Preview: true, ID: "private", ClientAuth: AuthPolicy{Mode: AuthModeOIDCJWT, Issuer: "https://issuer.example", JWKSURL: "https://issuer.example/jwks", Algorithms: []string{"RS256"}}}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(m.Handler())
	defer server.Close()
	target := "ws" + server.URL[len("http"):] + ManagedPathPrefix + "private"
	header := http.Header{"Authorization": []string{"Bearer " + testAccessToken(time.Now().Add(-time.Minute))}}
	client, response, err := websocket.DefaultDialer.Dial(target, header)
	if response != nil && response.Body != nil {
		response.Body.Close()
	}
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	cutoff := time.Now().UTC()
	reader.mu.Lock()
	reader.snapshot.SessionsRevokedBefore = &cutoff
	reader.mu.Unlock()
	client.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, _, err = client.ReadMessage()
	if !websocket.IsCloseError(err, websocket.ClosePolicyViolation) {
		t.Fatalf("idle close: %v", err)
	}
	_, response, err = websocket.DefaultDialer.Dial(target, header)
	if response == nil || response.StatusCode != http.StatusUnauthorized || err == nil {
		t.Fatalf("old token reconnect: %v %+v", err, response)
	}
	if response != nil && response.Body != nil {
		response.Body.Close()
	}
}
