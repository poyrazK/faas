package domainverify

import (
	"context"
	"errors"
	"sync"
	"time"
)

// ErrBusy means the manual-check worker bound has been reached.
var ErrBusy = errors.New("domain verification workers are busy")

type flight struct {
	done chan struct{}
	err  error
}

// Checker coalesces checks for a claim and bounds manual work independently of
// HTTP callers. A cancelled waiter does not cancel another waiter's check.
// The zero value is ready for use; no completed claims are retained in memory.
type Checker struct {
	mu     sync.Mutex
	active map[string]*flight
	wake   chan struct{}
}

// Do joins an existing claim's check or starts a bounded check. Each caller
// waits only until its own context ends, while the shared check has a timeout.
func (c *Checker) Do(ctx context.Context, key string, limit int, timeout time.Duration, check func(context.Context) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	f := c.active[key]
	if f == nil {
		if len(c.active) >= limit {
			c.mu.Unlock()
			return ErrBusy
		}
		if c.active == nil {
			c.active = make(map[string]*flight)
		}
		f = &flight{done: make(chan struct{})}
		c.active[key] = f
		go func() {
			checkCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
			defer cancel()
			f.err = check(checkCtx)
			c.mu.Lock()
			delete(c.active, key)
			close(f.done)
			c.mu.Unlock()
		}()
	}
	c.mu.Unlock()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-f.done:
		return f.err
	}
}

// Wake requests an early ownership pass. The channel holds one hint; the
// durable due-time scan remains the source of truth and periodic fallback.
func (c *Checker) Wake() {
	select {
	case c.wakeChannel() <- struct{}{}:
	default:
	}
}

// Wakes receives coalesced hints to scan for due ownership checks.
func (c *Checker) Wakes() <-chan struct{} {
	return c.wakeChannel()
}

func (c *Checker) wakeChannel() chan struct{} {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.wake == nil {
		c.wake = make(chan struct{}, 1)
	}
	return c.wake
}
