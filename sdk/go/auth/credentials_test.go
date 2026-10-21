package auth

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestVerifyReadsTheServiceAccount(t *testing.T) {
	key := newKey(t, "0A1B2C3D")
	app := newAuthApp(t, key)
	c, clk := newClient(t, app)
	payload := userClaims(c, clk)
	delete(payload, "session_id")
	for name, value := range map[string]any{"sub": "7c9e6679-7425-40de-944b-e07fc1f90ae7", "service_account": true, "secret_id": "k1", "scope": "docs:read",
		"org_id": "o1", "org_slug": "acme", "org_role": "deployer", "org_permissions": []string{"docs:read"}} {
		payload[name] = value
	}
	claims, err := c.Verify(context.Background(), key.sign(t, header(key.kid), payload))
	if err != nil {
		t.Fatal(err)
	}
	if want := (&ServiceAccount{ID: "7c9e6679-7425-40de-944b-e07fc1f90ae7", SecretID: "k1"}); !reflect.DeepEqual(claims.ServiceAccount, want) {
		t.Fatalf("service account = %+v", claims.ServiceAccount)
	}
	if !reflect.DeepEqual(claims.Scopes, []string{"docs:read"}) || claims.Organization == nil || claims.Organization.Role != "deployer" {
		t.Fatalf("scopes %v, organization %+v", claims.Scopes, claims.Organization)
	}
	// The marker without the secret it came from is not an account.
	delete(payload, "secret_id")
	marked, err := c.Verify(context.Background(), key.sign(t, header(key.kid), payload))
	if err != nil || marked.ServiceAccount != nil {
		t.Fatalf("without secret_id: %+v, %v", marked.ServiceAccount, err)
	}
	plain, err := c.Verify(context.Background(), key.sign(t, header(key.kid), userClaims(c, clk)))
	if err != nil || plain.ServiceAccount != nil {
		t.Fatalf("a person's token: %+v, %v", plain.ServiceAccount, err)
	}
}

func TestClientCredentialsCachesTokensUntilShortlyBeforeExpiry(t *testing.T) {
	const id = "7c9e6679-7425-40de-944b-e07fc1f90ae7"
	secret := "gasa_" + "a1B2-_c3D4" + "e5F6g7H8i9J0k1L2m3N4o5P6q7R8s9T0u"
	var requests atomic.Int32
	var mu sync.Mutex
	answer, status := `{"access_token":"token-1","token_type":"Bearer","expires_in":600}`, http.StatusOK
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		body, _ := io.ReadAll(r.Body)
		form, _ := url.ParseQuery(string(body))
		user, pass, ok := r.BasicAuth()
		if r.URL.Path != "/auth/v1/oauth/token" || !ok || user != id || pass != secret || form.Get("grant_type") != "client_credentials" || form.Get("scope") != "docs:read docs:write" {
			t.Errorf("request %s %v %q %q", r.URL.Path, form, user, base64.StdEncoding.EncodeToString([]byte(pass)))
		}
		mu.Lock()
		defer mu.Unlock()
		w.WriteHeader(status)
		_, _ = io.WriteString(w, answer)
	}))
	defer server.Close()
	machine, err := NewClientCredentials(server.URL, id, secret, WithScope("docs:read", "docs:write"))
	if err != nil {
		t.Fatal(err)
	}
	clk := &clock{t: time.Unix(1_800_000_000, 0)}
	machine.now = clk.now
	ctx := context.Background()
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if token, err := machine.Token(ctx); err != nil || token != "token-1" {
				t.Errorf("token = %q, %v", token, err)
			}
		}()
	}
	wg.Wait()
	if requests.Load() != 1 {
		t.Fatalf("%d requests for concurrent callers", requests.Load())
	}
	clk.advance(539 * time.Second)
	if token, _ := machine.Token(ctx); token != "token-1" || requests.Load() != 1 {
		t.Fatalf("refetched early: %q after %d requests", token, requests.Load())
	}
	mu.Lock()
	answer = `{"access_token":"token-2","token_type":"Bearer","expires_in":600}`
	mu.Unlock()
	clk.advance(time.Second)
	if token, _ := machine.Token(ctx); token != "token-2" {
		t.Fatalf("token = %q after the refresh point", token)
	}
	machine.Clear()
	mu.Lock()
	answer, status = `{"error":"invalid_client"}`, http.StatusUnauthorized
	mu.Unlock()
	_, err = machine.Token(ctx)
	var refusal *Error
	if !errors.As(err, &refusal) || refusal.Code != "invalid_client" || !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("refusal = %v", err)
	}
	for _, bad := range [][2]string{{"nope", secret}, {id, "secret"}} {
		if _, err := NewClientCredentials(server.URL, bad[0], bad[1]); err == nil {
			t.Fatalf("%v accepted", bad)
		}
	}
	if _, err := NewClientCredentials("http://auth.example.com", id, secret); err == nil {
		t.Fatal("plain HTTP to a remote host accepted")
	}
}
