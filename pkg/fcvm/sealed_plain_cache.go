package fcvm

// Node-local plaintext of sealed snapshots (ADR-20261110210000 slice 3d-5).
// A sealed snapshot is only ciphertext in the shared store and in this
// node's read-through cache, so every wake used to decrypt the whole memory
// file. This cache keeps one decrypted copy per capture on the node that
// restores it: the first wake decrypts (in parallel, crashcrypt v2), later
// wakes hardlink the entry exactly like an unsealed snapshot's cache file.
//
// Plaintext is only ever on a node that restored the app, i.e. that held its
// key from secretd, and only in a root-only directory no jail sees. Entries
// expire after MaxAge unused and are evicted oldest-first over Budget.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

// DefaultSealedPlainMaxAge is how long an unused entry is kept.
const DefaultSealedPlainMaxAge = 24 * time.Hour

const sealedPlainTmpPrefix = ".fill-"

// SealedPlainCache holds decrypted sealed-snapshot files on this node.
type SealedPlainCache struct {
	dir    string
	budget int64
	maxAge time.Duration
	now    func() time.Time

	mu       sync.Mutex
	inflight map[string]*sealedPlainFill
}

type sealedPlainFill struct {
	done chan struct{}
	err  error
}

// NewSealedPlainCache opens dir (created 0700) as the cache, holding at most
// budget bytes on disk. maxAge 0 means DefaultSealedPlainMaxAge. Leftover
// partial fills from a crash are removed.
func NewSealedPlainCache(dir string, budget int64, maxAge time.Duration) (*SealedPlainCache, error) {
	if dir == "" || !filepath.IsAbs(dir) {
		return nil, fmt.Errorf("fcvm: sealed plaintext cache dir %q must be absolute", dir)
	}
	if budget <= 0 {
		return nil, errors.New("fcvm: sealed plaintext cache needs a positive budget")
	}
	if maxAge <= 0 {
		maxAge = DefaultSealedPlainMaxAge
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("fcvm: sealed plaintext cache: %w", err)
	}
	// The directory is the only guard: staged files are linked world-readable
	// into a chroot, and every link shares the entry's inode.
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, fmt.Errorf("fcvm: sealed plaintext cache: %w", err)
	}
	c := &SealedPlainCache{dir: dir, budget: budget, maxAge: maxAge, now: time.Now, inflight: map[string]*sealedPlainFill{}}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("fcvm: sealed plaintext cache: %w", err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), sealedPlainTmpPrefix) {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
	return c, nil
}

// sealedPlainName names a capture's object: the capture key (its identity's
// recipient, unique per capture) and the storage key, so a re-capture under
// the same storage key never serves stale plaintext.
func sealedPlainName(captureKey, storageKey string) string {
	sum := sha256.Sum256([]byte(captureKey + "\x00" + storageKey))
	return hex.EncodeToString(sum[:])
}

// Ensure returns the entry for (captureKey, storageKey), calling fill to
// decrypt it into a fresh file when it is missing. Concurrent callers for
// one entry share a single fill.
func (c *SealedPlainCache) Ensure(ctx context.Context, captureKey, storageKey string, fill func(context.Context, *os.File) error) (string, error) {
	if captureKey == "" || storageKey == "" {
		return "", errors.New("fcvm: sealed plaintext cache needs a capture and a storage key")
	}
	name := sealedPlainName(captureKey, storageKey)
	path := filepath.Join(c.dir, name)
	for {
		if info, err := os.Lstat(path); err == nil && info.Mode().IsRegular() {
			now := c.now()
			_ = os.Chtimes(path, now, now)
			return path, nil
		}
		c.mu.Lock()
		if f, ok := c.inflight[name]; ok {
			c.mu.Unlock()
			select {
			case <-f.done:
			case <-ctx.Done():
				return "", ctx.Err()
			}
			if f.err != nil {
				return "", f.err
			}
			continue
		}
		// A fill can finish between the Lstat above and taking the lock. It
		// renames its entry into place before it leaves inflight, so look
		// again under the lock instead of decrypting the capture twice.
		if info, err := os.Lstat(path); err == nil && info.Mode().IsRegular() {
			c.mu.Unlock()
			continue
		}
		f := &sealedPlainFill{done: make(chan struct{})}
		c.inflight[name] = f
		c.mu.Unlock()
		f.err = c.fill(ctx, path, fill)
		c.mu.Lock()
		delete(c.inflight, name)
		c.mu.Unlock()
		close(f.done)
		if f.err != nil {
			return "", f.err
		}
		c.evict(name)
		return path, nil
	}
}

func (c *SealedPlainCache) fill(ctx context.Context, path string, fill func(context.Context, *os.File) error) error {
	tmp, err := os.CreateTemp(c.dir, sealedPlainTmpPrefix+"*")
	if err != nil {
		return fmt.Errorf("fcvm: sealed plaintext cache: %w", err)
	}
	tmpPath := tmp.Name()
	err = fill(ctx, tmp)
	if err == nil {
		// Written once, read by every later wake: flush now so a wake's
		// staging (which fsyncs its source) never pays for these pages.
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmpPath, path)
	}
	if err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}

// evict removes entries unused for maxAge, then the oldest until the cache
// fits its budget. keep (the entry just filled) is never removed here. A
// removed entry stays readable through any link a running VM holds.
func (c *SealedPlainCache) evict(keep string) {
	entries, err := os.ReadDir(c.dir)
	if err != nil {
		return
	}
	type entry struct {
		name  string
		mtime time.Time
		size  int64
	}
	var all []entry
	var total int64
	cutoff := c.now().Add(-c.maxAge)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), sealedPlainTmpPrefix) {
			continue
		}
		info, err := e.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		if e.Name() != keep && info.ModTime().Before(cutoff) {
			_ = os.Remove(filepath.Join(c.dir, e.Name()))
			continue
		}
		size := allocatedBytes(info)
		total += size
		all = append(all, entry{name: e.Name(), mtime: info.ModTime(), size: size})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].mtime.Before(all[j].mtime) })
	for _, e := range all {
		if total <= c.budget {
			return
		}
		if e.name == keep {
			continue
		}
		if err := os.Remove(filepath.Join(c.dir, e.name)); err == nil || errors.Is(err, fs.ErrNotExist) {
			total -= e.size
		}
	}
}

// allocatedBytes is the disk an entry uses: decrypted memory is sparse.
func allocatedBytes(info fs.FileInfo) int64 {
	if st, ok := info.Sys().(*syscall.Stat_t); ok && st.Blocks >= 0 {
		return st.Blocks * 512
	}
	return info.Size()
}
