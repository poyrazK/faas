package chaos

import (
	"context"
	"errors"
	"io"
	"net"
	"reflect"
	"sync"
	"time"
)

// ErrTCPReset identifies an intentional fault rather than an unhealthy replica.
var (
	ErrTCPReset          = errors.New("scenario TCP reset")
	ErrTCPConnectTimeout = errors.New("scenario TCP connect timeout")
	ErrTCPConnectRefused = errors.New("scenario TCP connection refused")
)

const tcpChunkSize = 4096

// TCPConn impairs an established byte stream without inspecting its payload.
// At most one chunk is gated in each direction, without adding a queue to the
// caller's bounded transport buffers. Update is safe while Read and Write are
// blocked; changing or expiring a lease releases those waits.
type TCPConn struct {
	net.Conn
	ctx      context.Context
	cancel   context.CancelFunc
	port     int
	ordinal  uint64
	onInject func(Rule, string)
	onMatch  func(Rule, string, string)

	mu          sync.Mutex
	lease       Lease
	changed     chan struct{}
	initialized bool
	resetAt     time.Time
	reset       bool
	observed    map[string]bool
	readMu      sync.Mutex
	writeMu     sync.Mutex
	closeOnce   sync.Once
}

func NewTCPConn(ctx context.Context, conn net.Conn, port int, ordinal uint64, onInject func(Rule, string)) *TCPConn {
	return NewTCPConnWithMatchObserver(ctx, conn, port, ordinal, onInject, nil)
}

// NewTCPConnWithMatchObserver preserves the ordinary per-direction injection
// callback and adds a callback carrying the plan generation active when a
// fault matches. Scenario evidence uses that generation to reject stale events
// after a plan has been replaced.
func NewTCPConnWithMatchObserver(ctx context.Context, conn net.Conn, port int, ordinal uint64, onInject func(Rule, string), onMatch func(Rule, string, string)) *TCPConn {
	ctx, cancel := context.WithCancel(ctx)
	c := &TCPConn{Conn: conn, ctx: ctx, cancel: cancel, port: port, ordinal: ordinal,
		onInject: onInject, onMatch: onMatch, changed: make(chan struct{}), observed: make(map[string]bool)}
	go c.watch()
	return c
}

// Update validates and selects rules once per connection. TCP has no trace ID;
// the ordinal stays fixed over policy refreshes and forwarding retries.
func (c *TCPConn) Update(lease Lease) error {
	selected := Lease{Generation: lease.Generation, ExpiresAt: lease.ExpiresAt}
	c.mu.Lock()
	initial := !c.initialized
	c.initialized = true
	c.mu.Unlock()
	for _, rule := range lease.Rules {
		if err := ValidateRule(rule); err != nil {
			return err
		}
		if rule.IsTCP() && rule.Port == c.port && Select(rule, c.ordinal, "") && (initial || !rule.IsTCPConnectFault()) {
			selected.Rules = append(selected.Rules, rule)
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.lease.Generation == selected.Generation && c.lease.ExpiresAt.Equal(selected.ExpiresAt) && reflect.DeepEqual(c.lease.Rules, selected.Rules) {
		return nil
	}
	c.lease = selected
	c.observed = make(map[string]bool)
	c.resetAt = time.Time{}
	for _, rule := range selected.Rules {
		if rule.Kind == KindTCPReset {
			deadline := time.Now().Add(time.Duration(rule.ResetAfterMS) * time.Millisecond)
			if c.resetAt.IsZero() || deadline.Before(c.resetAt) {
				c.resetAt = deadline
			}
		}
	}
	close(c.changed)
	c.changed = make(chan struct{})
	return nil
}

// ConnectionFault reports a connect-phase fault selected when this socket was
// accepted. These rules never attach to a connection that was already open
// when a plan was installed.
func (c *TCPConn) ConnectionFault() (Rule, bool) {
	c.mu.Lock()
	lease := c.lease
	if !lease.ExpiresAt.After(time.Now()) {
		c.mu.Unlock()
		return Rule{}, false
	}
	var selected Rule
	found := false
	for _, rule := range lease.Rules {
		if rule.IsTCPConnectFault() {
			selected, found = rule, true
			break
		}
	}
	c.mu.Unlock()
	if found {
		c.observe(selected, DirectionBoth, lease.Generation)
	}
	return selected, found
}

// WaitForConnectTimeout holds a newly accepted route before it reaches the
// guest listener. Client bytes are drained and discarded while the fault is
// active, and policy clear or lease expiry ends the wait with a timeout.
func (c *TCPConn) WaitForConnectTimeout() error {
	readResult := make(chan error, 1)
	go func() {
		buffer := make([]byte, tcpChunkSize)
		for {
			if _, err := c.Conn.Read(buffer); err != nil {
				readResult <- err
				return
			}
		}
	}()
	for {
		c.mu.Lock()
		lease, changed := c.lease, c.changed
		active := false
		for _, rule := range lease.Rules {
			if rule.Kind == KindTCPConnectTimeout {
				active = true
				break
			}
		}
		c.mu.Unlock()
		if !active || !lease.ExpiresAt.After(time.Now()) {
			return ErrTCPConnectTimeout
		}
		timer := time.NewTimer(time.Until(lease.ExpiresAt))
		select {
		case <-c.ctx.Done():
			timer.Stop()
			return c.ctx.Err()
		case err := <-readResult:
			timer.Stop()
			return err
		case <-changed:
			timer.Stop()
		case <-timer.C:
			return ErrTCPConnectTimeout
		}
	}
}

// ResetNow aborts the accepted client socket without opening a guest route.
func (c *TCPConn) ResetNow() {
	c.mu.Lock()
	c.reset = true
	c.mu.Unlock()
	if tcp, ok := c.Conn.(*net.TCPConn); ok {
		_ = tcp.SetLinger(0)
	}
	_ = c.Close()
}

func (c *TCPConn) Close() error {
	var err error
	c.closeOnce.Do(func() {
		c.cancel()
		err = c.Conn.Close()
	})
	return err
}

func (c *TCPConn) closedError() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.reset {
		return ErrTCPReset
	}
	return net.ErrClosed
}

func (c *TCPConn) Read(p []byte) (int, error) {
	c.readMu.Lock()
	defer c.readMu.Unlock()
	if c.ctx.Err() != nil {
		return 0, c.closedError()
	}
	n, err := c.Conn.Read(p[:min(len(p), tcpChunkSize)])
	if n > 0 {
		if gateErr := c.gate(DirectionUpstream, n); gateErr != nil {
			return 0, gateErr
		}
	}
	if err != nil && c.ctx.Err() != nil {
		return n, c.closedError()
	}
	return n, err
}

func (c *TCPConn) Write(p []byte) (int, error) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	total := 0
	for len(p) > 0 {
		n := min(len(p), tcpChunkSize)
		if err := c.gate(DirectionDownstream, n); err != nil {
			return total, err
		}
		written, err := c.Conn.Write(p[:n])
		total += written
		p = p[written:]
		if err != nil {
			if c.ctx.Err() != nil {
				err = c.closedError()
			}
			return total, err
		}
		if written != n {
			return total, io.ErrShortWrite
		}
	}
	return total, nil
}

