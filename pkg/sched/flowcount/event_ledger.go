package flowcount

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sort"
	"sync"
	"time"
)

// FlowEventKind identifies the lifecycle transition carried by a flow event.
// An eBPF reader can map conntrack/BPF callbacks to these three stable values.
type FlowEventKind string

const (
	FlowEventOpen   FlowEventKind = "open"
	FlowEventUpdate FlowEventKind = "update"
	FlowEventClose  FlowEventKind = "close"
)

const (
	// DefaultEventTTL bounds how long a completed or quiet flow remains in the
	// event ledger. A short TTL keeps the snapshot useful without allowing a
	// long-running tenant to turn lifecycle history into unbounded state.
	DefaultEventTTL = 2 * time.Minute
	// DefaultMaxEventFlows bounds distinct flow keys retained per instance.
	DefaultMaxEventFlows = 128
)

var ErrInvalidFlowEvent = errors.New("flowcount: invalid flow event")

// FlowEvent is the backend-neutral ingestion contract for richer egress
// telemetry. Bytes and Packets are deltas for this observation, not
// cumulative counters. At is optional; the ledger's clock fills it in.
//
// The current conntrack reader intentionally does not produce FlowEvents:
// conntrack has no reliable byte/latency samples. An eBPF adapter can feed
// this same contract without changing snapshot consumers.
type FlowEvent struct {
	InstanceID string
	Protocol   string
	RemoteIP   string
	RemotePort uint16
	State      string
	Direction  string
	Kind       FlowEventKind
	Bytes      uint64
	Packets    uint64
	At         time.Time
}

// FlowDetail is the bounded per-key result produced by EventLedger. Count is
// the number of observed flow lifecycles for the key; Active describes the
// latest lifecycle state. Bytes and Packets are saturated sums of event
// deltas, so malformed or hostile input cannot wrap the counters.
type FlowDetail struct {
	FlowSummary
	Bytes     uint64
	Packets   uint64
	FirstSeen time.Time
	LastSeen  time.Time
	Active    bool
}

// FlowEventSnapshotter is the optional detail surface for an eBPF-backed
// source. It is deliberately separate from Snapshotter: conntrack summaries
// and event-derived byte/lifecycle details have different freshness and
// failure semantics.
type FlowEventSnapshotter interface {
	DetailSnapshot(context.Context, string) ([]FlowDetail, error)
}

// EventLedgerOption configures an EventLedger.
type EventLedgerOption func(*EventLedger)

// WithEventTTL changes the quiet-flow retention period. Non-positive values
// are ignored so a bad config cannot disable the bounded-state guarantee.
func WithEventTTL(ttl time.Duration) EventLedgerOption {
	return func(l *EventLedger) {
		if ttl > 0 {
			l.ttl = ttl
		}
	}
}

// WithMaxEventFlows changes the per-instance flow-key cap. Non-positive
// values are ignored and leave DefaultMaxEventFlows in effect.
func WithMaxEventFlows(max int) EventLedgerOption {
	return func(l *EventLedger) {
		if max > 0 {
			l.maxFlows = max
		}
	}
}

// WithEventClock injects the clock used for zero-timestamp events and
// automatic pruning. It exists to make retention tests deterministic.
func WithEventClock(now func() time.Time) EventLedgerOption {
	return func(l *EventLedger) {
		if now != nil {
			l.now = now
		}
	}
}

type eventFlowKey struct {
	instanceID string
	protocol   string
	remoteIP   string
	remotePort uint16
	direction  string
}

type eventFlowState struct {
	detail FlowDetail
}

// EventLedger aggregates backend events into a bounded, concurrency-safe
// snapshot. It has no goroutine of its own: Observe and DetailSnapshot prune
// expired entries, while Prune is available for a periodic source loop.
type EventLedger struct {
	mu       sync.Mutex
	flows    map[eventFlowKey]*eventFlowState
	ttl      time.Duration
	maxFlows int
	now      func() time.Time
	dropped  int64
}

// NewEventLedger constructs an empty bounded event ledger.
func NewEventLedger(opts ...EventLedgerOption) *EventLedger {
	l := &EventLedger{
		flows:    make(map[eventFlowKey]*eventFlowState),
		ttl:      DefaultEventTTL,
		maxFlows: DefaultMaxEventFlows,
		now:      time.Now,
	}
	for _, opt := range opts {
		opt(l)
	}
	return l
}

