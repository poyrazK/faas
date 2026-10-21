package functionlifetime

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type Dial func(context.Context) (net.Conn, error)

// Client serializes reservation IDs. Retrying an uncertain Begin uses the
// same ID; failed reservations are tombstoned before they can arrive late.
type Client struct {
	dial         Dial
	gate         chan struct{}
	calls        chan struct{}
	sequence     uint64
	ObserveError func(error)
}

func NewClient(dial Dial) *Client {
	return &Client{dial: dial, gate: make(chan struct{}, 1), calls: make(chan struct{}, 8)}
}

// Acquire holds the gate across the whole Begin round trip, not just sequence
// allocation. The host rejects a Begin at or below its high-water sequence as
// stale, so concurrent Begins on separate connections could arrive out of order
// and fail; a retried uncertain Begin must also still be the highest sequence.
// A Busy or Limit reply returns at once; only an unreachable host costs the
// two RPCTimeout attempts.
func (c *Client) Acquire(ctx context.Context, budget time.Duration, hook string) (*Lease, api.Plan, error) {
	select {
	case c.gate <- struct{}{}:
	case <-ctx.Done():
		return nil, "", ctx.Err()
	}
	defer func() { <-c.gate }()
	if c.sequence == ^uint64(0) {
		return nil, "", errors.New("function lifetime: sequence exhausted")
	}
	c.sequence++
	req := Request{Version: Version, Kind: Begin, Sequence: c.sequence, BudgetMS: budget.Milliseconds(), Hook: hook}
	if err := req.Validate(); err != nil {
		return nil, "", err
	}
	lease := &Lease{client: c, sequence: req.Sequence, finished: make(chan struct{})}
	reply, err := c.retry(ctx, req)
	if err != nil {
		lease.ReleaseAfter(ctx, nil)
		return nil, "", err
	}
	if _, valid := api.LimitsFor(reply.Plan); !valid || !reply.Plan.TailEnabled() {
		lease.ReleaseAfter(ctx, nil)
		return nil, "", errors.New("function lifetime: missing authoritative plan")
	}
	return lease, reply.Plan, nil
}

type Lease struct {
	client   *Client
	sequence uint64
	once     sync.Once
	finished chan struct{}
	err      error
}

// ReleaseAfter hands terminal ownership to the cleanup fence. It never uses
// request cancellation or pipe EOF as evidence that execution stopped.
func (l *Lease) ReleaseAfter(parent context.Context, done <-chan struct{}) {
	if l == nil {
		return
	}
	l.once.Do(func() {
		go func() {
			defer close(l.finished)
			if done != nil {
				<-done
			}
			ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), 2*RPCTimeout)
			defer cancel()
			_, err := l.client.retry(ctx, Request{Version: Version, Kind: Finish, Sequence: l.sequence})
			l.err = err
			if err != nil && l.client.ObserveError != nil {
				l.client.ObserveError(err)
			}
		}()
	})
}

// Wait observes the host completion acknowledgement, after ReleaseAfter was
// called. Checkpoint callbacks must wait before acknowledging their HTTP hook.
func (l *Lease) Wait(ctx context.Context) error {
	if l == nil {
		return nil
	}
	select {
	case <-l.finished:
		return l.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *Client) retry(ctx context.Context, req Request) (Reply, error) {
	select {
	case c.calls <- struct{}{}:
	case <-ctx.Done():
		return Reply{}, ctx.Err()
	}
	defer func() { <-c.calls }()
	var reply Reply
	var err error
	for range 2 {
		reply, err = Call(ctx, c.dial, req)
		if err == nil {
			if reply.Status == Unavailable {
				err = errors.New("function lifetime: transport acknowledgement unavailable")
				continue
			}
			if reply.Status != OK {
				return reply, fmt.Errorf("function lifetime: reservation %s", reply.Status)
			}
			return reply, nil
		}
		if ctx.Err() != nil {
			break
		}
	}
	return reply, err
}

// Call performs one bounded, framed round trip. It requires an explicit host
// reply; a successful guest-init write or EOF never acknowledges ownership.
func Call(ctx context.Context, dial Dial, req Request) (Reply, error) {
	ctx, cancel := context.WithTimeout(ctx, RPCTimeout)
	defer cancel()
	conn, err := dial(ctx)
	if err != nil {
		return Reply{}, err
	}
	defer func() { _ = conn.Close() }()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	if deadline, ok := ctx.Deadline(); ok {
		if err := conn.SetDeadline(deadline); err != nil {
			return Reply{}, err
		}
	}
	if err := Write(conn, req); err != nil {
		return Reply{}, err
	}
	return ReadReply(conn)
}
