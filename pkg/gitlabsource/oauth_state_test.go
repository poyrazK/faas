package gitlabsource

import (
	"crypto/sha256"
	"encoding/base64"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestGitLabOAuthStateBindsAccountAndPKCE(t *testing.T) {
	config := OAuthConfig{ClientID: "client", ClientSecret: "secret", RedirectURI: "https://example.test/v1/gitlab/callback"}
	key := []byte("test-key-with-at-least-32-bytes-123456")
	authorize, cookie, err := MintOAuthState(config, "account-one", key, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(authorize)
	if err != nil {
		t.Fatal(err)
	}
	state := parsed.Query().Get("state")
	verifier, valid := VerifyOAuthState(cookie, state, "account-one", key, time.Now())
	if !valid {
		t.Fatal("valid state rejected")
	}
	challenge := sha256.Sum256([]byte(verifier))
	if parsed.Query().Get("code_challenge") != base64.RawURLEncoding.EncodeToString(challenge[:]) || parsed.Query().Get("code_challenge_method") != "S256" {
		t.Fatal("PKCE challenge does not match the stored verifier")
	}
	for _, test := range []struct {
		cookie, state, account string
		key                    []byte
		now                    time.Time
	}{
		{cookie, state, "account-two", key, time.Now()},
		{cookie, strings.Repeat("a", 64), "account-one", key, time.Now()},
		{cookie, state, "account-one", []byte("different-key"), time.Now()},
		{cookie, state, "account-one", key, time.Now().Add(OAuthStateLifetime + time.Second)},
	} {
		if _, valid := VerifyOAuthState(test.cookie, test.state, test.account, test.key, test.now); valid {
			t.Fatal("invalid OAuth state accepted")
		}
	}
}
