//go:build linux

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const runtimeSecretPollInterval = 10 * time.Second

type runtimeSecretsState struct {
	mu      sync.RWMutex
	secrets map[string]string
}

func newRuntimeSecretsState(initial map[string]string) *runtimeSecretsState {
	return &runtimeSecretsState{secrets: cloneRuntimeSecrets(initial)}
}

func (s *runtimeSecretsState) snapshot() map[string]string {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneRuntimeSecrets(s.secrets)
}

// publish couples the guest-local projection with the in-memory snapshot used
// by future supervisor starts. Holding the lock across the atomic file publish
// prevents a crash/restart from snapshotting stale env after the file changed.
func (s *runtimeSecretsState) publish(path string, uid int, secrets map[string]string) error {
	if s == nil {
		return errors.New("runtime secrets state is unavailable")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := writeRuntimeSecretsProjection(path, uid, secrets); err != nil {
		return err
	}
	s.secrets = cloneRuntimeSecrets(secrets)
	return nil
}

func cloneRuntimeSecrets(secrets map[string]string) map[string]string {
	clone := make(map[string]string, len(secrets))
	for key, value := range secrets {
		clone[key] = value
	}
	return clone
}

func runtimeSecretsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for key, value := range a {
		if other, ok := b[key]; !ok || other != value {
			return false
		}
	}
	return true
}

func startRuntimeSecretReloader(ctx context.Context, manifest api.AppManifest, secrets *runtimeSecretsState, sup *Supervisor, log *slog.Logger) {
	if ctx == nil || secrets == nil || sup == nil || manifest.SecretReloadSignal == "" {
		return
	}
	if log == nil {
		log = slog.Default()
	}
	signal := secretReloadSyscall(manifest.SecretReloadSignal)
	go func() {
		ticker := time.NewTicker(runtimeSecretPollInterval)
		defer ticker.Stop()
		lastRevision := ""
		for {
			response, err := fetchRuntimeSecrets(lastRevision)
			if err != nil {
				log.Debug("guest-init: runtime secret refresh unavailable", "err_kind", "fetch_failed")
			} else if response.Error != "" {
				log.Debug("guest-init: runtime secret refresh rejected", "reason", response.Error)
			} else if !validGuestRuntimeSecretRevision(response.Revision) {
				lastRevision = ""
				log.Debug("guest-init: runtime secret revision was invalid", "err_kind", "invalid_response")
			} else if response.Unchanged {
				if response.Revision == lastRevision {
					lastRevision = response.Revision
				} else {
					lastRevision = ""
					log.Debug("guest-init: runtime secret revision response was inconsistent", "err_kind", "invalid_response")
				}
			} else if response.Secrets == nil {
				lastRevision = ""
				log.Debug("guest-init: runtime secret response omitted its payload", "err_kind", "invalid_response")
			} else {
				current := secrets.snapshot()
				fresh := *response.Secrets
				if fresh == nil {
					fresh = map[string]string{}
				}
				if runtimeSecretsEqual(current, fresh) {
					lastRevision = response.Revision
				} else if err := secrets.publish(secretReloadFilePath, lookupUID(manifest.EffectiveUser()), fresh); err != nil {
					log.Warn("guest-init: runtime secret projection update failed", "err_kind", "write_failed")
				} else {
					lastRevision = response.Revision
					if err := sup.ForwardSignalOnStart(signal); err != nil && !errors.Is(err, os.ErrProcessDone) {
						log.Debug("guest-init: secret reload signal could not be forwarded", "signal", signal.String(), "err_kind", "signal_failed")
					}
					log.Info("guest-init: runtime secrets updated", "count", len(fresh), "revision", lastRevision)
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

func validGuestRuntimeSecretRevision(revision string) bool {
	if len(revision) != sha256.Size*2 {
		return false
	}
	decoded, err := hex.DecodeString(revision)
	return err == nil && len(decoded) == sha256.Size
}

func fetchRuntimeSecrets(revision string) (runtimeConfigResponse, error) {
	conn, err := dialRuntimeConfigHost()
	if err != nil {
		return runtimeConfigResponse{}, err
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(4 * time.Second))
	body, err := json.Marshal(runtimeConfigRequest{Kind: "secrets", Revision: revision})
	if err != nil {
		return runtimeConfigResponse{}, err
	}
	if err := writeRuntimeConfigFrame(conn, body); err != nil {
		return runtimeConfigResponse{}, err
	}
	frame, err := readRuntimeConfigFrame(conn)
	if err != nil {
		return runtimeConfigResponse{}, err
	}
	var response runtimeConfigResponse
	if err := json.Unmarshal(frame, &response); err != nil {
		return runtimeConfigResponse{}, err
	}
	return response, nil
}

func secretReloadSyscall(name string) syscall.Signal {
	switch name {
	case "SIGUSR1":
		return syscall.SIGUSR1
	case "SIGUSR2":
		return syscall.SIGUSR2
	default:
		return syscall.SIGHUP
	}
}

func writeRuntimeSecretsProjection(path string, uid int, secrets map[string]string) error {
	return writeRuntimeSecretsProjectionForOwner(path, uid, 0, 0, secrets)
}

func writeRuntimeSecretsProjectionForOwner(path string, uid, dirUID, dirGID int, secrets map[string]string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o711); err != nil {
		return fmt.Errorf("create secret projection directory: %w", err)
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("inspect secret projection directory: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("secret projection path is not a directory")
	}
	if err := os.Chown(dir, dirUID, dirGID); err != nil {
		return fmt.Errorf("secure secret projection directory owner: %w", err)
	}
	if err := os.Chmod(dir, 0o711); err != nil {
		return fmt.Errorf("secure secret projection directory mode: %w", err)
	}
	if secrets == nil {
		secrets = map[string]string{}
	}
	body, err := json.Marshal(secrets)
	if err != nil {
		return fmt.Errorf("encode secret projection: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".secrets-*.tmp")
	if err != nil {
		return fmt.Errorf("create secret projection temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if err := tmp.Chown(uid, dirGID); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("set secret projection owner: %w", err)
	}
	if err := tmp.Chmod(0o400); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("set secret projection mode: %w", err)
	}
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write secret projection: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync secret projection: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close secret projection: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("publish secret projection: %w", err)
	}
	return nil
}