// gate waits before forwarding a chunk. Backpressure prevents an unbounded
// queue; waits always observe cancellation, plan replacement and lease expiry.
func (c *TCPConn) gate(direction string, bytes int) error {
	started := time.Now()
	for {
		if c.ctx.Err() != nil {
			return c.closedError()
		}
		c.mu.Lock()
		lease, changed := c.lease, c.changed
		c.mu.Unlock()
		now := time.Now()
		if !lease.ExpiresAt.After(now) {
			return nil
		}
		var delay time.Duration
		blocked := false
		for _, rule := range lease.Rules {
			if rule.EffectiveDirection() != DirectionBoth && rule.EffectiveDirection() != direction {
				continue
			}
			switch rule.Kind {
			case KindTCPLatency:
				delay += time.Duration(rule.LatencyMS) * time.Millisecond
			case KindTCPBandwidth:
				delay += time.Duration(int64(bytes) * int64(time.Second) / (rule.RateKiBPerSecond * 1024))
			case KindTCPTimeout:
				blocked = true
			default:
				continue
			}
			c.observe(rule, direction, lease.Generation)
		}
		wait := time.Until(lease.ExpiresAt)
		if !blocked {
			wait = min(wait, delay-time.Since(started))
		}
		if wait <= 0 {
			return nil
		}
		timer := time.NewTimer(wait)
		select {
		case <-c.ctx.Done():
			timer.Stop()
			return c.closedError()
		case <-changed:
			timer.Stop()
			started = time.Now()
		case <-timer.C:
			return nil
		}
	}
}

func (c *TCPConn) observe(rule Rule, direction, generation string) {
	key := rule.Kind + "/" + direction
	c.mu.Lock()
	seen := c.observed[key]
	c.observed[key] = true
	c.mu.Unlock()
	if !seen && c.onInject != nil {
		c.onInject(rule, direction)
	}
	if !seen && c.onMatch != nil {
		c.onMatch(rule, direction, generation)
	}
}

func (c *TCPConn) watch() {
	for {
		c.mu.Lock()
		lease, changed, resetAt := c.lease, c.changed, c.resetAt
		c.mu.Unlock()
		var deadline <-chan time.Time
		var timer *time.Timer
		if lease.ExpiresAt.After(time.Now()) && !resetAt.IsZero() {
			timer = time.NewTimer(max(0, time.Until(minTime(resetAt, lease.ExpiresAt))))
			deadline = timer.C
		}
		select {
		case <-c.ctx.Done():
			if timer != nil {
				timer.Stop()
			}
			_ = c.Close()
			return
		case <-changed:
			if timer != nil {
				timer.Stop()
			}
		case <-deadline:
			c.mu.Lock()
			// Recheck under the lock so a replaced or expired rule cannot reset.
			if c.changed != changed {
				c.mu.Unlock()
				continue
			}
			if !c.lease.ExpiresAt.After(time.Now()) {
				c.resetAt = time.Time{}
				c.mu.Unlock()
				continue
			}
			c.reset = true
			c.mu.Unlock()
			for _, rule := range lease.Rules {
				if rule.Kind == KindTCPReset {
					c.observe(rule, DirectionBoth, lease.Generation)
				}
			}
			if tcp, ok := c.Conn.(*net.TCPConn); ok {
				_ = tcp.SetLinger(0)
			}
			_ = c.Close()
			return
		}
	}
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
