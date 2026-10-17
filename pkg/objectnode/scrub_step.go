package objectnode

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding"
	"encoding/hex"
	"encoding/json"
	"errors"
	"hash"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"golang.org/x/sys/unix"
)

// ScrubStep performs at most budget blob bytes and a bounded target count.
// Checkpoints do not mutate the object catalog or its inventory revision.
func (s *Store) ScrubStep(ctx context.Context, budget int64) (ScrubStatus, error) {
	s.life.RLock()
	defer s.life.RUnlock()
	if !s.scrubMu.TryLock() {
		return ScrubStatus{}, ErrScrubBusy
	}
	defer s.scrubMu.Unlock()
	if s.closed {
		return ScrubStatus{}, ErrClosed
	}
	s.mu.RLock()
	serveErr := s.servingError()
	s.mu.RUnlock()
	if serveErr != nil {
		return ScrubStatus{}, serveErr
	}
	if budget < 1 || budget > api.ObjectNodeScrubMaxBytesPerStep {
		return ScrubStatus{}, ErrInvalid
	}
	p, err := s.readScrubState()
	if err != nil || p.State != "running" {
		return p.ScrubStatus, err
	}
	buffer := make([]byte, api.ObjectNodeScrubBufferBytes)
	for i := 0; i < api.ObjectNodeScrubTargetsPerStep && budget > 0 && p.Processed < p.Targets; i++ {
		if err = ctx.Err(); err != nil {
			return p.ScrubStatus, err
		}
		if p.Current == nil {
			if err = s.nextScrubTarget(&p); err != nil {
				return p.ScrubStatus, err
			}
		}
		n, err := s.scrubCurrent(ctx, &p, budget, buffer)
		if err != nil {
			return p.ScrubStatus, err
		}
		budget -= n
		if p.Current != nil {
			break
		}
	}
	if err = ctx.Err(); err != nil {
		return p.ScrubStatus, err
	}
	if p.Processed == p.Targets {
		if err = finishScrubCycle(&p); err != nil {
			return p.ScrubStatus, err
		}
	}
	if err = s.saveScrubState(&p); err != nil {
		return p.ScrubStatus, err
	}
	if p.State == "complete" {
		_ = os.Remove(s.scrubPath(p.RunID + ".jsonl"))
	}
	return p.ScrubStatus, nil
}

func restoreScrubHash(state []byte) (hash.Hash, error) {
	h := sha256.New()
	if len(state) != 0 && h.(encoding.BinaryUnmarshaler).UnmarshalBinary(state) != nil {
		return nil, ErrCorrupt
	}
	return h, nil
}

