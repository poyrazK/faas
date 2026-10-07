package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/gofrs/flock"
	"github.com/onebox-faas/faas/pkg/api"
)

const startSessionVersion = 1

// startSession contains only recovery metadata. Tokens, secret values and
// secret-file paths belong to the credential/secrets workflows, never here.
type startSession struct {
	Version      int    `json:"version"`
	APIBase      string `json:"api_base"`
	AccountID    string `json:"account_id"`
	ScopePath    string `json:"scope_path"`
	SourcePath   string `json:"source_path"`
	Template     string `json:"template,omitempty"`
	AppSlug      string `json:"app_slug"`
	AppID        string `json:"app_id,omitempty"`
	DeploymentID string `json:"deployment_id,omitempty"`
	Status       string `json:"status"`
	Revision     int    `json:"revision,omitempty"`
	HealthPath   string `json:"health_path,omitempty"`
	UpdatedAt    string `json:"updated_at"`
}

func startSessionPath(scope string) (string, error) {
	dir := os.Getenv("XDG_STATE_HOME")
	if dir == "" {
		var err error
		dir, err = os.UserConfigDir()
		if err != nil {
			return "", fmt.Errorf("locate start session directory: %w", err)
		}
	}
	key := sha256.Sum256([]byte(scope))
	return filepath.Join(dir, "gregale", "start", hex.EncodeToString(key[:])+".json"), nil
}

func loadStartSession(path string) (startSession, bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return startSession{}, false, nil
	}
	if err != nil {
		return startSession{}, false, fmt.Errorf("read start session: %w", err)
	}
	var session startSession
	if err := json.Unmarshal(data, &session); err != nil {
		return session, true, fmt.Errorf("decode start session: %w", err)
	}
	if session.Version != startSessionVersion || session.APIBase == "" || session.AccountID == "" || session.ScopePath == "" || session.SourcePath == "" || !api.ValidAppSlug(session.AppSlug) {
		return session, true, errors.New("start session has an unsupported or incomplete format")
	}
	return session, true, nil
}

func saveStartSession(path string, session startSession) error {
	session.Version = startSessionVersion
	session.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	data, err := json.MarshalIndent(session, "", "  ")
	if err != nil {
		return fmt.Errorf("encode start session: %w", err)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create start session directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".start-*.tmp")
	if err != nil {
		return fmt.Errorf("create start session file: %w", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	defer func() { _ = tmp.Close() }()
	if err := tmp.Chmod(0o600); err != nil {
		return fmt.Errorf("protect start session: %w", err)
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("write start session: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("sync start session: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close start session: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("publish start session: %w", err)
	}
	return nil
}

func lockStartSession(path string) (*flock.Flock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create start session directory: %w", err)
	}
	lock := flock.New(path + ".lock")
	ok, err := lock.TryLock()
	if err != nil || !ok {
		_ = lock.Close()
		if err != nil {
			return nil, fmt.Errorf("lock start session: %w", err)
		}
		return nil, errors.New("another gregale start session is running for this directory")
	}
	return lock, nil
}
