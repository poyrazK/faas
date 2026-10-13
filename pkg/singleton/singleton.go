// Package singleton runs control-plane work on exactly one node of a cell
// (ADR-1152 §4).
//
// Every control node runs the same daemons. Work that must not run twice (the
// central scheduler, meterd's sampling and billing) is guarded by a named
// lease in the cell's Postgres, and only the holder runs it. The holder renews
// the lease; a standby takes over only after the lease has expired by the
// database clock. The holder stops its own work earlier than that, timed from
// its own last successful renewal, so two holders never overlap even when the
// old holder is partitioned from the database.
//
// Two ways to use it:
//   - Runner.Run runs a function while the lease is held and competes again
//     after losing it (in-process work such as a loop).
//   - Runner.Acquire blocks as a standby until the lease is held and returns
//     a Holding whose context ends when the lease is lost (a whole daemon
//     that exits on loss and restarts as a standby).
//
// Work receives the lease epoch. Writes whose duplication would be harmful can
// pass it to state.ControlPlaneLeaseHeld in the same transaction to refuse a
// holder that has already been replaced.
package singleton

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// ErrLeaseLost is the cause of a Holding's context when the lease was lost
// rather than released.
var ErrLeaseLost = errors.New("singleton: lease lost")

// Timings controls how a Runner holds its lease. The zero value means the
// production values from pkg/api/limits.go.
type Timings struct {
	TTL          time.Duration // lease lifetime per renewal, whole seconds
	RenewEvery   time.Duration // how often the holder renews, and a standby retries
	SafetyMargin time.Duration // how long before TTL the holder stops its work
}

func (t Timings) withDefaults() Timings {
	if t.TTL == 0 {
		t.TTL = api.ControlPlaneLeaseTTL
	}
	if t.RenewEvery == 0 {
		t.RenewEvery = api.ControlPlaneLeaseRenewInterval
	}
	if t.SafetyMargin == 0 {
		t.SafetyMargin = api.ControlPlaneLeaseSafetyMargin
	}
	return t
}

func (t Timings) validate() error {
	if t.TTL < time.Second || t.TTL%time.Second != 0 {
		return errors.New("singleton: lease TTL must be whole seconds")
	}
	if t.RenewEvery <= 0 || t.SafetyMargin <= 0 || t.RenewEvery >= t.TTL-t.SafetyMargin {
		return errors.New("singleton: renew interval must be shorter than TTL minus the safety margin")
	}
	return nil
}

// Work is a singleton's body for Run. It runs until ctx is cancelled (lease
// lost or shutdown) and should return promptly when it is. Returning early
// gives the lease up so another node can take it.
type Work func(ctx context.Context, epoch int64) error

// Runner competes for one named lease.
type Runner struct {
	store    state.ControlPlaneLeaseStore
	name     string
	holder   string
	timings  Timings
	log      *slog.Logger
	onChange func(held bool, epoch int64)
}

// New returns a Runner for the lease name. holder identifies this process and
// must be unique per process start; NewHolderID provides one.
func New(store state.ControlPlaneLeaseStore, name, holder string, timings Timings, log *slog.Logger) (*Runner, error) {
	if store == nil || name == "" || holder == "" {
		return nil, errors.New("singleton: New needs a store, a lease name and a holder ID")
	}
	timings = timings.withDefaults()
	if err := timings.validate(); err != nil {
		return nil, err
	}
	if log == nil {
		log = slog.Default()
	}
	return &Runner{store: store, name: name, holder: holder, timings: timings,
		log: log.With("lease", name, "holder", holder)}, nil
}

// OnChange registers fn to run whenever this process gains or loses the
// lease (for a held/epoch gauge). fn must not block.
func (r *Runner) OnChange(fn func(held bool, epoch int64)) *Runner {
	r.onChange = fn
	return r
}

// NewHolderID returns hostname/pid/random, unique per process start, so a
// restarted process never renews its predecessor's epoch.
func NewHolderID() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "unknown-host"
	}
	var b [6]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("%s/%d/%s", host, os.Getpid(), hex.EncodeToString(b[:]))
}

// Run competes for the lease until ctx ends and runs work whenever it holds
// it. work's context ends when the lease is lost or ctx ends; Run waits for
// work to return before competing again. When work returns while the lease
// is still held, Run releases it and backs off one renewal interval so
// another node can take over.
func (r *Runner) Run(ctx context.Context, work Work) error {
	for {
		h, err := r.Acquire(ctx)
		if err != nil {
			return err
		}
		werr := work(h.Context(), h.Epoch())
		lost := h.Lost()
		h.Release()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if lost {
			continue // a renewal may already hold a new epoch
		}
		if werr != nil && !errors.Is(werr, context.Canceled) {
			r.log.Warn("singleton: work returned an error; lease released", "epoch", h.Epoch(), "err", werr)
		}
		if !sleepCtx(ctx, r.timings.RenewEvery) {
			return ctx.Err()
		}
	}
}

