package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

type captureCoverageSink struct {
	mu      sync.Mutex
	samples []state.OutboundFlowCaptureSample
}

func (s *captureCoverageSink) InsertOutboundFlowCaptureSample(_ context.Context, sample state.OutboundFlowCaptureSample) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.samples = append(s.samples, sample)
	return nil
}

func (s *captureCoverageSink) snapshot() []state.OutboundFlowCaptureSample {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]state.OutboundFlowCaptureSample(nil), s.samples...)
}

func TestFlowCaptureCoverageHeartbeatsAndLossCounters(t *testing.T) {
	sink := &captureCoverageSink{}
	c := newFlowCaptureCoverage(sink, uuid.NewString(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	c.interval = 5 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	go c.run(ctx)
	c.setListening(true, "listening")
	c.queueDrop()
	c.databaseDrop(3, "database_write_failed")
	c.unparsedEvent()
	c.unattributedEvent()
	c.stderrEvent()
	deadline := time.After(time.Second)
	for {
		samples := sink.snapshot()
		heartbeat := false
		for _, sample := range samples {
			if sample.Reason == "heartbeat" && sample.Listening {
				heartbeat = true
			}
		}
		if heartbeat {
			break
		}
		select {
		case <-deadline:
			t.Fatal("no healthy heartbeat persisted")
		case <-time.After(time.Millisecond):
		}
	}
	cancel()
	select {
	case <-c.done:
	case <-time.After(time.Second):
		t.Fatal("coverage writer did not stop")
	}
	samples := sink.snapshot()
	last := samples[len(samples)-1]
	if last.Reason != "shutdown" || last.Listening || last.QueueDroppedTotal != 1 ||
		last.DatabaseDroppedTotal != 3 || last.UnparsedTotal != 1 ||
		last.UnattributedTotal != 1 || last.StderrTotal != 1 {
		t.Fatalf("final coverage sample = %#v", last)
	}
	for _, sample := range samples {
		if sample.SessionID != c.sessionID || sample.NodeID != c.nodeID || sample.ID == "" {
			t.Fatalf("inconsistent coverage identity = %#v", sample)
		}
	}
}

type canceledFlowSink struct{}

func (canceledFlowSink) InsertOutboundFlowEvents(ctx context.Context, _ []state.OutboundFlowEvent) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return errors.New("database unavailable")
}

func TestWriteOutboundFlowsCountsShutdownQueue(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	queue := make(chan state.OutboundFlowEvent, 2)
	queue <- state.OutboundFlowEvent{}
	queue <- state.OutboundFlowEvent{}
	close(queue)
	cancel()
	coverage := newFlowCaptureCoverage(&captureCoverageSink{}, uuid.NewString(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	writeOutboundFlows(ctx, canceledFlowSink{}, queue, coverage.log, coverage)
	if got := coverage.databaseDropped.Load(); got != 2 {
		t.Fatalf("shutdown loss count = %d, want 2", got)
	}
}
