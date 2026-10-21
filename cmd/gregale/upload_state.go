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
	"sort"
	"strings"
	"time"

	"github.com/gofrs/flock"
	"github.com/onebox-faas/faas/pkg/api"
)

const (
	resumableUploadStateVersion = 3
	uploadCacheMaxAge           = 7 * 24 * time.Hour
	uploadCacheMaxEntries       = 256
)

// resumableUploadState is deliberately local-only metadata. The server owns
// the upload bytes and offset; this record only lets a later CLI process find
// that server session and prove it is still uploading the same archive.
type resumableUploadState struct {
	Version                int    `json:"version"`
	UploadID               string `json:"upload_id"`
	AppSlug                string `json:"app_slug"`
	APIBase                string `json:"api_base"`
	RecoveryIdentitySHA256 string `json:"recovery_identity_sha256"`
	ArchivePath            string `json:"archive_path"`
	ArchiveSize            int64  `json:"archive_size"`
	ArchiveSHA256          string `json:"archive_sha256"`
	DeployOptionsSHA256    string `json:"deploy_options_sha256"`
	TotalSize              int64  `json:"total_size"`
	ChunkSize              int64  `json:"chunk_size"`
	CreatedAt              string `json:"created_at"`
	CommitAttempted        bool   `json:"commit_attempted,omitempty"`
}

// uploadRecoveryIdentity binds recovery to the authenticated connection,
// source bytes, deployment options, and logical retry intent. Temporary archive
// paths and mtimes do not determine source identity. Only the
// combined digest is persisted; credentials and retry keys are never saved.
func uploadRecoveryIdentity(c *Client, ctx context.Context, slug, archiveSHA256, optionsSHA256 string) string {
	values := []string{c.BaseURL(), c.Token(), slug, archiveSHA256, optionsSHA256, api.IdempotencyKeyFromContext(ctx)}
	data, _ := json.Marshal(values) // A slice of strings cannot fail to marshal.
	key := sha256.Sum256(data)
	return hex.EncodeToString(key[:])
}

// uploadStatePath is keyed by content and intent so a repacked archive can
// resume across CLI processes. The same identity also serializes local uploads.
func uploadStatePath(identity string) (string, error) {
	dir, err := uploadStateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, identity+".json"), nil
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

type resumableUploadLock struct {
	statePath string
	digest    *flock.Flock
	guard     *flock.Flock
}

func (l *resumableUploadLock) Unlock() error {
	return l.digest.Unlock()
}

func (l *resumableUploadLock) Close() error {
	digestErr := l.digest.Close()
	guardUnlockErr := l.guard.Unlock()
	guardCloseErr := l.guard.Close()
	tryRemoveOrphanUploadLock(l.statePath)
	return errors.Join(digestErr, guardUnlockErr, guardCloseErr)
}

func lockResumableUploadState(ctx context.Context, path string) (*resumableUploadLock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create upload state directory: %w", err)
	}
	// Upgrade cleanup: one process opportunistically removes legacy orphan
	// locks and stale recovery records before joining the shared operation
	// guard. A busy cache is skipped so deploy startup never waits on GC.
	tryCleanupUploadCache(uploadCachePolicy{MaxAge: uploadCacheMaxAge, MaxEntries: uploadCacheMaxEntries})
	guard := flock.New(uploadCacheGuardPath(filepath.Dir(path)), flock.SetPermissions(0o600))
	guarded, err := guard.TryRLockContext(ctx, 100*time.Millisecond)
	if err != nil {
		_ = guard.Close()
		return nil, fmt.Errorf("guard upload recovery state: %w", err)
	}
	if !guarded {
		_ = guard.Close()
		return nil, errors.New("upload cache cleanup is in progress")
	}
	lock := flock.New(path+".lock", flock.SetPermissions(0o600))
	locked, err := lock.TryLockContext(ctx, 100*time.Millisecond)
	if err != nil {
		_ = lock.Close()
		_ = guard.Unlock()
		_ = guard.Close()
		return nil, fmt.Errorf("lock upload recovery state: %w", err)
	}
	if !locked {
		_ = lock.Close()
		_ = guard.Unlock()
		_ = guard.Close()
		return nil, errors.New("another Gregale deploy is already uploading this archive")
	}
	return &resumableUploadLock{statePath: path, digest: lock, guard: guard}, nil
}

