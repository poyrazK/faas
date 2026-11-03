package main

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// cliSessionMetadata is deliberately non-secret. It binds the locally stored
// token to the exact server key that an interactive login minted, allowing
// logout to revoke only that credential. TokenFingerprint prevents stale
// metadata from revoking a different token after a manual keychain replacement.
type cliSessionMetadata struct {
	KeyID            string `json:"key_id"`
	APIBase          string `json:"api_base"`
	TokenFingerprint string `json:"token_fingerprint,omitempty"`
	FingerprintSalt  string `json:"fingerprint_salt,omitempty"`
	// TokenSHA256 is read-only compatibility for sessions created before the
	// keyed fingerprint format. Legacy metadata is consumed once at logout.
	TokenSHA256 string `json:"token_sha256,omitempty"`
	Managed     bool   `json:"managed"`
}

func cliSessionMetadataPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	if name := currentProfile(); name != "default" {
		return filepath.Join(dir, "gregale", "profiles", name, "session.json"), nil
	}
	return filepath.Join(dir, "gregale", "session.json"), nil
}

func tokenFingerprint(token string, salt []byte) string {
	mac := hmac.New(sha256.New, salt)
	_, _ = mac.Write([]byte("gregale-cli-session\x00"))
	_, _ = mac.Write([]byte(strings.TrimSpace(token)))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func saveManagedSession(token, keyID, baseURL string) error {
	if strings.TrimSpace(keyID) == "" {
		clearManagedSession()
		return nil
	}
	p, err := cliSessionMetadataPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	salt := make([]byte, 32)
	if _, err := rand.Read(salt); err != nil {
		return fmt.Errorf("generate session fingerprint salt: %w", err)
	}
	data, err := json.Marshal(cliSessionMetadata{
		KeyID:            keyID,
		APIBase:          normalizeAPIBase(baseURL),
		TokenFingerprint: tokenFingerprint(token, salt),
		FingerprintSalt:  base64.RawURLEncoding.EncodeToString(salt),
		Managed:          true,
	})
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".session-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, p); err != nil {
		return err
	}
	return nil
}

func loadManagedSession() (cliSessionMetadata, error) {
	p, err := cliSessionMetadataPath()
	if err != nil {
		return cliSessionMetadata{}, err
	}
	data, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return cliSessionMetadata{}, nil
	}
	if err != nil {
		return cliSessionMetadata{}, err
	}
	var meta cliSessionMetadata
	if err := json.Unmarshal(data, &meta); err != nil {
		return cliSessionMetadata{}, fmt.Errorf("decode CLI session metadata: %w", err)
	}
	return meta, nil
}

func (m cliSessionMetadata) matches(token string) bool {
	if !m.Managed || m.KeyID == "" {
		return false
	}
	if m.TokenFingerprint != "" && m.FingerprintSalt != "" {
		salt, err := base64.RawURLEncoding.DecodeString(m.FingerprintSalt)
		if err != nil || len(salt) != 32 {
			return false
		}
		want, err := base64.RawURLEncoding.DecodeString(m.TokenFingerprint)
		if err != nil {
			return false
		}
		got, err := base64.RawURLEncoding.DecodeString(tokenFingerprint(token, salt))
		return err == nil && hmac.Equal(got, want)
	}
	// Older releases stored an unkeyed token digest. Avoid continuing that
	// weak format: accept the already-bound key ID for one final logout and
	// remove the metadata immediately afterward.
	return m.TokenSHA256 != ""
}

// managedLogin is a saved credential together with the CLI key that an
// interactive login minted for it.
type managedLogin struct {
	token string
	meta  cliSessionMetadata
}

// currentManagedLogin returns the selected profile's saved login when an
// interactive login minted it (session metadata bound to the stored token),
// otherwise the zero value. A key the user created and passed with --token
// has no metadata, so the CLI never revokes it.
func currentManagedLogin() managedLogin {
	token := loadStoredToken(io.Discard)
	meta, err := loadManagedSession()
	if err != nil || token == "" || !meta.matches(token) {
		return managedLogin{}
	}
	return managedLogin{token: token, meta: meta}
}

// revokeManagedLogin revokes the CLI key behind a saved login, using that
// login's own credential against the API it was minted on. A key the server
// no longer accepts (401) or knows (404) is already unusable.
func revokeManagedLogin(ctx context.Context, login managedLogin) error {
	base := login.meta.APIBase
	if base == "" {
		base = apiBase()
	}
	client := NewClient(base, login.token)
	client.SetCompletionCache(nil)
	err := client.DeleteKey(ctx, login.meta.KeyID)
	var apiErr *APIError
	if errors.As(err, &apiErr) && (apiErr.Problem.Status == http.StatusUnauthorized || apiErr.Problem.Status == http.StatusNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("could not revoke CLI key %s: %w", login.meta.KeyID, err)
	}
	return nil
}

// revokeReplacedLogin is the best-effort revoke for a login that re-login or
// `config profile remove` has already replaced or deleted locally: failure
// only warns, naming the key so the user can revoke it by hand.
func revokeReplacedLogin(ctx context.Context, login managedLogin) {
	if err := revokeManagedLogin(ctx, login); err != nil {
		PrintWarn(osStderr, "%s. Revoke it with 'gregale key delete %s' while signed in to that account.", redactErrorText(err.Error()), login.meta.KeyID)
	}
}

func clearManagedSession() {
	if p, err := cliSessionMetadataPath(); err == nil {
		_ = os.Remove(p)
	}
}
