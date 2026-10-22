package objectnode

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func (s *Store) scanOnlineBlobs(ctx context.Context, limit int) (err error) {
	// A nonblocking lease cannot deadlock a prepared upload waiting for mu.
	// Holding it excludes preparation until unlink and accounting are settled.
	select {
	case s.replaySlots <- struct{}{}:
		defer func() { <-s.replaySlots }()
	default:
		s.compactionStats.Deferred = true
		return nil
	}
	c := s.compaction
	if c.blobs == nil {
		c.blobs, err = os.Open(s.replicationPath("blobs")) //nolint:forbidigo // Private generated directory, bounded ReadDir cursor.
		if err != nil {
			return err
		}
	}
	defer func() { err = errors.Join(err, s.flushBlobDebt()) }()
	for scanned := 0; scanned < limit; scanned++ {
		if err = ctx.Err(); err != nil {
			return err
		}
		if len(c.entries) == 0 {
			c.entries, err = c.blobs.ReadDir(limit - scanned)
			if err != nil && !errors.Is(err, io.EOF) {
				c.close()
				return err
			}
			if len(c.entries) == 0 {
				return s.finishBlobScan()
			}
		}
		entry := c.entries[0]
		digest := strings.TrimSuffix(entry.Name(), ".data")
		if !validScrubFindingID(digest) || entry.Name() != digest+".data" || !entry.Type().IsRegular() {
			return ErrCorrupt
		}
		if s.retentionIndex.refs[digest] == 0 && s.retentionIndex.pins[digest] == 0 && (s.pending == nil || s.pending.Object.SHA256 != digest) {
			path := s.replicationPath("blobs/" + entry.Name())
			_, size, statErr := retainedFileProof(path)
			if statErr != nil {
				return statErr
			}
			if err = os.Remove(path); err != nil {
				return err
			}
			c.blobDebt.Blobs++
			s.compactionStats.StepBlobs++
			c.blobDebt.BlobBytes += size
			c.entries = c.entries[1:]
			if err = s.compactionEvent(ctx, "pruned blob"); err != nil {
				return err
			}
		} else {
			c.entries = c.entries[1:]
		}
	}
	return nil
}

func (s *Store) flushBlobDebt() error {
	c := s.compaction
	if c == nil || c.blobDebt.Blobs == 0 {
		return nil
	}
	if err := s.syncCompaction("blobs"); err != nil {
		return err
	}
	d := c.blobDebt
	if s.journalUsage.Blobs < d.Blobs || s.journalUsage.BlobBytes < d.BlobBytes {
		return ErrCorrupt
	}
	s.journalUsage.Blobs -= d.Blobs
	s.journalUsage.BlobBytes -= d.BlobBytes
	s.compactionStats.BlobsPruned += d.Blobs
	s.compactionStats.BytesUnlinked += d.BlobBytes
	c.blobDebt = JournalUsage{}
	return nil
}

func (s *Store) finishBlobScan() error {
	if err := s.flushBlobDebt(); err != nil {
		return err
	}
	c := s.compaction
	c.close()
	if s.replication.BaseSequence >= c.target {
		s.compaction = nil
		s.compactionStats.Active = false
		s.compactionStats.Phase = "idle"
		s.compactionStats.Cycles++
	} else {
		c.phase = "records"
		c.nextScan = time.Now().Add(api.ObjectNodeCompactionRetry)
		s.compactionStats.Pinned = true
		s.compactionStats.Phase = "pinned"
	}
	return nil
}
