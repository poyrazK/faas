package workerpool

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
)

var ErrPoolBusy = errors.New("workerpool: workers have not quiesced")

// Batch pins every loaded interpreter for a lifecycle callback. New ordinary
// acquisitions wait until all callback execution/cancellation fences settle.
type Batch struct {
	Lines []*Lines
	pool  *pool
	once  sync.Once
	done  chan struct{}
}

func (b *Batch) Released() <-chan struct{} { return b.done }

func (b *Batch) Close(healthy []bool) {
	b.once.Do(func() {
		for i, lines := range b.Lines {
			lines.Close(i < len(healthy) && healthy[i])
		}
		go func() {
			for _, lines := range b.Lines {
				<-lines.Released()
			}
			b.pool.mu.Lock()
			b.pool.broadcasting = false
			b.pool.notifyLocked()
			b.pool.mu.Unlock()
			close(b.done)
		}()
	})
}

func OpenAllIfSupported(ctx context.Context, spec Spec, request any) (*Batch, error) {
	if !supportsPersistentProtocol(spec) {
		return nil, errors.New("workerpool: lifecycle broadcast requires a generated adapter")
	}
	return poolFor(spec).openAll(ctx, request)
}

func (p *pool) openAll(ctx context.Context, request any) (*Batch, error) {
	payload, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	if p.live == 0 {
		p.mu.Unlock()
		w, err := p.acquire(ctx)
		if err != nil {
			return nil, err
		}
		p.release(w, true)
		p.mu.Lock()
	}
	if p.broadcasting || p.starting > 0 || len(p.idle) > 0 || p.live != len(p.shared) {
		p.mu.Unlock()
		return nil, ErrPoolBusy
	}
	for _, w := range p.shared {
		if w.active != 0 || w.gone || !w.completion {
			p.mu.Unlock()
			return nil, ErrPoolBusy
		}
	}
	workers := append([]*worker(nil), p.shared...)
	p.broadcasting = true
	for _, w := range workers {
		w.active++
	}
	p.mu.Unlock()
	b := &Batch{pool: p, done: make(chan struct{})}
	for i, w := range workers {
		lines, err := p.openShared(ctx, w, payload)
		if err != nil {
			for _, unsent := range workers[i+1:] {
				p.releaseSlot(unsent)
			}
			b.Close(nil)
			return b, err
		}
		b.Lines = append(b.Lines, lines)
	}
	return b, nil
}
