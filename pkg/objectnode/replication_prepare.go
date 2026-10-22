package objectnode

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// preparedReplay is an in-process proof for one immutable upload. It is never
// serialized or used by restart recovery. Only the catalog-lock owner binds it.
type preparedReplay struct {
	store       *Store
	object      Object
	stage, temp string
	live        string
	sourceProof scrubFingerprint
	tempProof   scrubFingerprint
	liveProof   scrubFingerprint
	replayProof scrubFingerprint
	synced      bool
}

func replayFingerprint(path string, size int64) (scrubFingerprint, error) {
	f, err := openBackupFile(path)
	if err != nil {
		return scrubFingerprint{}, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil || info.Size() != size {
		return scrubFingerprint{}, ErrCorrupt
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Nlink != 1 {
		return scrubFingerprint{}, ErrCorrupt
	}
	return scrubFileFingerprint(info), nil
}

func checkReplayFingerprint(path string, size int64, expected scrubFingerprint) error {
	actual, err := replayFingerprint(path, size)
	if err != nil {
		return err
	}
	if actual != expected {
		return ErrCorrupt
	}
	return nil
}

func (s *Store) prepareReplay(ctx context.Context, stage string, o Object) (*preparedReplay, error) {
	s.mu.RLock()
	enabled := s.replication != nil
	s.mu.RUnlock()
	if !enabled {
		return nil, nil
	}
	select {
	case s.replaySlots <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	p := &preparedReplay{store: s, object: o, stage: stage}
	fail := func(err error) (*preparedReplay, error) { p.close(); return nil, err }
	var err error
	p.sourceProof, err = replayFingerprint(stage, o.Size)
	if err != nil {
		return fail(err)
	}
	destination := s.replicationPath("blobs/" + o.SHA256 + ".data")
	if p.replayProof, err = replayFingerprint(destination, o.Size); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return fail(err)
		}
		p.temp = filepath.Join(s.root, "staging", uuid.NewString())
	}
	buffer := make([]byte, api.ObjectNodeBackupBufferBytes)
	digest, err := s.replayCopy(ctx, stage, p.temp, o.Size, buffer)
	if err != nil {
		return fail(err)
	}
	if digest != o.SHA256 {
		return fail(ErrDigest)
	}
	if err = checkReplayFingerprint(stage, o.Size, p.sourceProof); err != nil {
		return fail(err)
	}
	if p.temp != "" {
		p.tempProof, err = replayFingerprint(p.temp, o.Size)
	} else {
		digest, err = s.replayCopy(ctx, destination, "", o.Size, buffer)
		if err == nil && digest != o.SHA256 {
			err = ErrDigest
		}
		if err == nil {
			err = checkReplayFingerprint(destination, o.Size, p.replayProof)
		}
	}
	if err != nil {
		return fail(err)
	}
	return p, nil
}

func (p *preparedReplay) close() {
	if p != nil {
		if p.temp != "" {
			_ = os.Remove(p.temp)
		}
		<-p.store.replaySlots
	}
}

func (s *Store) bindPreparedReplay(p *preparedReplay) func() {
	s.preparedReplay = p
	return func() { s.preparedReplay = nil }
}

func (s *Store) moveStagedBlob(source, destination string) error {
	p := s.preparedReplay
	if p != nil && p.stage == source {
		if err := checkReplayFingerprint(source, p.object.Size, p.sourceProof); err != nil {
			return err
		}
	}
	if err := os.Rename(source, destination); err != nil {
		return err
	}
	if p != nil && p.stage == source {
		proof, err := replayFingerprint(destination, p.object.Size)
		if err != nil {
			return err
		}
		// Rename changes ctime, but cannot change the inode, bytes or mtime.
		before, after := p.sourceProof, proof
		before.Changed, after.Changed = 0, 0
		if before != after {
			return ErrCorrupt
		}
		p.live, p.liveProof = destination, proof
	}
	return nil
}

func (s *Store) preparedFor(blob *ReplicationBlob) *preparedReplay {
	p := s.preparedReplay
	if p == nil || p.live != filepath.Join(s.root, blob.Path) || p.object.Size != blob.Size || p.object.SHA256 != blob.SHA256 {
		return nil
	}
	return p
}

func (p *preparedReplay) install() error {
	s, size := p.store, p.object.Size
	if err := checkReplayFingerprint(p.live, size, p.liveProof); err != nil {
		return err
	}
	destination := s.replicationPath("blobs/" + p.object.SHA256 + ".data")
	if p.temp != "" {
		if err := checkReplayFingerprint(p.temp, size, p.tempProof); err != nil {
			return err
		}
		if err := s.canRetainBlob(size); err != nil {
			return err
		}
		if err := renameNoReplace(p.temp, destination); err != nil {
			return err
		}
		p.temp = ""
		s.journalUsage.Blobs++
		s.journalUsage.BlobBytes += size
		proof, err := replayFingerprint(destination, size)
		if err != nil {
			return err
		}
		before, after := p.tempProof, proof
		before.Changed, after.Changed = 0, 0
		if before != after {
			return ErrCorrupt
		}
		p.replayProof = proof
	}
	if err := checkReplayFingerprint(destination, size, p.replayProof); err != nil {
		return err
	}
	if !p.synced {
		if err := syncDirectory(filepath.Dir(destination)); err != nil {
			return err
		}
		if err := syncDirectory(filepath.Join(s.root, "staging")); err != nil {
			return err
		}
		p.synced = true
	}
	return nil
}
