package objectnode

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// BenchmarkReplicationPublication profiles concurrent tracked writes and a
// foreground reader. The optional follower shares this host/filesystem; its
// final tail is outside the timed primary workload. Qualification checks the
// complete replay and recovery contract separately.
func BenchmarkReplicationPublication(b *testing.B) {
	for _, replica := range []bool{false, true} {
		name := "primary"
		if replica {
			name = "with_follower"
		}
		b.Run(name, func(b *testing.B) { benchmarkPublication(b, replica) })
	}
}

func benchmarkPublication(b *testing.B, replica bool) {
	directory := filepath.Join(b.TempDir(), "primary")
	primary, err := Open(directory)
	if err != nil {
		b.Fatal(err)
	}
	if err = primary.CreateBucket("test-bucket"); err != nil {
		b.Fatal(err)
	}
	if _, err = primary.Put(b.Context(), "test-bucket", "baseline", bytes.NewReader([]byte("seed")), 4, PutOptions{}); err != nil {
		b.Fatal(err)
	}
	if err = primary.Close(); err != nil {
		b.Fatal(err)
	}
	if _, err = EnableReplication(b.Context(), directory); err != nil {
		b.Fatal(err)
	}
	var follower *ReplicaFollower
	if replica {
		seed, target := filepath.Join(b.TempDir(), "seed"), filepath.Join(b.TempDir(), "follower")
		if _, err = Backup(b.Context(), directory, seed); err != nil {
			b.Fatal(err)
		}
		if _, err = BootstrapReplica(b.Context(), seed, target); err != nil {
			b.Fatal(err)
		}
		follower, err = OpenFollower(target)
		if err != nil {
			b.Fatal(err)
		}
		defer func() { _ = follower.Close() }()
	}
	primary, err = Open(directory)
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = primary.Close() }()
	ctx, cancel := context.WithCancel(b.Context())
	defer cancel()
	readerDone := make(chan struct{})
	var reads, readNanos, maxRead atomic.Int64
	go func() {
		defer close(readerDone)
		for ctx.Err() == nil {
			started := time.Now()
			_, file, err := primary.Get("test-bucket", "baseline")
			if err != nil {
				b.Error(err)
				return
			}
			_ = file.Close()
			n := int64(time.Since(started))
			reads.Add(1)
			readNanos.Add(n)
			for old := maxRead.Load(); n > old && !maxRead.CompareAndSwap(old, n); old = maxRead.Load() {
			}
		}
	}()
	followerDone := make(chan struct{})
	if follower != nil {
		go func() {
			defer close(followerDone)
			for ctx.Err() == nil {
				p, err := follower.CatchUp(ctx, localReplicationSource{primary}, CatchUpOptions{MaxEntries: api.ObjectNodeReplicationBatch, MaxBytes: api.ObjectNodeReplicationStepBytes})
				if err == nil && p.CaughtUp {
					_, err = primary.WaitForReplicationChange(ctx, *p.WaitCursor)
				}
				if err != nil && !errors.Is(err, context.Canceled) {
					b.Error(err)
					return
				}
			}
		}()
	} else {
		close(followerDone)
	}
	const size = 5 << 20
	var sequence atomic.Uint64
	b.SetBytes(size)
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(p *testing.PB) {
		body := make([]byte, size)
		for p.Next() {
			binary.LittleEndian.PutUint64(body, sequence.Add(1))
			id := uuid.NewString()
			if _, err := primary.Put(ctx, "test-bucket", id, bytes.NewReader(body), size, PutOptions{Receipt: id}); err != nil {
				b.Error(err)
				return
			}
		}
	})
	b.StopTimer()
	cancel()
	<-readerDone
	<-followerDone
	if count := reads.Load(); count != 0 {
		b.ReportMetric(float64(readNanos.Load())/float64(count)/float64(time.Millisecond), "read_avg_ms")
	}
	b.ReportMetric(float64(maxRead.Load())/float64(time.Millisecond), "read_max_ms")
}