// isUploadSessionCap reports the per-app open upload session refusal.
func isUploadSessionCap(err error) bool {
	var ae *api.APIError
	return errors.As(err, &ae) && ae.Problem.Code == api.CodeUploadSessionTooMany
}

// reclaimAppUploadSessions cancels this CLI's other recorded upload sessions
// for the app and forgets them. States are keyed by archive and deploy
// options, so this deploy can never resume them, yet each server session
// counts against the per-app cap of five until its 24 h TTL: after five
// failed submissions of edited sources the app could not deploy at all
// (production-us hunt #9, "Too many upload sessions"). A state another local
// deploy holds locked is left alone. Uncertain commits retain their recovery
// evidence, and a failed cancellation never removes a local receipt.
func reclaimAppUploadSessions(ctx context.Context, c *Client, slug, keepStatePath string) int {
	dir := filepath.Dir(keepStatePath)
	files, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	reclaimed := 0
	for _, file := range files {
		name, path := file.Name(), filepath.Join(dir, file.Name())
		if file.IsDir() || !strings.HasSuffix(name, ".json") || strings.HasPrefix(name, ".") || path == keepStatePath {
			continue
		}
		state, found, err := loadResumableUploadState(path)
		if err != nil || !found || state.AppSlug != slug || state.APIBase != c.BaseURL() || state.UploadID == "" || state.CommitAttempted {
			continue
		}
		lock := flock.New(path+".lock", flock.SetPermissions(0o600))
		if locked, err := lock.TryLock(); err != nil || !locked {
			_ = lock.Close()
			continue
		}
		// Re-read after locking: the previous owner may have attempted commit
		// between discovery and our acquisition of its recovery lock.
		current, currentFound, currentErr := loadResumableUploadState(path)
		if currentErr != nil || !currentFound || current.UploadID != state.UploadID || current.CommitAttempted || current.APIBase != c.BaseURL() || current.AppSlug != slug {
			_ = lock.Unlock()
			_ = lock.Close()
			continue
		}
		if err := c.CancelUpload(ctx, state.UploadID); err != nil {
			_ = lock.Unlock()
			_ = lock.Close()
			continue
		}
		removeResumableUploadState(path)
		_ = lock.Unlock()
		_ = lock.Close()
		tryRemoveOrphanUploadLock(path)
		reclaimed++
	}
	return reclaimed
}

type uploadCachePolicy struct {
	MaxAge     time.Duration
	MaxEntries int
}

type uploadCacheEntry struct {
	Key       string    `json:"key"`
	StatePath string    `json:"state_path,omitempty"`
	LockPath  string    `json:"lock_path,omitempty"`
	CreatedAt time.Time `json:"created_at,omitempty"`
	Status    string    `json:"status"`
	Action    string    `json:"action,omitempty"`
}

type uploadCacheCleanupResult struct {
	Scanned int `json:"scanned"`
	Removed int `json:"removed"`
	Kept    int `json:"kept"`
}

func uploadCacheGuardPath(dir string) string { return filepath.Join(dir, ".cache.lock") }

func inspectUploadCache(now time.Time, policy uploadCachePolicy) ([]uploadCacheEntry, error) {
	dir, err := uploadStateDir()
	if err != nil {
		return nil, err
	}
	files, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return []uploadCacheEntry{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read upload cache: %w", err)
	}
	byKey := map[string]*uploadCacheEntry{}
	for _, file := range files {
		name := file.Name()
		if file.IsDir() || name == ".cache.lock" || strings.HasPrefix(name, ".upload-state-") {
			continue
		}
		var key string
		switch {
		case strings.HasSuffix(name, ".json.lock"):
			key = strings.TrimSuffix(name, ".json.lock")
		case strings.HasSuffix(name, ".json"):
			key = strings.TrimSuffix(name, ".json")
		default:
			continue
		}
		entry := byKey[key]
		if entry == nil {
			entry = &uploadCacheEntry{Key: key}
			byKey[key] = entry
		}
		path := filepath.Join(dir, name)
		if strings.HasSuffix(name, ".lock") {
			entry.LockPath = path
		} else {
			entry.StatePath = path
		}
	}
	entries := make([]uploadCacheEntry, 0, len(byKey))
	for _, entry := range byKey {
		switch entry.StatePath {
		case "":
			entry.Status = "orphan_lock"
			entry.Action = "remove"
		default:
			state, _, loadErr := loadResumableUploadState(entry.StatePath)
			if loadErr != nil {
				entry.Status = "invalid_state"
				entry.Action = "remove"
			} else {
				entry.Status = "resumable"
				entry.CreatedAt, _ = time.Parse(time.RFC3339Nano, state.CreatedAt)
				if entry.CreatedAt.IsZero() {
					if info, statErr := os.Stat(entry.StatePath); statErr == nil {
						entry.CreatedAt = info.ModTime()
					}
				}
				if policy.MaxAge > 0 && !entry.CreatedAt.IsZero() && now.Sub(entry.CreatedAt) > policy.MaxAge {
					entry.Status = "stale"
					entry.Action = "remove"
				}
			}
		}
		entries = append(entries, *entry)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].CreatedAt.After(entries[j].CreatedAt) })
	if policy.MaxEntries >= 0 {
		keptStates := 0
		for i := range entries {
			if entries[i].StatePath == "" || entries[i].Action != "" {
				continue
			}
			keptStates++
			if keptStates > policy.MaxEntries {
				entries[i].Status = "over_capacity"
				entries[i].Action = "remove"
			}
		}
	}
	return entries, nil
}

