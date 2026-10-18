package objectnode

import (
	"crypto/sha256"
	"encoding"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// ScrubTarget is an exact immutable identity. It never means the current value
// of a key without the captured bucket incarnation, blob ID and expected digest.
type ScrubTarget struct {
	Kind       string `json:"kind"`
	Bucket     string `json:"bucket"`
	Generation string `json:"generation"`
	Key        string `json:"key"`
	BlobID     string `json:"blob_id"`
	Size       int64  `json:"size"`
	SHA256     string `json:"sha256"`
	UploadID   string `json:"upload_id,omitempty"`
	PartNumber int32  `json:"part_number,omitempty"`
}

type ScrubStatus struct {
	Version         int       `json:"version"`
	State           string    `json:"state"`
	RunID           string    `json:"run_id"`
	Snapshot        string    `json:"snapshot"`
	StartedAt       time.Time `json:"started_at"`
	CompletedAt     time.Time `json:"completed_at"`
	LastCompletedAt time.Time `json:"last_completed_at"`
	LastVerifiedAt  time.Time `json:"last_verified_at"`
	UpdatedAt       time.Time `json:"updated_at"`
	Targets         int64     `json:"targets"`
	Processed       int64     `json:"processed"`
	Verified        int64     `json:"verified"`
	Failures        int64     `json:"failures"`
	Stale           int64     `json:"stale"`
	BytesRead       int64     `json:"bytes_read"`
	CurrentBlobID   string    `json:"current_blob_id"`
	CurrentOffset   int64     `json:"current_offset"`
}

type scrubFingerprint struct {
	Device, Inode           uint64
	Size, Modified, Changed int64
}

type scrubCurrent struct {
	Target ScrubTarget
	Offset int64
	Hash   []byte
	File   scrubFingerprint
}

type scrubState struct {
	ScrubStatus
	ManifestBytes  int64
	ManifestSHA256 string
	ManifestHash   []byte
	NextOffset     int64
	Current        *scrubCurrent
	Seal           string
}

func (s *Store) scrubPath(name string) string { return filepath.Join(s.root, "scrub", name) }

func scrubSeal(value any) string {
	body, _ := json.Marshal(value)
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func validScrubTarget(t ScrubTarget) bool {
	sha, err := hex.DecodeString(t.SHA256)
	if !ValidBucket(t.Bucket) || !validReceiptID(t.Generation) || !validKey(t.Key) || !validReceiptID(t.BlobID) || t.Size < 0 || t.Size > api.MaxObjectUploadBytes || err != nil || len(sha) != sha256.Size {
		return false
	}
	return t.Kind == "object" && t.UploadID == "" && t.PartNumber == 0 || t.Kind == "part" && validReceiptID(t.UploadID) && t.PartNumber >= 1 && t.PartNumber <= api.MaxMultipartParts && t.Size <= api.MaxObjectSinglePutBytes
}

func (s *Store) readScrubState() (scrubState, error) {
	var p scrubState
	info, statErr := os.Lstat(s.scrubPath("progress.json"))
	if statErr != nil && !errors.Is(statErr, os.ErrNotExist) || statErr == nil && !info.Mode().IsRegular() {
		return p, ErrCorrupt
	}
	err := readJSON(s.scrubPath("progress.json"), &p)
	if errors.Is(err, os.ErrNotExist) {
		return scrubState{ScrubStatus: ScrubStatus{Version: 1, State: "idle"}}, nil
	}
	if err != nil {
		return p, err
	}
	seal := p.Seal
	p.Seal = ""
	if seal != scrubSeal(p) || p.Version != 1 || !validReceiptID(p.RunID) || p.StartedAt.IsZero() || p.UpdatedAt.Before(p.StartedAt) || p.Targets < 0 || p.Processed < 0 || p.Processed > p.Targets || p.Verified < 0 || p.Failures < 0 || p.Stale < 0 || p.Processed != p.Verified+p.Failures+p.Stale || p.BytesRead < 0 || p.NextOffset < 0 || p.NextOffset > p.ManifestBytes || p.ManifestBytes < 0 || p.ManifestBytes > api.ObjectNodeScrubManifestMaxBytes || len(p.ManifestSHA256) != 64 || p.State != "running" && p.State != "complete" {
		return p, ErrCorrupt
	}
	if p.State == "complete" && (p.Current != nil || p.Processed != p.Targets || p.NextOffset != p.ManifestBytes || p.CompletedAt.IsZero()) {
		return p, ErrCorrupt
	}
	if c := p.Current; c != nil {
		if !validScrubTarget(c.Target) || c.Offset < 0 || c.Offset > c.Target.Size || p.Processed >= p.Targets {
			return p, ErrCorrupt
		}
		if c.Offset != 0 {
			h := sha256.New()
			if h.(encoding.BinaryUnmarshaler).UnmarshalBinary(c.Hash) != nil {
				return p, ErrCorrupt
			}
		}
	}
	if p.NextOffset != 0 {
		if sha256.New().(encoding.BinaryUnmarshaler).UnmarshalBinary(p.ManifestHash) != nil {
			return p, ErrCorrupt
		}
	}
	return p, nil
}

func (s *Store) saveScrubState(p *scrubState) error {
	p.CurrentBlobID, p.CurrentOffset = "", 0
	if p.Current != nil {
		p.CurrentBlobID, p.CurrentOffset = p.Current.Target.BlobID, p.Current.Offset
	}
	p.UpdatedAt = time.Now().UTC()
	p.Seal = ""
	p.Seal = scrubSeal(*p)
	_, err := s.scrubPersist(s.scrubPath("progress.json"), *p)
	return err
}

func (s *Store) ScrubStatus() (ScrubStatus, error) {
	s.life.RLock()
	defer s.life.RUnlock()
	s.scrubMu.Lock()
	defer s.scrubMu.Unlock()
	if s.closed {
		return ScrubStatus{}, ErrClosed
	}
	p, err := s.readScrubState()
	return p.ScrubStatus, err
}
