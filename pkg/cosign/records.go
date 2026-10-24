package cosign

import (
	"crypto/ecdsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"syscall"
	"time"
)

// DefaultAttestationRecordsPath is where schedd remembers the layers it has
// verified. Its directory is created 0700 for faas-schedd by the unit.
const DefaultAttestationRecordsPath = "/var/lib/faas/schedd/layer-attestations.json"

// attestationRecords remembers layers this host has verified, so a schedd
// restart does not re-read and re-hash every layer (ADR-038 amendment,
// 2026-10-24). A record pins the exact file that was hashed: the node cache
// writes a layer once and replaces it only by renaming a new file over it, so
// the same device, inode and size mean the same bytes. Any other file, an
// unknown signing key or an expired record falls back to a full verification.
//
// The records are trusted input that skips a hash, so they are honoured only
// from a regular file the daemon owns and nobody else can read or write.
type attestationRecords struct {
	path   string
	keyID  string
	maxAge time.Duration
	now    func() time.Time

	mu    sync.Mutex
	byKey map[string]attestationRecord
}

type attestationRecord struct {
	LayerKey   string    `json:"layer_key"`
	SigKey     string    `json:"sig_key"`
	KeyID      string    `json:"key_id"`
	Device     uint64    `json:"device"`
	Inode      uint64    `json:"inode"`
	Size       int64     `json:"size"`
	VerifiedAt time.Time `json:"verified_at"`
}

// fileIdentity is the file a layer was hashed from.
type fileIdentity struct {
	device, inode uint64
	size          int64
}

func identityOf(fi os.FileInfo) (fileIdentity, bool) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok || !fi.Mode().IsRegular() {
		return fileIdentity{}, false
	}
	return fileIdentity{device: uint64(st.Dev), inode: st.Ino, size: fi.Size()}, true
}

// publicKeyID fingerprints the verifying key; records made under another key
// are never honoured.
func publicKeyID(pub *ecdsa.PublicKey) (string, error) {
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return "", fmt.Errorf("cosign: fingerprint public key: %w", err)
	}
	sum := sha256.Sum256(der)
	return hex.EncodeToString(sum[:]), nil
}

// openAttestationRecords loads path, keeping records made under keyID that
// are younger than maxAge. A missing file is an empty set; an unreadable or
// malformed one is too, and the next verification rewrites it. A file that is
// not private to this process is refused.
func openAttestationRecords(path, keyID string, maxAge time.Duration, now func() time.Time) (*attestationRecords, error) {
	r := &attestationRecords{path: path, keyID: keyID, maxAge: maxAge, now: now, byKey: map[string]attestationRecord{}}
	fi, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return r, nil
	}
	if err != nil {
		return nil, fmt.Errorf("cosign: attestation records %s: %w", path, err)
	}
	if err := requirePrivateFile(path, fi); err != nil {
		return nil, err
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("cosign: read attestation records %s: %w", path, err)
	}
	var records []attestationRecord
	if err := json.Unmarshal(body, &records); err != nil {
		records = nil // malformed: start empty; the next verification rewrites it
	}
	for _, rec := range records {
		if r.fresh(rec) {
			r.byKey[recordKey(rec.LayerKey, rec.SigKey)] = rec
		}
	}
	return r, nil
}

func requirePrivateFile(path string, fi os.FileInfo) error {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !fi.Mode().IsRegular() || fi.Mode().Perm()&0o077 != 0 || !ok || int(st.Uid) != os.Getuid() {
		return fmt.Errorf("cosign: attestation records %s must be a regular file owned by uid %d with mode 0600 (got %v)", path, os.Getuid(), fi.Mode())
	}
	return nil
}

func recordKey(layerKey, sigKey string) string { return layerKey + "\x00" + sigKey }

func (r *attestationRecords) fresh(rec attestationRecord) bool {
	return rec.KeyID == r.keyID && r.now().Sub(rec.VerifiedAt) < r.maxAge
}

// matches reports whether id is the file a still-valid record verified for
// this layer and signature.
func (r *attestationRecords) matches(layerKey, sigKey string, id fileIdentity) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec, ok := r.byKey[recordKey(layerKey, sigKey)]
	return ok && r.fresh(rec) && rec.Device == id.device && rec.Inode == id.inode && rec.Size == id.size
}

// remember records that id verified for this layer and signature, and
// rewrites the file atomically without the expired records.
func (r *attestationRecords) remember(layerKey, sigKey string, id fileIdentity) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.byKey[recordKey(layerKey, sigKey)] = attestationRecord{
		LayerKey: layerKey, SigKey: sigKey, KeyID: r.keyID,
		Device: id.device, Inode: id.inode, Size: id.size, VerifiedAt: r.now().UTC(),
	}
	records := make([]attestationRecord, 0, len(r.byKey))
	for key, rec := range r.byKey {
		if !r.fresh(rec) {
			delete(r.byKey, key)
			continue
		}
		records = append(records, rec)
	}
	sort.Slice(records, func(i, j int) bool {
		return recordKey(records[i].LayerKey, records[i].SigKey) < recordKey(records[j].LayerKey, records[j].SigKey)
	})
	body, err := json.Marshal(records)
	if err != nil {
		return fmt.Errorf("cosign: encode attestation records: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(r.path), ".layer-attestations-*")
	if err != nil {
		return fmt.Errorf("cosign: write attestation records: %w", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("cosign: write attestation records: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("cosign: write attestation records: %w", err)
	}
	if err := os.Rename(tmp.Name(), r.path); err != nil {
		return fmt.Errorf("cosign: write attestation records: %w", err)
	}
	return nil
}
