package sched

import (
	"context"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// ReportFrameworkReady persists the first readiness receipt under the same app
// lock used by park/destroy. Replays do not move the promotion age floor.
func (e *Engine) ReportFrameworkReady(ctx context.Context, id string) error {
	ins, err := e.lockedRunning(ctx, id)
	if err != nil {
		return err
	}
	if ins == nil {
		return fmt.Errorf("%w: framework-ready instance is not running", state.ErrConflict)
	}
	defer e.unlockApp(ins.AppID)
	if err := ctx.Err(); err != nil {
		return err
	}
	if ins.FrameworkReadyAt != nil {
		return nil
	}
	return e.store.SetInstanceFrameworkReadyAt(ctx, id, time.Now())
}