func (s *Store) nextScrubTarget(p *scrubState) error {
	f, err := os.Open(s.scrubPath(p.RunID + ".jsonl")) //nolint:forbidigo // Validated private immutable worklist identity.
	if err != nil {
		return ErrCorrupt
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != p.ManifestBytes {
		return ErrCorrupt
	}
	if _, err = f.Seek(p.NextOffset, io.SeekStart); err != nil {
		return err
	}
	line, err := bufio.NewReaderSize(f, api.ObjectNodeScrubRecordMaxBytes).ReadSlice('\n')
	if err != nil || len(line) > api.ObjectNodeScrubRecordMaxBytes {
		return ErrCorrupt
	}
	var target ScrubTarget
	if json.Unmarshal(line, &target) != nil || !validScrubTarget(target) {
		return ErrCorrupt
	}
	h, err := restoreScrubHash(p.ManifestHash)
	if err != nil {
		return err
	}
	_, _ = h.Write(line)
	p.ManifestHash, err = h.(encoding.BinaryMarshaler).MarshalBinary()
	if err != nil {
		return err
	}
	p.NextOffset += int64(len(line))
	p.Current = &scrubCurrent{Target: target}
	return nil
}

func finishScrubCycle(p *scrubState) error {
	h, err := restoreScrubHash(p.ManifestHash)
	if err != nil || p.Current != nil || p.NextOffset != p.ManifestBytes || hex.EncodeToString(h.Sum(nil)) != p.ManifestSHA256 {
		return ErrCorrupt
	}
	p.State, p.CompletedAt = "complete", time.Now().UTC()
	p.LastCompletedAt = p.CompletedAt
	if p.Failures == 0 && p.Stale == 0 {
		p.LastVerifiedAt = p.CompletedAt
	}
	return nil
}

func (s *Store) scrubCurrent(ctx context.Context, p *scrubState, budget int64, buffer []byte) (int64, error) {
	c := p.Current
	f, fingerprint, current, reason, err := s.openScrubTarget(c.Target)
	if err != nil {
		return 0, err
	}
	if !current {
		p.Stale++
		p.Processed++
		p.Current = nil
		return 0, nil
	}
	if reason != "" {
		return 0, s.finishScrubTarget(p, reason, "")
	}
	defer func() { _ = f.Close() }()
	if c.File != fingerprint {
		c.Offset, c.Hash, c.File = 0, nil, fingerprint
	}
	h, err := restoreScrubHash(c.Hash)
	if err != nil {
		return 0, err
	}
	if _, err = f.Seek(c.Offset, io.SeekStart); err != nil {
		return 0, err
	}
	take := min(budget, c.Target.Size-c.Offset)
	n, err := io.CopyBuffer(h, io.LimitReader(contextReader{ctx, f}, take), buffer)
	if err != nil {
		return n, err
	}
	c.Offset += n
	p.BytesRead += n
	if n != take {
		return n, s.finishScrubTarget(p, "size_mismatch", "")
	}
	s.mu.RLock()
	current, err = s.scrubTargetCurrent(c.Target)
	s.mu.RUnlock()
	if err != nil {
		return n, err
	}
	if !current {
		p.Stale++
		p.Processed++
		p.Current = nil
		return n, nil
	}
	after, err := f.Stat()
	if err != nil {
		return n, err
	}
	if scrubFileFingerprint(after) != fingerprint {
		c.Offset, c.Hash, c.File = 0, nil, scrubFingerprint{}
		return n, nil
	}
	live, err := os.Lstat(f.Name())
	if errors.Is(err, os.ErrNotExist) {
		return n, s.finishScrubTarget(p, "missing_blob", "")
	}
	if err != nil {
		return n, err
	}
	if !live.Mode().IsRegular() {
		return n, s.finishScrubTarget(p, "invalid_file_type", "")
	}
	if scrubFileFingerprint(live) != fingerprint {
		c.Offset, c.Hash, c.File = 0, nil, scrubFingerprint{}
		return n, nil
	}
	c.Hash, err = h.(encoding.BinaryMarshaler).MarshalBinary()
	if err != nil {
		return n, err
	}
	if c.Offset == c.Target.Size {
		digest := hex.EncodeToString(h.Sum(nil))
		if digest != c.Target.SHA256 {
			reason = "checksum_mismatch"
		}
		return n, s.finishScrubTarget(p, reason, digest)
	}
	return n, nil
}

func (s *Store) finishScrubTarget(p *scrubState, reason, digest string) error {
	target := p.Current.Target
	s.mu.RLock()
	current, err := s.scrubTargetCurrent(target)
	s.mu.RUnlock()
	if err != nil {
		return err
	}
	if !current {
		p.Stale++
	} else {
		if err = s.saveScrubFinding(*p, target, reason, digest); err != nil {
			return err
		}
		if reason == "" {
			p.Verified++
		} else {
			p.Failures++
		}
	}
	p.Processed++
	p.Current = nil
	return nil
}

func scrubFileFingerprint(info os.FileInfo) scrubFingerprint {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return scrubFingerprint{}
	}
	return scrubFingerprint{Device: uint64(st.Dev), Inode: st.Ino, Size: info.Size(), Modified: info.ModTime().UnixNano(), Changed: st.Ctim.Sec*int64(time.Second) + st.Ctim.Nsec}
}

func (s *Store) openScrubTarget(t ScrubTarget) (*os.File, scrubFingerprint, bool, string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	current, err := s.scrubTargetCurrent(t)
	if err != nil || !current {
		return nil, scrubFingerprint{}, current, "", err
	}
	dir := s.objectPath(t.Bucket, t.Key)
	if t.Kind == "part" {
		dir = s.partPath(t.UploadID, t.PartNumber)
	}
	path := filepath.Join(dir, t.BlobID+".data")
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if errors.Is(err, os.ErrNotExist) {
		return nil, scrubFingerprint{}, true, "missing_blob", nil
	}
	if errors.Is(err, unix.ELOOP) {
		return nil, scrubFingerprint{}, true, "invalid_file_type", nil
	}
	if err != nil {
		return nil, scrubFingerprint{}, true, "", err
	}
	f := os.NewFile(uintptr(fd), path)
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, scrubFingerprint{}, true, "", err
	}
	if !info.Mode().IsRegular() {
		_ = f.Close()
		return nil, scrubFingerprint{}, true, "invalid_file_type", nil
	}
	if info.Size() != t.Size {
		_ = f.Close()
		return nil, scrubFingerprint{}, true, "size_mismatch", nil
	}
	return f, scrubFileFingerprint(info), true, "", nil
}
