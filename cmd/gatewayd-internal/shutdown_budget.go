// adr: 531
package main

import (
	"context"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// The main run goroutine begins drain before invoking deferred cleanup. Every
// cleanup consumer shares this deadline, including startup-error returns.
type gatewayShutdownBudget struct {
	overallDeadline time.Time
	once            sync.Once
	cleanupDeadline time.Time
}

func (b *gatewayShutdownBudget) beginDrain() {
	b.overallDeadline = time.Now().Add(time.Duration(api.GatewayDrainGraceSeconds+api.GatewayShutdownCleanupGraceSeconds) * time.Second)
}

func (b *gatewayShutdownBudget) context(parent context.Context) (context.Context, context.CancelFunc) {
	b.once.Do(func() {
		deadline := time.Now().Add(time.Duration(api.GatewayShutdownCleanupGraceSeconds) * time.Second)
		if !b.overallDeadline.IsZero() && b.overallDeadline.Before(deadline) {
			deadline = b.overallDeadline
		}
		b.cleanupDeadline = deadline
	})
	return context.WithDeadline(context.WithoutCancel(parent), b.cleanupDeadline)
}
