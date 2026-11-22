// Package volbackup backs up persistent sandbox sessions' workspace volumes
// to object storage and restores them (ADR-20261122132055 §4).
//
// A volume is cut into fixed chunks. Each chunk that holds data is stored
// once per account, zstd-compressed, under its SHA-256; a chunk of zeros is
// not stored. A backup is a manifest listing its chunks' hashes in order, so
// a backup is complete on its own (a restore reads one manifest, never a
// chain), unchanged chunks cost nothing to back up again, and the forks of a
// session share the chunks they have in common.
//
// A backup reads only the chunks a list of changed ranges touches and takes
// the others' hashes from the previous manifest; the node derives the ranges
// from reflink clones of the volume (storage.ChangedRanges).
package volbackup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/klauspost/compress/zstd"

	"github.com/onebox-faas/faas/pkg/storage"
)

// ChunkBytes is a chunk's size: large enough that a 10 GB volume's manifest
// stays small, small enough that a scattered write uploads little.
const ChunkBytes = 4 << 20

// manifestVersion is the manifest format.
const manifestVersion = 1

// maxManifestBytes bounds a manifest read: 1 TB of 4 MiB chunks, each a
// 64-character hash in JSON.
const maxManifestBytes = 32 << 20

// Manifest is one backup of a volume.
type Manifest struct {
	Version    int       `json:"version"`
	ID         string    `json:"id"`
	SizeBytes  int64     `json:"size_bytes"`
	ChunkBytes int64     `json:"chunk_bytes"`
	Chunks     []string  `json:"chunks"` // SHA-256 hex per chunk; "" is a chunk of zeros
	CreatedAt  time.Time `json:"created_at"`
}

// Stats are what a backup read and wrote.
type Stats struct {
	ChunksRead     int   // chunks read from the volume
	ChunksUploaded int   // chunks the store did not have yet
	BytesUploaded  int64 // compressed bytes put, chunks and manifest
}

// ChunkKey is a chunk's object key, shared by an account's volumes.
func ChunkKey(accountID, hash string) string {
	return "sandbox-volumes/" + accountID + "/chunks/" + hash
}

// ManifestKey is a backup's manifest key.
func ManifestKey(accountID, sandboxID, backupID string) string {
	return "sandbox-volumes/" + accountID + "/" + sandboxID + "/backups/" + backupID + ".json"
}

// Backup reads the volume at path and stores backup id. prev is the last
// backup of the same volume, or nil; changed lists the byte ranges that may
// differ from it, or with a nil prev the ranges that hold data (the rest are
// holes). A nil changed reads every chunk.
func Backup(ctx context.Context, store storage.StorageBackend, accountID, sandboxID, id, path string, prev *Manifest, changed []storage.Range) (Manifest, Stats, error) {
	if err := checkIDs(accountID, sandboxID, id); err != nil {
		return Manifest{}, Stats{}, err
	}
	f, err := os.Open(path) //nolint:forbidigo // a node-owned volume clone the caller resolved.
	if err != nil {
		return Manifest{}, Stats{}, fmt.Errorf("volbackup: open volume: %w", err)
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return Manifest{}, Stats{}, fmt.Errorf("volbackup: stat volume: %w", err)
	}
	m := Manifest{Version: manifestVersion, ID: id, SizeBytes: info.Size(), ChunkBytes: ChunkBytes, CreatedAt: time.Now().UTC()}
	n := int((info.Size() + ChunkBytes - 1) / ChunkBytes)
	m.Chunks = make([]string, n)
	reuse := prev != nil && changed != nil && prev.ChunkBytes == ChunkBytes
	enc, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedFastest), zstd.WithEncoderConcurrency(1))
	if err != nil {
		return Manifest{}, Stats{}, err
	}
	defer func() { _ = enc.Close() }()
	var st Stats
	buf := make([]byte, ChunkBytes)
	for i := range n {
		if err := ctx.Err(); err != nil {
			return Manifest{}, st, err
		}
		start := int64(i) * ChunkBytes
		end := min(start+ChunkBytes, info.Size())
		// A chunk is the same object when it spans the same bytes in both
		// sizes and nothing in it changed; a grown volume's old short last
		// chunk is read again.
		if reuse && i < len(prev.Chunks) && end == min(start+ChunkBytes, prev.SizeBytes) && !touches(changed, start, end) {
			m.Chunks[i] = prev.Chunks[i]
			continue
		}
		// Without a previous backup, changed is the volume's data: a chunk
		// it does not touch is a hole, all zeros.
		if prev == nil && changed != nil && !touches(changed, start, end) {
			continue
		}
		chunk := buf[:end-start]
		if _, err := f.ReadAt(chunk, start); err != nil && !errors.Is(err, io.EOF) {
			return Manifest{}, st, fmt.Errorf("volbackup: read chunk %d: %w", i, err)
		}
		st.ChunksRead++
		if allZero(chunk) {
			continue
		}
		sum := sha256.Sum256(chunk)
		hash := hex.EncodeToString(sum[:])
		m.Chunks[i] = hash
		key := ChunkKey(accountID, hash)
		if exists, supported, err := storage.Exists(ctx, store, key); err == nil && supported && exists {
			continue
		}
		packed := enc.EncodeAll(chunk, nil)
		if err := store.Put(ctx, key, bytes.NewReader(packed)); err != nil {
			return Manifest{}, st, fmt.Errorf("volbackup: put chunk %d: %w", i, err)
		}
		st.ChunksUploaded++
		st.BytesUploaded += int64(len(packed))
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return Manifest{}, st, err
	}
	if err := store.Put(ctx, ManifestKey(accountID, sandboxID, id), bytes.NewReader(raw)); err != nil {
		return Manifest{}, st, fmt.Errorf("volbackup: put manifest: %w", err)
	}
	st.BytesUploaded += int64(len(raw))
	return m, st, nil
}

