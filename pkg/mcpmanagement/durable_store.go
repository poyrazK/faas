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

// StateStore is an encrypted, single-writer bridge journal. Its key must be
// provided independently of the stored state and retained across restarts.
// A self-hosted bridge keeps it in a private file; the hosted bridge keeps it
// in Postgres under a lease (ADR-20261026195952).
type StateStore struct {
	mu      sync.Mutex
	persist statePersistence
	records map[string]json.RawMessage
	failure error
	closed  bool
}

// statePersistence makes the store's records durable.
type statePersistence interface {
	load() (map[string]json.RawMessage, error)
	// commit makes records durable. changed names the namespaces this update
	// set or removed; a backend that stores the records whole may ignore it.
	commit(records map[string]json.RawMessage, changed []string) error
	close() error
}

func openStateStore(persist statePersistence) (*StateStore, error) {
	records, err := persist.load()
	if err != nil {
		_ = persist.close()
		return nil, err
	}
	if records == nil {
		records = map[string]json.RawMessage{}
	}
	return &StateStore{persist: persist, records: records}, nil
}

// OpenStateStore locks a private state directory until Close. The key file must
// contain 32 random raw bytes or 64 hex characters and have private permissions.
// Endpoint/grant bindings are checked when the HTTP handler restores the store.
func OpenStateStore(path, keyPath string) (*StateStore, error) {
	if path == "" || keyPath == "" {
		return nil, errors.New("state file and independent key file are both required")
	}
	aead, err := readStateKey(keyPath)
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
	return openStateStore(&fileState{path: path, lock: lock, aead: aead})
}

// readStateKey reads the independent 32-byte state key.
func readStateKey(keyPath string) (cipher.AEAD, error) {
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
	return cipher.NewGCM(block)
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
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failure != nil || s.closed {
		return errors.New("MCP state unavailable; restart after restoring storage")
	}
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	changed := []string{namespace}
	if value == nil {
		delete(s.records, namespace)
	} else {
		s.records[namespace] = data
	}
	if snapshot, ok := value.(oauthSnapshot); ok && namespace == "oauth" {
		for key := range s.records {
			if strings.HasPrefix(key, "plans/") || strings.HasPrefix(key, "environment-plans/") {
				grantID := strings.TrimPrefix(strings.TrimPrefix(key, "environment-plans/"), "plans/")
				if _, active := snapshot.Grants[grantID]; !active {
					delete(s.records, key)
					changed = append(changed, key)
				}
			}
		}
	}
	if err := s.persist.commit(s.records, changed); err != nil {
		s.failure = err
		return errors.New("MCP state could not be committed; further access disabled")
	}
	return nil
}

// fail disables further access, as a failed commit does.
func (s *StateStore) fail(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failure == nil {
		s.failure = err
	}
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
	return s.persist.close()
}

// fileState seals every record into one private file, replaced atomically.
type fileState struct {
	path string
	lock *flock.Flock
	aead cipher.AEAD
}

func (f *fileState) load() (map[string]json.RawMessage, error) {
	data, err := readPrivateFile(f.path, maxStateBytes)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read MCP state: %w", err)
	}
	n := f.aead.NonceSize()
	if len(data) < n {
		return nil, errors.New("invalid encrypted MCP state")
	}
	plain, err := f.aead.Open(nil, data[:n], data[n:], []byte(stateFormat))
	if err != nil {
		return nil, errors.New("MCP state authentication failed; use the original key and unmodified state")
	}
	var records map[string]json.RawMessage
	if err := json.Unmarshal(plain, &records); err != nil || records == nil {
		return nil, errors.New("invalid MCP state records")
	}
	return records, nil
}

func (f *fileState) commit(records map[string]json.RawMessage, _ []string) error {
	plain, err := json.Marshal(records)
	if err != nil {
		return err
	}
	nonce := make([]byte, f.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	data := f.aead.Seal(nonce, nonce, plain, []byte(stateFormat))
	if len(data) > maxStateBytes {
		return errors.New("MCP state capacity reached")
	}
	temp, err := os.CreateTemp(filepath.Dir(f.path), ".mcp-state-*")
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
	if err := os.Rename(temp.Name(), f.path); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(f.path)) //nolint:forbidigo // private operator-owned directory validated during OpenStateStore; opened only for fsync
	if err != nil {
		return err
	}
	defer func() { _ = dir.Close() }()
	return dir.Sync()
}

func (f *fileState) close() error {
	return f.lock.Unlock()
}
