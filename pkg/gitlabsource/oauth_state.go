package gitlabsource

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"strconv"
	"strings"
	"time"
)

const OAuthStateLifetime = 5 * time.Minute

func oauthStateMAC(key []byte, state, verifier, accountID, issuedAt string) string {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(state + "\x00" + verifier + "\x00" + accountID + "\x00" + issuedAt))
	return hex.EncodeToString(mac.Sum(nil))
}

// MintOAuthState returns a GitLab authorization URL and an account-bound,
// short-lived state-cookie value containing the PKCE verifier.
func MintOAuthState(config OAuthConfig, accountID string, key []byte, now time.Time) (string, string, error) {
	if accountID == "" || len(key) < 32 {
		return "", "", ErrUnavailable
	}
	nonce := make([]byte, 32)
	verifierBytes := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		return "", "", err
	}
	if _, err := rand.Read(verifierBytes); err != nil {
		return "", "", err
	}
	state := hex.EncodeToString(nonce)
	verifier := base64.RawURLEncoding.EncodeToString(verifierBytes)
	challengeBytes := sha256.Sum256([]byte(verifier))
	authorize, err := config.AuthorizationURL(state, base64.RawURLEncoding.EncodeToString(challengeBytes[:]))
	if err != nil {
		return "", "", err
	}
	issuedAt := strconv.FormatInt(now.Unix(), 10)
	value := strings.Join([]string{state, verifier, accountID, issuedAt, oauthStateMAC(key, state, verifier, accountID, issuedAt)}, ".")
	return authorize, value, nil
}

// VerifyOAuthState accepts only the same signed state, account and short time
// window, then returns the PKCE verifier for one authorization-code exchange.
func VerifyOAuthState(cookieValue, queryState, accountID string, key []byte, now time.Time) (string, bool) {
	parts := strings.Split(cookieValue, ".")
	if len(parts) != 5 || len(key) < 32 {
		return "", false
	}
	issuedUnix, err := strconv.ParseInt(parts[3], 10, 64)
	if err != nil {
		return "", false
	}
	age := now.Sub(time.Unix(issuedUnix, 0))
	if age < 0 || age > OAuthStateLifetime || len(parts[0]) != 64 || len(parts[1]) != 43 || parts[2] != accountID ||
		subtle.ConstantTimeCompare([]byte(parts[0]), []byte(queryState)) != 1 ||
		subtle.ConstantTimeCompare([]byte(parts[4]), []byte(oauthStateMAC(key, parts[0], parts[1], parts[2], parts[3]))) != 1 {
		return "", false
	}
	return parts[1], true
}
