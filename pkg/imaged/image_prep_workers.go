package imaged

import (
	"context"
	"sync"
)

// imagePrepWorkers let the loop prepare several deployments' images at once
// (ADR-20261029174438). Without them the loop prepared one deployment at a
// time, so a cache-hit build waited behind another deployment's scan. The
// handler already runs beside the durable replay worker, and each preparation
// holds its deployment's activation lock, so the workers add parallelism, not
// new ownership.
type imagePrepWorkers struct {
	slots    chan struct{}
	mu       sync.Mutex
	inFlight map[string]struct{}
	wg       sync.WaitGroup
}

// newImagePrepWorkers returns nil for one worker or fewer: the loop then
// prepares inline, as before.
func newImagePrepWorkers(n int) *imagePrepWorkers {
	if n <= 1 {
		return nil
	}
	return &imagePrepWorkers{slots: make(chan struct{}, n), inFlight: map[string]struct{}{}}
}

// start runs work for deploymentID on its own goroutine once a slot is free,
// blocking the caller while every slot is busy, as inline preparation did. It
// runs nothing and returns false when this process is already preparing that
// deployment, or when ctx ends first. Recovery re-reads the durable rows every
// two seconds, and the outbox replays an unacknowledged notification.
func (w *imagePrepWorkers) start(ctx context.Context, deploymentID string, work func(context.Context)) bool {
	w.mu.Lock()
	if _, busy := w.inFlight[deploymentID]; busy {
		w.mu.Unlock()
		return false
	}
	w.inFlight[deploymentID] = struct{}{}
	w.mu.Unlock()
	select {
	case w.slots <- struct{}{}:
	case <-ctx.Done():
		w.finish(deploymentID)
		return false
	}
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		defer w.finish(deploymentID)
		defer func() { <-w.slots }()
		work(ctx)
	}()
	return true
}

func (w *imagePrepWorkers) finish(deploymentID string) {
	w.mu.Lock()
	delete(w.inFlight, deploymentID)
	w.mu.Unlock()
}

// wait returns once every started preparation has returned.
func (w *imagePrepWorkers) wait() {
	if w != nil {
		w.wg.Wait()
	}
}