// Observe applies one lifecycle or counter-delta event. Invalid events are
// rejected before touching the ledger. New keys beyond the per-instance cap
// are dropped and counted, preserving the fail-open behavior of flowcount's
// existing telemetry paths.
func (l *EventLedger) Observe(event FlowEvent) error {
	if l == nil {
		return errors.New("flowcount: nil event ledger")
	}
	if err := validateFlowEvent(event); err != nil {
		return err
	}
	now := l.now()
	at := event.At
	if at.IsZero() {
		at = now
	}

	key := eventFlowKey{
		instanceID: event.InstanceID,
		protocol:   event.Protocol,
		remoteIP:   event.RemoteIP,
		remotePort: event.RemotePort,
		direction:  event.Direction,
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	l.pruneLocked(now)

	state := l.flows[key]
	if state == nil {
		if l.instanceFlowCountLocked(event.InstanceID) >= l.maxFlows {
			l.dropped++
			return nil
		}
		state = &eventFlowState{detail: FlowDetail{
			FlowSummary: FlowSummary{
				InstanceID: event.InstanceID,
				Protocol:   event.Protocol,
				RemoteIP:   event.RemoteIP,
				RemotePort: event.RemotePort,
				State:      event.State,
				Direction:  event.Direction,
				Count:      1,
			},
			Bytes:     event.Bytes,
			Packets:   event.Packets,
			FirstSeen: at,
			LastSeen:  at,
			Active:    event.Kind != FlowEventClose,
		}}
		l.flows[key] = state
		return nil
	}

	detail := &state.detail
	detail.Bytes = saturatedAdd(detail.Bytes, event.Bytes)
	detail.Packets = saturatedAdd(detail.Packets, event.Packets)
	if at.Before(detail.FirstSeen) {
		detail.FirstSeen = at
	}
	if at.After(detail.LastSeen) {
		// Out-of-order delivery must not move LastSeen backwards.
		detail.LastSeen = at
	}
	if event.State != "" {
		detail.State = event.State
	}
	switch event.Kind {
	case FlowEventOpen:
		if !detail.Active {
			detail.Count = saturatedCount(detail.Count)
		}
		detail.Active = true
	case FlowEventClose:
		detail.Active = false
	case FlowEventUpdate:
		// Keep the last lifecycle state. An update may carry an empty state
		// when the source only observed a byte/packet delta.
	}
	return nil
}

// DetailSnapshot returns a deterministic defensive copy for one instance.
// The empty result is nil, nil; callers can distinguish a source error from
// an instance with no currently retained flow details.
func (l *EventLedger) DetailSnapshot(ctx context.Context, instanceID string) ([]FlowDetail, error) {
	if l == nil {
		return nil, errors.New("flowcount: nil event ledger")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	now := l.now()
	l.mu.Lock()
	l.pruneLocked(now)
	var out []FlowDetail
	for key, state := range l.flows {
		if key.instanceID != instanceID {
			continue
		}
		out = append(out, state.detail)
	}
	l.mu.Unlock()
	sort.Slice(out, func(i, j int) bool {
		if out[i].RemoteIP != out[j].RemoteIP {
			return out[i].RemoteIP < out[j].RemoteIP
		}
		if out[i].RemotePort != out[j].RemotePort {
			return out[i].RemotePort < out[j].RemotePort
		}
		return out[i].Direction < out[j].Direction
	})
	return out, nil
}

// Prune removes quiet entries older than the configured TTL and returns the
// number removed. A zero timestamp uses the injected clock.
func (l *EventLedger) Prune(now time.Time) int {
	if l == nil {
		return 0
	}
	if now.IsZero() {
		now = l.now()
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.pruneLocked(now)
}

// DroppedEvents reports how many new flow keys were refused by the bound.
func (l *EventLedger) DroppedEvents() int64 {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.dropped
}

func (l *EventLedger) instanceFlowCountLocked(instanceID string) int {
	count := 0
	for key := range l.flows {
		if key.instanceID == instanceID {
			count++
		}
	}
	return count
}

func (l *EventLedger) pruneLocked(now time.Time) int {
	removed := 0
	for key, state := range l.flows {
		if now.Before(state.detail.LastSeen) || now.Sub(state.detail.LastSeen) < l.ttl {
			continue
		}
		delete(l.flows, key)
		removed++
	}
	return removed
}

func validateFlowEvent(event FlowEvent) error {
	if event.InstanceID == "" || event.Protocol == "" || event.RemotePort == 0 || net.ParseIP(event.RemoteIP) == nil {
		return fmt.Errorf("%w: instance, protocol, remote ip, and remote port are required", ErrInvalidFlowEvent)
	}
	if event.Direction != "inbound" && event.Direction != "outbound" {
		return fmt.Errorf("%w: direction must be inbound or outbound", ErrInvalidFlowEvent)
	}
	switch event.Kind {
	case FlowEventOpen, FlowEventUpdate, FlowEventClose:
		return nil
	case "":
		return fmt.Errorf("%w: event kind is required", ErrInvalidFlowEvent)
	default:
		return fmt.Errorf("%w: unknown event kind %q", ErrInvalidFlowEvent, event.Kind)
	}
}

func saturatedAdd(a, b uint64) uint64 {
	if ^uint64(0)-a < b {
		return ^uint64(0)
	}
	return a + b
}

func saturatedCount(count int64) int64 {
	if count == int64(^uint64(0)>>1) {
		return count
	}
	return count + 1
}