// ReadManifest loads backup id of the session's volume.
func ReadManifest(ctx context.Context, store storage.StorageBackend, accountID, sandboxID, id string) (Manifest, error) {
	if err := checkIDs(accountID, sandboxID, id); err != nil {
		return Manifest{}, err
	}
	rc, err := store.Get(ctx, ManifestKey(accountID, sandboxID, id))
	if err != nil {
		return Manifest{}, fmt.Errorf("volbackup: get manifest: %w", err)
	}
	defer func() { _ = rc.Close() }()
	raw, err := io.ReadAll(io.LimitReader(rc, maxManifestBytes+1))
	if err != nil {
		return Manifest{}, fmt.Errorf("volbackup: read manifest: %w", err)
	}
	if len(raw) > maxManifestBytes {
		return Manifest{}, errors.New("volbackup: manifest too large")
	}
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return Manifest{}, fmt.Errorf("volbackup: decode manifest: %w", err)
	}
	if m.Version != manifestVersion || m.ID != id || m.ChunkBytes != ChunkBytes || m.SizeBytes < 0 ||
		int64(len(m.Chunks)) != (m.SizeBytes+ChunkBytes-1)/ChunkBytes {
		return Manifest{}, fmt.Errorf("volbackup: manifest %s is malformed", id)
	}
	return m, nil
}

// Restore writes backup m into a new sparse file at dst, checking each
// chunk's hash, and only then gives it dst's name. A dst that exists is
// replaced.
func Restore(ctx context.Context, store storage.StorageBackend, accountID string, m Manifest, dst string) (err error) {
	if !safeID(accountID) {
		return fmt.Errorf("volbackup: invalid account %q", accountID)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return fmt.Errorf("volbackup: restore dir: %w", err)
	}
	tmp := dst + ".restore"
	_ = os.Remove(tmp)
	f, err := os.OpenFile(tmp, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600) //nolint:forbidigo // a node-owned volume path.
	if err != nil {
		return fmt.Errorf("volbackup: create volume: %w", err)
	}
	defer func() {
		if cerr := f.Close(); cerr != nil && err == nil {
			err = cerr
		}
		if err != nil {
			_ = os.Remove(tmp)
		}
	}()
	if err := f.Truncate(m.SizeBytes); err != nil {
		return fmt.Errorf("volbackup: size volume: %w", err)
	}
	dec, err := zstd.NewReader(nil, zstd.WithDecoderConcurrency(1), zstd.WithDecoderMaxMemory(2*ChunkBytes))
	if err != nil {
		return err
	}
	defer dec.Close()
	for i, hash := range m.Chunks {
		if hash == "" {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		chunk, err := readChunk(ctx, store, dec, accountID, hash)
		if err != nil {
			return fmt.Errorf("volbackup: chunk %d: %w", i, err)
		}
		start := int64(i) * ChunkBytes
		if want := min(int64(ChunkBytes), m.SizeBytes-start); int64(len(chunk)) != want {
			return fmt.Errorf("volbackup: chunk %d is %d bytes, want %d", i, len(chunk), want)
		}
		if _, err := f.WriteAt(chunk, start); err != nil {
			return fmt.Errorf("volbackup: write chunk %d: %w", i, err)
		}
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("volbackup: sync volume: %w", err)
	}
	if err := os.Rename(tmp, dst); err != nil {
		return fmt.Errorf("volbackup: place volume: %w", err)
	}
	return nil
}

func readChunk(ctx context.Context, store storage.StorageBackend, dec *zstd.Decoder, accountID, hash string) ([]byte, error) {
	rc, err := store.Get(ctx, ChunkKey(accountID, hash))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }()
	packed, err := io.ReadAll(io.LimitReader(rc, 2*ChunkBytes))
	if err != nil {
		return nil, err
	}
	chunk, err := dec.DecodeAll(packed, make([]byte, 0, ChunkBytes))
	if err != nil {
		return nil, err
	}
	if sum := sha256.Sum256(chunk); hex.EncodeToString(sum[:]) != hash {
		return nil, errors.New("hash mismatch")
	}
	return chunk, nil
}

// ChunkSet lists the distinct chunk hashes m refers to.
func (m Manifest) ChunkSet() map[string]bool {
	out := make(map[string]bool, len(m.Chunks))
	for _, h := range m.Chunks {
		if h != "" {
			out[h] = true
		}
	}
	return out
}

func touches(changed []storage.Range, start, end int64) bool {
	for _, r := range changed {
		if r.Start < end && start < r.End {
			return true
		}
	}
	return false
}

func allZero(b []byte) bool {
	for len(b) >= 8 {
		if b[0]|b[1]|b[2]|b[3]|b[4]|b[5]|b[6]|b[7] != 0 {
			return false
		}
		b = b[8:]
	}
	for _, c := range b {
		if c != 0 {
			return false
		}
	}
	return true
}

func checkIDs(ids ...string) error {
	for _, id := range ids {
		if !safeID(id) {
			return fmt.Errorf("volbackup: invalid id %q", id)
		}
	}
	return nil
}

func safeID(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for _, r := range s {
		if ok := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-'; !ok {
			return false
		}
	}
	return true
}
