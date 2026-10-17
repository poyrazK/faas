package objectnode

import (
	"context"
	"encoding/hex"
	"errors"
	"os"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type ScrubFinding struct {
	Version         int         `json:"version"`
	ID              string      `json:"id"`
	Target          ScrubTarget `json:"target"`
	State           string      `json:"state"`
	Reason          string      `json:"reason"`
	FirstDetectedAt time.Time   `json:"first_detected_at"`
	LastDetectedAt  time.Time   `json:"last_detected_at"`
	LastVerifiedAt  time.Time   `json:"last_verified_at"`
	ObservedSHA256  string      `json:"observed_sha256"`
	RunID           string      `json:"run_id"`
}

type scrubFindingRecord struct {
	ScrubFinding
	Seal string
}
type ScrubFindingView struct {
	ScrubFinding
	Current      bool `json:"current"`
	CurrentKnown bool `json:"current_known"`
}
type ScrubFindingsPage struct {
	Version    int                `json:"version"`
	Items      []ScrubFindingView `json:"items"`
	NextCursor string             `json:"next_cursor"`
}

func validScrubFindingID(id string) bool {
	data, err := hex.DecodeString(id)
	return err == nil && len(data) == 32 && strings.ToLower(id) == id
}

func (s *Store) readScrubFinding(id string) (scrubFindingRecord, error) {
	var f scrubFindingRecord
	if !validScrubFindingID(id) {
		return f, ErrCorrupt
	}
	info, err := os.Lstat(s.scrubPath("findings/" + id + ".json"))
	if err != nil {
		return f, err
	}
	if !info.Mode().IsRegular() {
		return f, ErrCorrupt
	}
	if err := readJSON(s.scrubPath("findings/"+id+".json"), &f); err != nil {
		return f, err
	}
	seal := f.Seal
	f.Seal = ""
	if seal != scrubSeal(f) || f.Version != 1 || f.ID != id || f.ID != scrubSeal(f.Target) || !validScrubTarget(f.Target) || !validReceiptID(f.RunID) || f.FirstDetectedAt.IsZero() || f.LastDetectedAt.Before(f.FirstDetectedAt) || f.State != "detected" && f.State != "verified" {
		return f, ErrCorrupt
	}
	switch f.Reason {
	case "checksum_mismatch", "missing_blob", "size_mismatch", "invalid_file_type":
	default:
		return f, ErrCorrupt
	}
	if f.State == "verified" && f.LastVerifiedAt.IsZero() {
		return f, ErrCorrupt
	}
	return f, nil
}

// Findings are committed before a target's progress can advance. Replaying a
// step updates the same identity and preserves the first detected observation.
func (s *Store) saveScrubFinding(p scrubState, target ScrubTarget, reason, digest string) error {
	id := scrubSeal(target)
	f, err := s.readScrubFinding(id)
	if errors.Is(err, os.ErrNotExist) {
		if reason == "" {
			return nil
		}
		f = scrubFindingRecord{ScrubFinding: ScrubFinding{Version: 1, ID: id, Target: target, FirstDetectedAt: time.Now().UTC()}}
	} else if err != nil {
		return err
	}
	f.RunID = p.RunID
	if reason == "" {
		f.State, f.LastVerifiedAt = "verified", time.Now().UTC()
	} else {
		f.State, f.Reason, f.ObservedSHA256, f.LastDetectedAt = "detected", reason, digest, time.Now().UTC()
	}
	f.Seal = ""
	f.Seal = scrubSeal(f)
	_, err = s.scrubPersist(s.scrubPath("findings/"+id+".json"), f)
	return err
}

func (s *Store) ScrubFindings(ctx context.Context, cursor string, limit int32) (ScrubFindingsPage, error) {
	s.life.RLock()
	defer s.life.RUnlock()
	s.scrubMu.Lock()
	defer s.scrubMu.Unlock()
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return ScrubFindingsPage{}, ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return ScrubFindingsPage{}, err
	}
	if limit < 1 || limit > api.MaxObjectS3ListItems || cursor != "" && !validScrubFindingID(cursor) {
		return ScrubFindingsPage{}, ErrInvalid
	}
	p := ScrubFindingsPage{Version: 1, Items: []ScrubFindingView{}}
	err := visitDirectory(s.scrubPath("findings"), func(e os.DirEntry) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		id := strings.TrimSuffix(e.Name(), ".json")
		if id <= cursor {
			return nil
		}
		f, err := s.readScrubFinding(id)
		if err != nil {
			return err
		}
		current, err := s.scrubTargetCurrent(f.Target)
		p.Items = keepInventory(p.Items, ScrubFindingView{ScrubFinding: f.ScrubFinding, Current: current, CurrentKnown: err == nil}, int(limit)+1, func(v ScrubFindingView) string { return v.ID })
		return nil
	})
	if err != nil {
		return ScrubFindingsPage{}, err
	}
	if len(p.Items) > int(limit) {
		p.Items = p.Items[:limit]
		p.NextCursor = p.Items[limit-1].ID
	}
	return p, nil
}

// Called with the catalog lock. A failed observation belongs to a blob only
// while every captured identity still matches, including its bucket incarnation.
func (s *Store) scrubTargetCurrent(t ScrubTarget) (bool, error) {
	if s.pending != nil {
		return false, ErrCorrupt
	}
	b, err := s.bucket(t.Bucket)
	if errors.Is(err, ErrNoBucket) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if b.Generation != t.Generation {
		return false, nil
	}
	var o Object
	if t.Kind == "object" {
		o, err = readObject(s.objectPath(t.Bucket, t.Key))
		if errors.Is(err, ErrNoObject) {
			return false, nil
		}
	} else {
		u, e := s.upload(t.Bucket, t.Key, t.UploadID)
		if errors.Is(e, ErrNoUpload) || e == nil && u.Status != multipartActive && u.Status != multipartCompleting {
			return false, nil
		}
		if e != nil {
			return false, e
		}
		part, e := s.readPart(t.UploadID, t.PartNumber)
		err, o = e, part.Object
		if errors.Is(err, ErrInvalidPart) {
			return false, nil
		}
	}
	if err != nil {
		return false, err
	}
	if o.ID != t.BlobID {
		return false, nil
	}
	if o.Size != t.Size || o.SHA256 != t.SHA256 || o.Key != t.Key {
		return false, ErrCorrupt
	}
	return true, nil
}
