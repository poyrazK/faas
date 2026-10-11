// adr: 096 — gateway app-error ingestion shares apid's database pool.

package main

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"

	apidpb "github.com/onebox-faas/faas/api/proto/onebox/faas/apid/v1"
)

// production-us hunt #5 (H5-34): one app's error stream (≈75 records/s)
// held all twelve of apid's database connections; `secrets set --restart`
// failed at its deadline. Telemetry writes now share at most
// api.TelemetryIngestDBConcurrency connections.
func TestIngestGateBoundsConcurrentWrites(t *testing.T) {
	gate := newIngestGate(2)
	var live, peak atomic.Int32
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			release, err := gate.acquire(context.Background())
			if err != nil {
				t.Error(err)
				return
			}
			n := live.Add(1)
			for {
				p := peak.Load()
				if n <= p || peak.CompareAndSwap(p, n) {
					break
				}
			}
			time.Sleep(5 * time.Millisecond)
			live.Add(-1)
			release()
		}()
	}
	wg.Wait()
	if got := peak.Load(); got != 2 {
		t.Fatalf("peak concurrent writes = %d, want the gate's 2", got)
	}
}

func TestIngestGateWaitStopsWithTheStream(t *testing.T) {
	gate := newIngestGate(1)
	hold, _ := gate.acquire(context.Background())
	defer hold()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := gate.acquire(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want the stream's cancellation", err)
	}
	if release, err := ingestGate(nil).acquire(ctx); err != nil || release == nil {
		t.Fatalf("nil gate: release=%v err=%v, want an admitted no-op", release != nil, err)
	}
}

type gatedAppErrorsStream struct {
	grpc.ServerStream
	ctx  context.Context
	recv int
}

func (s *gatedAppErrorsStream) Context() context.Context { return s.ctx }
func (s *gatedAppErrorsStream) Recv() (*apidpb.IncrementAppErrorRequest, error) {
	s.recv++
	return &apidpb.IncrementAppErrorRequest{}, nil
}
func (s *gatedAppErrorsStream) Send(*apidpb.IncrementAppErrorResponse) error { return nil }

func TestAppErrorsStreamWaitsForTheIngestGate(t *testing.T) {
	gate := newIngestGate(1)
	hold, _ := gate.acquire(context.Background())
	defer hold()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	receiver := newAppErrorsReceiver(nil, nil, true)
	receiver.gate = gate
	stream := &gatedAppErrorsStream{ctx: ctx}
	if err := receiver.IncrementAppError(stream); !errors.Is(err, context.DeadlineExceeded) || stream.recv != 1 {
		t.Fatalf("err=%v records=%d, want the first record to wait on the full gate until the stream ends", err, stream.recv)
	}
}