// Acquire blocks as a standby until this process holds the lease or ctx
// ends. The returned Holding renews the lease in the background; its
// context ends with cause ErrLeaseLost when the lease cannot be renewed
// before the local deadline or another holder took it. Call Release when
// done.
func (r *Runner) Acquire(ctx context.Context) (*Holding, error) {
	waiting := false
	for {
		start := time.Now()
		lease, held, err := r.store.AcquireControlPlaneLease(ctx, r.name, r.holder, r.timings.TTL)
		switch {
		case ctx.Err() != nil:
			return nil, ctx.Err()
		case err != nil:
			r.log.Warn("singleton: lease acquire failed", "err", err)
		case held:
			return r.startHolding(ctx, lease.Epoch, start), nil
		case !waiting:
			waiting = true
			r.log.Info("singleton: lease held by another node; waiting as standby")
		}
		if !sleepCtx(ctx, r.timings.RenewEvery) {
			return nil, ctx.Err()
		}
	}
}

// Holding is a held lease.
type Holding struct {
	r      *Runner
	epoch  int64
	ctx    context.Context
	cancel context.CancelCauseFunc
	stop   chan struct{}
	done   chan struct{}

	mu       sync.Mutex
	lost     bool
	released bool
}

// Context ends when the lease is lost (cause ErrLeaseLost), released, or the
// parent context ends.
func (h *Holding) Context() context.Context { return h.ctx }

// Epoch is the lease epoch this holding runs under.
func (h *Holding) Epoch() int64 { return h.epoch }

// Lost reports whether the lease was lost rather than released.
func (h *Holding) Lost() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.lost
}

// Release stops renewing, ends the holding's context, and gives the lease up
// in the database if it is still held, so a standby need not wait for
// expiry. Safe to call more than once.
func (h *Holding) Release() {
	h.mu.Lock()
	if h.released {
		h.mu.Unlock()
		return
	}
	h.released = true
	h.mu.Unlock()
	close(h.stop)
	<-h.done
	h.cancel(context.Canceled)
	if !h.Lost() {
		h.r.release(h.epoch)
		h.r.changed(false, h.epoch)
	}
}

func (r *Runner) startHolding(parent context.Context, epoch int64, acquiredStart time.Time) *Holding {
	ctx, cancel := context.WithCancelCause(parent)
	h := &Holding{r: r, epoch: epoch, ctx: ctx, cancel: cancel, stop: make(chan struct{}), done: make(chan struct{})}
	r.log.Info("singleton: lease acquired", "epoch", epoch)
	r.changed(true, epoch)
	go h.renew(acquiredStart)
	return h
}

// localDeadline is when this holder must have stopped its work: TTL minus the
// safety margin after the start of the renewal that succeeded. Timing from the
// request start (not its reply) keeps the bound conservative however slow the
// round trip was.
func (r *Runner) localDeadline(renewStart time.Time) time.Time {
	return renewStart.Add(r.timings.TTL - r.timings.SafetyMargin)
}

func (h *Holding) renew(acquiredStart time.Time) {
	defer close(h.done)
	r := h.r
	deadline := time.NewTimer(time.Until(r.localDeadline(acquiredStart)))
	defer deadline.Stop()
	ticker := time.NewTicker(r.timings.RenewEvery)
	defer ticker.Stop()
	for {
		select {
		case <-h.stop:
			return
		case <-h.ctx.Done():
			return
		case <-deadline.C:
			// No renewal succeeded in time. Another node may take over once
			// the database clock passes expiry, so stop now (self-fence).
			h.lose("lease not renewed before the local deadline")
			return
		case <-ticker.C:
			start := time.Now()
			lease, held, err := r.store.AcquireControlPlaneLease(h.ctx, r.name, r.holder, r.timings.TTL)
			switch {
			case h.ctx.Err() != nil:
				return
			case err != nil:
				r.log.Warn("singleton: lease renewal failed; holding until the local deadline", "epoch", h.epoch, "err", err)
			case !held:
				h.lose("lease taken by another holder")
				return
			case lease.Epoch != h.epoch:
				// Our row expired and this renewal re-acquired it under a new
				// epoch. Work started under the old epoch must not continue.
				h.lose("lease expired and was re-acquired under a new epoch")
				return
			default:
				if !deadline.Stop() {
					select {
					case <-deadline.C:
					default:
					}
				}
				deadline.Reset(time.Until(r.localDeadline(start)))
			}
		}
	}
}

func (h *Holding) lose(reason string) {
	h.mu.Lock()
	h.lost = true
	h.mu.Unlock()
	h.r.log.Warn("singleton: lease lost; stopping work", "epoch", h.epoch, "reason", reason)
	h.cancel(ErrLeaseLost)
	h.r.changed(false, h.epoch)
}

func (r *Runner) release(epoch int64) {
	ctx, cancel := context.WithTimeout(context.Background(), r.timings.RenewEvery)
	defer cancel()
	if err := r.store.ReleaseControlPlaneLease(ctx, r.name, r.holder, epoch); err != nil {
		r.log.Warn("singleton: lease release failed; it will expire instead", "epoch", epoch, "err", err)
	}
}

func (r *Runner) changed(held bool, epoch int64) {
	if r.onChange != nil {
		r.onChange(held, epoch)
	}
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