func cleanupUploadCache(now time.Time, policy uploadCachePolicy, apply bool) (uploadCacheCleanupResult, []uploadCacheEntry, error) {
	entries, err := inspectUploadCache(now, policy)
	if err != nil {
		return uploadCacheCleanupResult{}, nil, err
	}
	result := uploadCacheCleanupResult{Scanned: len(entries)}
	for _, entry := range entries {
		if entry.Action == "" {
			result.Kept++
			continue
		}
		if !apply {
			continue
		}
		removed := false
		for _, path := range []string{entry.StatePath, entry.LockPath} {
			if path == "" {
				continue
			}
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return result, entries, fmt.Errorf("remove upload cache entry %q: %w", entry.Key, err)
			}
			removed = true
		}
		if removed {
			result.Removed++
		}
	}
	return result, entries, nil
}

func tryCleanupUploadCache(policy uploadCachePolicy) {
	dir, err := uploadStateDir()
	if err != nil || os.MkdirAll(dir, 0o700) != nil {
		return
	}
	guard := flock.New(uploadCacheGuardPath(dir), flock.SetPermissions(0o600))
	locked, err := guard.TryLock()
	if err != nil || !locked {
		_ = guard.Close()
		return
	}
	_, _, _ = cleanupUploadCache(time.Now(), policy, true)
	_ = guard.Unlock()
	_ = guard.Close()
}

func cleanupUploadCacheExclusive(ctx context.Context, policy uploadCachePolicy, apply bool) (uploadCacheCleanupResult, []uploadCacheEntry, error) {
	dir, err := uploadStateDir()
	if err != nil {
		return uploadCacheCleanupResult{}, nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return uploadCacheCleanupResult{}, nil, fmt.Errorf("create upload state directory: %w", err)
	}
	guard := flock.New(uploadCacheGuardPath(dir), flock.SetPermissions(0o600))
	locked, err := guard.TryLockContext(ctx, 100*time.Millisecond)
	if err != nil {
		_ = guard.Close()
		return uploadCacheCleanupResult{}, nil, fmt.Errorf("lock upload cache: %w", err)
	}
	if !locked {
		_ = guard.Close()
		return uploadCacheCleanupResult{}, nil, errors.New("upload cache is busy; wait for active uploads to finish")
	}
	defer func() {
		_ = guard.Unlock()
		_ = guard.Close()
	}()
	return cleanupUploadCache(time.Now(), policy, apply)
}

func tryRemoveOrphanUploadLock(statePath string) {
	if _, err := os.Stat(statePath); err == nil || !errors.Is(err, os.ErrNotExist) {
		return
	}
	dir := filepath.Dir(statePath)
	guard := flock.New(uploadCacheGuardPath(dir), flock.SetPermissions(0o600))
	locked, err := guard.TryLock()
	if err != nil || !locked {
		_ = guard.Close()
		return
	}
	if _, err := os.Stat(statePath); errors.Is(err, os.ErrNotExist) {
		_ = os.Remove(statePath + ".lock")
	}
	_ = guard.Unlock()
	_ = guard.Close()
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
