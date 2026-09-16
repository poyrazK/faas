package main

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
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

func clearManagedSession() {
	if p, err := cliSessionMetadataPath(); err == nil {
		_ = os.Remove(p)
	}
}
