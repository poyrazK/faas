package objectnode

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// StartScrub snapshots catalog identities into a bounded, fsynced worklist.
// File contents are verified later without holding the catalog lock.
func (s *Store) StartScrub(ctx context.Context) (ScrubStatus, error) {
	s.life.RLock()
	defer s.life.RUnlock()
	if !s.scrubMu.TryLock() {
		return ScrubStatus{}, ErrScrubBusy
	}
	defer s.scrubMu.Unlock()
	if s.closed {
		return ScrubStatus{}, ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return ScrubStatus{}, err
	}
	previous, err := s.readScrubState()
	if err != nil {
		return ScrubStatus{}, err
	}
	if previous.State == "running" {
		return previous.ScrubStatus, ErrScrubBusy
	}
	available, err := s.AvailableBytes()
	if err != nil {
		return ScrubStatus{}, err
	}
	if available < uint64(api.ObjectNodeScrubManifestMaxBytes+api.ObjectUploadSpoolMinFreeBytes) {
		return ScrubStatus{}, ErrCapacity
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err = s.servingError(); err != nil {
		return ScrubStatus{}, err
	}
	if s.pending != nil {
		return ScrubStatus{}, ErrCorrupt
	}
	p := scrubState{ScrubStatus: ScrubStatus{Version: 1, State: "running", RunID: uuid.NewString(), Snapshot: s.inventorySnapshot(), StartedAt: time.Now().UTC(), LastCompletedAt: previous.LastCompletedAt, LastVerifiedAt: previous.LastVerifiedAt}}
	if err = s.buildScrubManifest(ctx, &p); err != nil {
		return ScrubStatus{}, err
	}
	if err = ctx.Err(); err != nil {
		return ScrubStatus{}, err
	}
	if err = s.saveScrubState(&p); err != nil {
		return ScrubStatus{}, err
	}
	return p.ScrubStatus, nil
}

func (s *Store) buildScrubManifest(ctx context.Context, p *scrubState) error {
	path := s.scrubPath(".tmp-" + p.RunID)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close(); _ = os.Remove(path) }()
	h := sha256.New()
	w := bufio.NewWriterSize(io.MultiWriter(f, h), api.ObjectNodeScrubBufferBytes)
	appendTarget := func(target ScrubTarget) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !validScrubTarget(target) {
			return ErrCorrupt
		}
		line, err := json.Marshal(target)
		if err != nil || len(line)+1 > api.ObjectNodeScrubRecordMaxBytes || int64(len(line)+1) > api.ObjectNodeScrubManifestMaxBytes-p.ManifestBytes {
			return ErrCapacity
		}
		if _, err = w.Write(append(line, '\n')); err != nil {
			return err
		}
		p.ManifestBytes += int64(len(line) + 1)
		p.Targets++
		return nil
	}
	if err = s.scrubObjectTargets(ctx, appendTarget); err != nil {
		return err
	}
	if err = s.scrubPartTargets(ctx, appendTarget); err != nil {
		return err
	}
	if err = w.Flush(); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	p.ManifestSHA256 = hex.EncodeToString(h.Sum(nil))
	if err = os.Rename(path, s.scrubPath(p.RunID+".jsonl")); err != nil {
		return err
	}
	return syncDirectory(s.scrubPath(""))
}

func (s *Store) scrubObjectTargets(ctx context.Context, appendTarget func(ScrubTarget) error) error {
	return visitDirectory(filepath.Join(s.root, "buckets"), func(e os.DirEntry) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		var b bucketRecord
		dir := filepath.Join(s.root, "buckets", e.Name())
		if !e.IsDir() || readJSON(filepath.Join(dir, "bucket.json"), &b) != nil || nameHash(b.Name) != e.Name() {
			return ErrCorrupt
		}
		if _, err := s.bucket(b.Name); err != nil {
			return err
		}
		return visitDirectory(filepath.Join(dir, "objects"), func(entry os.DirEntry) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if !entry.IsDir() {
				return ErrCorrupt
			}
			o, err := readObject(filepath.Join(dir, "objects", entry.Name()))
			if errors.Is(err, ErrNoObject) {
				return nil
			}
			if err != nil {
				return err
			}
			return appendTarget(ScrubTarget{Kind: "object", Bucket: b.Name, Generation: b.Generation, Key: o.Key, BlobID: o.ID, Size: o.Size, SHA256: o.SHA256})
		})
	})
}

func (s *Store) scrubPartTargets(ctx context.Context, appendTarget func(ScrubTarget) error) error {
	return visitDirectory(filepath.Join(s.root, "multipart"), func(e os.DirEntry) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !e.IsDir() {
			return ErrCorrupt
		}
		u, err := s.readMultipart(e.Name())
		if errors.Is(err, ErrNoUpload) {
			return nil
		}
		if err != nil {
			return err
		}
		if u.Status != multipartActive && u.Status != multipartCompleting {
			return nil
		}
		b, err := s.bucket(u.Bucket)
		if err != nil || b.Generation != u.Generation {
			return ErrCorrupt
		}
		return visitDirectory(filepath.Join(s.multipartPath(u.ID), "parts"), func(part os.DirEntry) error {
			n, err := strconv.ParseInt(part.Name(), 10, 32)
			if err != nil || n < 1 || n > api.MaxMultipartParts || strconv.FormatInt(n, 10) != part.Name() || !part.IsDir() {
				return ErrCorrupt
			}
			p, err := s.readPart(u.ID, int32(n))
			if errors.Is(err, ErrInvalidPart) {
				return nil
			}
			if err != nil || p.Object.Key != u.Key {
				return ErrCorrupt
			}
			return appendTarget(ScrubTarget{Kind: "part", Bucket: u.Bucket, Generation: u.Generation, Key: u.Key, UploadID: u.ID, PartNumber: int32(n), BlobID: p.Object.ID, Size: p.Object.Size, SHA256: p.Object.SHA256})
		})
	})
}
