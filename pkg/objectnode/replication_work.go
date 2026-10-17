package objectnode

import (
	"context"
	"io"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"golang.org/x/time/rate"
)

// A worker keeps its budgets across bounded steps and reconnects. Bytes cover
// attempted network ranges and local replay reads/copies, including cache hits.
// This bounds byte work, not fsync latency, CPU time or startup verification.
type replicaWorkBudget struct {
	byteRate  int64
	entryRate int
	bytes     *rate.Limiter
	entries   *rate.Limiter
	total     int64
	wait      time.Duration
	entryWait time.Duration
}

func validCatchUpOptions(o CatchUpOptions) bool {
	return o.MaxEntries >= 1 && o.MaxEntries <= api.ObjectNodeReplicationBatch && o.MaxBytes >= 1 && o.MaxBytes <= api.ObjectNodeReplicationStepBytes && (o.WorkBytesPerSecond == 0 && o.WorkEntriesPerSecond == 0 || o.WorkBytesPerSecond >= 1 && o.WorkBytesPerSecond <= api.ObjectNodeReplicationMaxWorkBytes && o.WorkEntriesPerSecond >= 1 && o.WorkEntriesPerSecond <= api.ObjectNodeReplicationMaxWorkEntries)
}

func (s *Store) configureReplicaWork(o CatchUpOptions) {
	if o.WorkBytesPerSecond == 0 {
		s.replicaWork = nil
		return
	}
	w := s.replicaWork
	if w == nil || w.byteRate != o.WorkBytesPerSecond || w.entryRate != o.WorkEntriesPerSecond {
		s.replicaWork = &replicaWorkBudget{byteRate: o.WorkBytesPerSecond, entryRate: o.WorkEntriesPerSecond,
			bytes: rate.NewLimiter(rate.Limit(o.WorkBytesPerSecond), int(api.ObjectNodeReplicationChunkBytes)), entries: rate.NewLimiter(rate.Limit(o.WorkEntriesPerSecond), api.ObjectNodeReplicationBatch)}
	}
}

func (w *replicaWorkBudget) waitBytes(ctx context.Context, n int) error {
	if w == nil {
		return nil
	}
	started := time.Now()
	err := w.bytes.WaitN(ctx, n)
	w.wait += time.Since(started)
	if err == nil {
		w.total += int64(n)
	}
	return err
}

func (w *replicaWorkBudget) waitEntries(ctx context.Context, n int) error {
	if w == nil {
		return nil
	}
	started := time.Now()
	err := w.entries.WaitN(ctx, n)
	elapsed := time.Since(started)
	w.wait += elapsed
	w.entryWait += elapsed
	return err
}

type pacedReplicaReader struct {
	ctx    context.Context
	reader io.Reader
	work   *replicaWorkBudget
}

func (r pacedReplicaReader) Read(p []byte) (int, error) {
	if err := r.work.waitBytes(r.ctx, len(p)); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}
