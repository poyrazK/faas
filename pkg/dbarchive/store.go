package dbarchive

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
)

// Store holds archived WAL per database.
type Store interface {
	// PutWAL stores exactly size bytes from r as the database's WAL file
	// name, durably before it returns. An identical existing file is
	// success (PostgreSQL may re-archive after a crash); a different one is
	// ErrConflict.
	PutWAL(ctx context.Context, databaseID, name string, r io.Reader, size int64) error
	// OpenWAL returns a WAL file and its size, or ErrNotFound.
	OpenWAL(ctx context.Context, databaseID, name string) (io.ReadCloser, int64, error)
}

// LocalStore keeps the archive on the node's disk:
// <dir>/<database-id>/wal/<name>.
type LocalStore struct {
	dir string
	// maint serializes passes that walk the whole store: uploads, pruning
	// and drops, so a drop cannot race an upload of the same database.
	maint     sync.Mutex
	windowsMu sync.Mutex
	windows   map[string]int64
	// offsite journals deletions, holds and handoffs for an off-site
	// mirror (offsite.go); offsiteLast is the last entry's sequence.
	offsite     atomic.Bool
	offsiteLast atomic.Int64
}

// NewLocalStore creates dir if needed and clears leftovers of remote reads.
func NewLocalStore(dir string) (*LocalStore, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("dbarchive: store dir: %w", err)
	}
	s := &LocalStore{dir: dir}
	if err := os.RemoveAll(s.fetchDir()); err != nil {
		return nil, fmt.Errorf("dbarchive: clear fetch dir: %w", err)
	}
	if err := os.MkdirAll(s.fetchDir(), 0o700); err != nil {
		return nil, fmt.Errorf("dbarchive: fetch dir: %w", err)
	}
	return s, nil
}

// fetchDir holds remote reads in flight; it is not a database id, so
// uploads and pruning never walk it.
func (s *LocalStore) fetchDir() string { return filepath.Join(s.dir, ".fetch") }

func (s *LocalStore) walPath(databaseID, name string) (string, error) {
	if !ValidDatabaseID(databaseID) || !ValidWALName(name) {
		return "", fmt.Errorf("dbarchive: invalid wal reference %q/%q", databaseID, name)
	}
	return filepath.Join(s.dir, databaseID, "wal", name), nil
}

func (s *LocalStore) PutWAL(_ context.Context, databaseID, name string, r io.Reader, size int64) error {
	path, err := s.walPath(databaseID, name)
	if err != nil {
		return err
	}
	if size <= 0 || size > MaxWALBytes {
		return fmt.Errorf("dbarchive: wal size %d out of range", size)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+name+".*.tmp")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	h := sha256.New()
	n, err := io.CopyN(io.MultiWriter(tmp, h), r, size)
	if err == nil && n != size {
		err = io.ErrUnexpectedEOF
	}
	if err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return fmt.Errorf("dbarchive: receive %s: %w", name, err)
	}
	if existing, err := fileSHA256(path); err == nil {
		if !bytes.Equal(existing, h.Sum(nil)) {
			return ErrConflict
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	// Link instead of rename: it fails rather than replaces when another
	// put of the same name won the race.
	if err := os.Link(tmp.Name(), path); err != nil {
		if errors.Is(err, os.ErrExist) {
			existing, hashErr := fileSHA256(path)
			if hashErr == nil && bytes.Equal(existing, h.Sum(nil)) {
				return nil
			}
			return ErrConflict
		}
		return err
	}
	return syncDir(dir)
}

func (s *LocalStore) OpenWAL(_ context.Context, databaseID, name string) (io.ReadCloser, int64, error) {
	path, err := s.walPath(databaseID, name)
	if err != nil {
		return nil, 0, err
	}
	f, err := os.Open(path) //nolint:gosec,forbidigo // validated database id and WAL name under the store root
	if errors.Is(err, os.ErrNotExist) {
		return nil, 0, ErrNotFound
	}
	if err != nil {
		return nil, 0, err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, 0, err
	}
	return f, info.Size(), nil
}

func fileSHA256(path string) ([]byte, error) {
	f, err := os.Open(path) //nolint:gosec,forbidigo // a path built by walPath
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return nil, err
	}
	return h.Sum(nil), nil
}

func syncDir(dir string) error {
	d, err := os.Open(dir) //nolint:gosec,forbidigo // the store's own directory
	if err != nil {
		return err
	}
	defer func() { _ = d.Close() }()
	return d.Sync()
}
