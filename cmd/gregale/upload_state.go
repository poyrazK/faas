package main

import (
	"context"
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

const resumableUploadStateVersion = 2

// resumableUploadState is deliberately local-only metadata. The server owns
// the upload bytes and offset; this record only lets a later CLI process find
// that server session and prove it is still uploading the same archive.
type resumableUploadState struct {
	Version                int    `json:"version"`
	UploadID               string `json:"upload_id"`
	AppSlug                string `json:"app_slug"`
	APIBase                string `json:"api_base"`
	ArchivePath            string `json:"archive_path"`
	ArchiveSize            int64  `json:"archive_size"`
	ArchiveSHA256          string `json:"archive_sha256"`
	ArchiveModTimeUnixNano int64  `json:"archive_mtime_unix_nano"`
	DeployOptionsSHA256    string `json:"deploy_options_sha256"`
	TotalSize              int64  `json:"total_size"`
	ChunkSize              int64  `json:"chunk_size"`
	CreatedAt              string `json:"created_at"`
}

// uploadStatePath is keyed by the app and absolute archive path so rerunning
// the same deploy resumes naturally while separate archives can progress in
// parallel. The path itself is stored only as diagnostic metadata.
func uploadStatePath(appSlug, archivePath string) (string, error) {
	abs, err := filepath.Abs(archivePath)
	if err != nil {
		return "", fmt.Errorf("absolutize upload archive: %w", err)
	}
	dir, err := uploadStateDir()
	if err != nil {
		return "", err
	}
	key := sha256.Sum256([]byte(apiBase() + "\x00" + appSlug + "\x00" + abs))
	return filepath.Join(dir, hex.EncodeToString(key[:])+".json"), nil
}

func uploadStateDir() (string, error) {
	// XDG_STATE_HOME is the right place on Linux and is also useful in CI.
	// Fall back to the existing per-user Gregale config directory on macOS
	// and Windows, where Go does not expose XDG state semantics.
	if dir := os.Getenv("XDG_STATE_HOME"); dir != "" {
		return filepath.Join(dir, "gregale", "uploads"), nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("locate Gregale upload state: %w", err)
	}
	return filepath.Join(dir, "gregale", "uploads"), nil
}

func loadResumableUploadState(path string) (resumableUploadState, bool, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return resumableUploadState{}, false, nil
	}
	if err != nil {
		return resumableUploadState{}, false, fmt.Errorf("read upload state: %w", err)
	}
	var state resumableUploadState
	if err := json.Unmarshal(b, &state); err != nil {
		return resumableUploadState{}, true, fmt.Errorf("decode upload state: %w", err)
	}
	if state.Version != resumableUploadStateVersion || state.UploadID == "" || state.AppSlug == "" || state.ArchiveSize <= 0 {
		return resumableUploadState{}, true, errors.New("upload state has an unsupported or incomplete format")
	}
	return state, true, nil
}

func saveResumableUploadState(path string, state resumableUploadState) error {
	if state.Version == 0 {
		state.Version = resumableUploadStateVersion
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode upload state: %w", err)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create upload state directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".upload-state-*.tmp")
	if err != nil {
		return fmt.Errorf("create upload state temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		_ = os.Remove(tmpName)
	}()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("protect upload state: %w", err)
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write upload state: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync upload state: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close upload state: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("publish upload state: %w", err)
	}
	return nil
}

func removeResumableUploadState(path string) {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		if !jsonOutput {
			PrintWarn(osStderr, "could not remove upload recovery state: %v", err)
		}
	}
}

func lockResumableUploadState(ctx context.Context, path string) (*flock.Flock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create upload state directory: %w", err)
	}
	lock := flock.New(path+".lock", flock.SetPermissions(0o600))
	locked, err := lock.TryLockContext(ctx, 100*time.Millisecond)
	if err != nil {
		_ = lock.Close()
		return nil, fmt.Errorf("lock upload recovery state: %w", err)
	}
	if !locked {
		_ = lock.Close()
		return nil, errors.New("another Gregale deploy is already uploading this archive")
	}
	return lock, nil
}

func deployOptionsFingerprint(options []api.UploadDeployOptions) (string, error) {
	var value api.UploadDeployOptions
	if len(options) > 0 {
		value = options[0]
	}
	b, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("encode deploy options: %w", err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}
