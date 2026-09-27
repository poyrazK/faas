package main

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

const flowCoverageInterval = 5 * time.Second

type flowCoverageSink interface {
	InsertOutboundFlowCaptureSample(context.Context, state.OutboundFlowCaptureSample) error
}

// flowCaptureCoverage emits transition samples immediately and a heartbeat
// every five seconds. Missing samples are evidence of unknown coverage even
// when a crash or database outage prevents recording the error itself.
type flowCaptureCoverage struct {
	sink      flowCoverageSink
	nodeID    string
	sessionID string
	log       *slog.Logger
	interval  time.Duration
	updates   chan state.OutboundFlowCaptureSample
	done      chan struct{}

	listening       atomic.Bool
	queueDropped    atomic.Int64
	databaseDropped atomic.Int64
	unparsed        atomic.Int64
	unattributed    atomic.Int64
	stderr          atomic.Int64
}

func newFlowCaptureCoverage(sink flowCoverageSink, nodeID string, log *slog.Logger) *flowCaptureCoverage {
	if log == nil {
		log = slog.Default()
	}
	return &flowCaptureCoverage{
		sink: sink, nodeID: nodeID, sessionID: uuid.NewString(), log: log,
		interval: flowCoverageInterval,
		updates:  make(chan state.OutboundFlowCaptureSample, 64), done: make(chan struct{}),
	}
}

func (c *flowCaptureCoverage) sample(reason string) state.OutboundFlowCaptureSample {
	return state.OutboundFlowCaptureSample{
		ID: uuid.NewString(), SessionID: c.sessionID, NodeID: c.nodeID,
		SampledAt: time.Now().UTC(), Listening: c.listening.Load(), Reason: reason,
		QueueDroppedTotal:    c.queueDropped.Load(),
		DatabaseDroppedTotal: c.databaseDropped.Load(),
		UnparsedTotal:        c.unparsed.Load(), UnattributedTotal: c.unattributed.Load(),
		StderrTotal: c.stderr.Load(),
	}
}

func (c *flowCaptureCoverage) enqueue(reason string) {
	select {
	case c.updates <- c.sample(reason):
	default:
		// The next heartbeat carries the current cumulative counters. If the
		// writer is stalled, missing heartbeat rows also reveal the gap.
	}
}

func (c *flowCaptureCoverage) setListening(listening bool, reason string) {
	c.listening.Store(listening)
	c.enqueue(reason)
}

func (c *flowCaptureCoverage) queueDrop() {
	n := c.queueDropped.Add(1)
	if n == 1 || n%1000 == 0 {
		c.enqueue("queue_full")
	}
}

func (c *flowCaptureCoverage) databaseDrop(n int64, reason string) {
	c.databaseDropped.Add(n)
	c.enqueue(reason)
}

func (c *flowCaptureCoverage) unparsedEvent() {
	n := c.unparsed.Add(1)
	if n == 1 || n%1000 == 0 {
		c.enqueue("unparsed_event")
	}
}

func (c *flowCaptureCoverage) unattributedEvent() bool {
	n := c.unattributed.Add(1)
	if n == 1 || n%1000 == 0 {
		c.enqueue("unattributed_guest_source")
		return true
	}
	return false
}

func (c *flowCaptureCoverage) stderrEvent() {
	n := c.stderr.Add(1)
	if n == 1 || n%1000 == 0 {
		c.enqueue("conntrack_stderr")
	}
}

func (c *flowCaptureCoverage) persist(ctx context.Context, sample state.OutboundFlowCaptureSample) {
	writeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	err := c.sink.InsertOutboundFlowCaptureSample(writeCtx, sample)
	cancel()
	if err != nil {
		c.log.Warn("outbound flow capture coverage sample failed", "node_id", c.nodeID, "reason", sample.Reason, "err", err)
	}
}

func (c *flowCaptureCoverage) run(ctx context.Context) {
	defer close(c.done)
	tick := time.NewTicker(c.interval)
	defer tick.Stop()
	c.enqueue("startup")
	for {
		if ctx.Err() != nil {
			c.listening.Store(false)
			// The daemon may still have a live database connection during its
			// shutdown grace period. A missing final row remains detectable.
			c.persist(context.WithoutCancel(ctx), c.sample("shutdown"))
			return
		}
		select {
		case sample := <-c.updates:
			c.persist(ctx, sample)
		case <-tick.C:
			c.persist(ctx, c.sample("heartbeat"))
		case <-ctx.Done():
			continue
		}
	}
}
