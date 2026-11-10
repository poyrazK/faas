package workerpool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// BroadcastResult is one worker's answer to a broadcast request.
type BroadcastResult struct {
	Status int `json:"status"`
}

// ErrBroadcastUnsupported means the handler is not a generated adapter, so it
// cannot run a function's snapshot hooks.
var ErrBroadcastUnsupported = errors.New("workerpool: snapshot hooks require a generated adapter")

// BroadcastIfSupported sends request once to every live worker of spec's pool
// and returns each worker's first response line, decoded
// (ADR-20261110190000 §3). It waits for the pool to quiesce first, and new
// requests wait until it finishes, so a hook never runs beside a request.
// Unlike OpenAllIfSupported it needs neither execution completion nor shared
// workers: exclusive one-slot workers take part too. A pool with no live
// worker has nothing to run and returns no results.
func BroadcastIfSupported(ctx context.Context, spec Spec, request any) ([]BroadcastResult, error) {
	if !supportsPersistentProtocol(spec) {
		return nil, ErrBroadcastUnsupported
	}
	return poolFor(spec).broadcast(ctx, request)
}

func (p *pool) broadcast(ctx context.Context, request any) ([]BroadcastResult, error) {
	payload, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("workerpool: encode broadcast: %w", err)
	}
	exclusive, shared, err := p.claimAll(ctx)
	if err != nil {
		return nil, err
	}
	defer func() {
		p.mu.Lock()
		p.broadcasting = false
		p.notifyLocked()
		p.mu.Unlock()
	}()
	for _, w := range append(append([]*worker(nil), exclusive...), shared...) {
		if !w.functionHooks {
			for _, idle := range exclusive {
				p.release(idle, true)
			}
			for _, busy := range shared {
				p.releaseSlot(busy)
			}
			return nil, ErrBroadcastUnsupported
		}
	}
	results := make([]BroadcastResult, 0, len(exclusive)+len(shared))
	var failed error
	for i, w := range exclusive {
		if failed != nil {
			p.release(w, true)
			continue
		}
		line := append(append([]byte(nil), payload...), '\n')
		if _, err := w.stdin.Write(line); err != nil {
			p.release(w, false)
			failed = fmt.Errorf("workerpool: broadcast to worker %d: %w", i+1, err)
			continue
		}
		lines := &Lines{ctx: ctx, pool: p, w: w, released: make(chan struct{})}
		result, err := readBroadcast(ctx, lines)
		lines.Close(err == nil)
		if err != nil {
			failed = fmt.Errorf("workerpool: broadcast to worker %d: %w", i+1, err)
			continue
		}
		results = append(results, result)
	}
	for i, w := range shared {
		if failed != nil {
			p.releaseSlot(w)
			continue
		}
		lines, err := p.openShared(ctx, w, payload)
		if err != nil {
			failed = fmt.Errorf("workerpool: broadcast to shared worker %d: %w", i+1, err)
			continue
		}
		result, err := readBroadcast(ctx, lines)
		lines.Close(err == nil)
		if err != nil {
			failed = fmt.Errorf("workerpool: broadcast to shared worker %d: %w", i+1, err)
			continue
		}
		results = append(results, result)
	}
	if failed != nil {
		return nil, failed
	}
	return results, nil
}

// claimAll waits until no request holds a worker, then takes every idle
// exclusive worker and a slot on every shared one, and marks the pool as
// broadcasting so acquisitions wait.
func (p *pool) claimAll(ctx context.Context) (exclusive, shared []*worker, err error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		p.mu.Lock()
		if !p.broadcasting && p.starting == 0 && p.quiescentLocked() {
			exclusive, p.idle = p.idle, nil
			for _, w := range p.shared {
				if !w.gone {
					w.active++
					shared = append(shared, w)
				}
			}
			p.broadcasting = true
			p.mu.Unlock()
			return exclusive, shared, nil
		}
		changed := p.changed
		p.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		case <-changed:
		}
	}
}

// quiescentLocked reports whether every live worker is idle: exclusive ones in
// idle and shared ones with no request in flight.
func (p *pool) quiescentLocked() bool {
	if len(p.idle)+len(p.shared) != p.live {
		return false
	}
	for _, w := range p.shared {
		if w.active != 0 {
			return false
		}
	}
	return true
}

func readBroadcast(ctx context.Context, lines *Lines) (BroadcastResult, error) {
	line, err := lines.NextContext(ctx)
	if err != nil {
		return BroadcastResult{}, err
	}
	var result BroadcastResult
	if err := json.Unmarshal(line, &result); err != nil {
		return BroadcastResult{}, fmt.Errorf("decode response: %w", err)
	}
	return result, nil
}
