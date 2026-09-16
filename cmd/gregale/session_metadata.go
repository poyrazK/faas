package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// cliSessionMetadata is deliberately non-secret. It binds the locally stored
// token to the exact server key that an interactive login minted, allowing
// logout to revoke only that credential. TokenSHA256 prevents stale metadata
// from revoking a different token after a manual keychain replacement.
type cliSessionMetadata struct {
	KeyID       string `json:"key_id"`
	APIBase     string `json:"api_base"`
	TokenSHA256 string `json:"token_sha256"`
	Managed     bool   `json:"managed"`
}

func cliSessionMetadataPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "gregale", "session.json"), nil
}

func tokenSHA256(token string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(token)))
	return hex.EncodeToString(sum[:])
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
	data, err := json.Marshal(cliSessionMetadata{
		KeyID:       keyID,
		APIBase:     normalizeAPIBase(baseURL),
		TokenSHA256: tokenSHA256(token),
		Managed:     true,
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
	return m.Managed && m.KeyID != "" && m.TokenSHA256 != "" &&
		m.TokenSHA256 == tokenSHA256(token)
}

func clearManagedSession() {
	if p, err := cliSessionMetadataPath(); err == nil {
		_ = os.Remove(p)
	}
}
