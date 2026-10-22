package mcpmanagement

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/gofrs/flock"
)

const stateFormat = "gregale-mcp-state-v1"
const maxStateBytes = 32 << 20

// StateStore is an encrypted, atomic, single-writer bridge journal. Its key
// must be provided independently of the state file and retained across restarts.
type StateStore struct {
	mu      sync.Mutex
	path    string
	lock    *flock.Flock
	aead    cipher.AEAD
	records map[string]json.RawMessage
	failure error
	closed  bool
}

// OpenStateStore locks a private state directory until Close. The key file must
// contain 32 random raw bytes or 64 hex characters and have private permissions.
// Endpoint/grant bindings are checked when the HTTP handler restores the store.
func OpenStateStore(path, keyPath string) (*StateStore, error) {
	if path == "" || keyPath == "" {
		return nil, errors.New("state file and independent key file are both required")
	}
	key, err := readPrivateFile(keyPath, 128)
	if err != nil {
		return nil, fmt.Errorf("read MCP state key: %w", err)
	}
	if len(key) != 32 {
		key, err = hex.DecodeString(strings.TrimSpace(string(key)))
	}
	if err != nil || len(key) != 32 {
		return nil, errors.New("MCP state key must be 32 raw bytes or 64 hex characters")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("create MCP state directory: %w", err)
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("MCP state directory must be a private directory (0700)")
	}
	lockPath := path + ".lock"
	if info, err := os.Lstat(lockPath); err == nil && (!info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0) {
		return nil, errors.New("MCP state lock must be a private regular file")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	lock := flock.New(lockPath, flock.SetPermissions(0600))
	acquired, err := lock.TryLock()
	if err != nil || !acquired {
		_ = lock.Close()
		return nil, errors.New("MCP state is already in use or cannot be locked")
	}
	store := &StateStore{path: path, lock: lock, aead: aead, records: map[string]json.RawMessage{}}
	if err := store.load(); err != nil {
		_ = lock.Unlock()
		return nil, err
	}
	return store, nil
}

func readPrivateFile(path string, limit int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("must be a private regular file (0600)")
	}
	f, err := os.Open(path) //nolint:forbidigo // operator-owned state/key path, checked above for private permissions and no symlink
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err == nil && int64(len(data)) > limit {
		return nil, errors.New("private file is too large")
	}
	return data, err
}

func (s *StateStore) load() error {
	data, err := readPrivateFile(s.path, maxStateBytes)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read MCP state: %w", err)
	}
	n := s.aead.NonceSize()
	if len(data) < n {
		return errors.New("invalid encrypted MCP state")
	}
	plain, err := s.aead.Open(nil, data[:n], data[n:], []byte(stateFormat))
	if err != nil {
		return errors.New("MCP state authentication failed; use the original key and unmodified state")
	}
	if err := json.Unmarshal(plain, &s.records); err != nil || s.records == nil {
		return errors.New("invalid MCP state records")
	}
	return nil
}

func (s *StateStore) read(namespace string, value any) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failure != nil || s.closed {
		return errors.New("MCP state unavailable")
	}
	if data := s.records[namespace]; len(data) > 0 {
		return json.Unmarshal(data, value)
	}
	return nil
}

func (s *StateStore) update(namespace string, value any) error {
	return s.updateMany(map[string]any{namespace: value})
}

// updateMany commits related records in one encrypted, fsynced snapshot. In
// particular, an action's history and its replay payload must not diverge.
func (s *StateStore) updateMany(values map[string]any) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failure != nil || s.closed {
		return errors.New("MCP state unavailable; restart after restoring storage")
	}
	encoded := make(map[string]json.RawMessage, len(values))
	for namespace, value := range values {
		data, err := json.Marshal(value)
		if err != nil {
			return err
		}
		encoded[namespace] = data
	}
	for namespace, value := range values {
		if value == nil {
			delete(s.records, namespace)
		} else {
			s.records[namespace] = encoded[namespace]
		}
	}
	if snapshot, ok := values["oauth"].(oauthSnapshot); ok {
		for key := range s.records {
			for _, prefix := range []string{"plans/", "environment-plans/", "actions/", workloadActionsNamespace} {
				if grantID, matched := strings.CutPrefix(key, prefix); matched {
					if _, active := snapshot.Grants[grantID]; !active {
						delete(s.records, key)
					}
					break
				}
			}
		}
	}
	if err := s.flush(); err != nil {
		s.failure = err
		return errors.New("MCP state could not be committed; further access disabled")
	}
	return nil
}

func (s *StateStore) flush() error {
	plain, err := json.Marshal(s.records)
	if err != nil {
		return err
	}
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	data := s.aead.Seal(nonce, nonce, plain, []byte(stateFormat))
	if len(data) > maxStateBytes {
		return errors.New("MCP state capacity reached")
	}
	temp, err := os.CreateTemp(filepath.Dir(s.path), ".mcp-state-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(temp.Name()) }()
	if _, err = temp.Write(data); err == nil {
		err = temp.Sync()
	}
	closeErr := temp.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err := os.Rename(temp.Name(), s.path); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(s.path)) //nolint:forbidigo // private operator-owned directory validated during OpenStateStore; opened only for fsync
	if err != nil {
		return err
	}
	defer func() { _ = dir.Close() }()
	return dir.Sync()
}

func (s *StateStore) available() bool {
	if s == nil {
		return true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.failure == nil && !s.closed
}

// Close releases the single-writer lease; it never deletes committed state.
func (s *StateStore) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	return s.lock.Unlock()
}
