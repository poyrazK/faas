package sched

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/db"
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

// frameworkReadyRelay is the NotifyFrameworkReadyRelayed payload.
type frameworkReadyRelay struct {
	InstanceID string `json:"instance_id"`
	AppID      string `json:"app_id"`
}

// RelayFrameworkReady publishes a framework-ready receipt for the schedd that
// owns the instance's app. The caller has established that this schedd hosts
// the instance but does not own the app (Hunt 14 H14-104).
func (e *Engine) RelayFrameworkReady(ctx context.Context, instanceID, appID string) error {
	if instanceID == "" || appID == "" {
		return errors.New("sched: framework-ready relay requires instance_id and app_id")
	}
	if e.notif == nil {
		return errors.New("sched: relay framework ready: no notifier configured")
	}
	payload, err := json.Marshal(frameworkReadyRelay{InstanceID: instanceID, AppID: appID})
	if err != nil {
		return fmt.Errorf("sched: relay framework ready: encode: %w", err)
	}
	if err := e.notif.Notify(ctx, db.NotifyFrameworkReadyRelayed, string(payload)); err != nil {
		return fmt.Errorf("sched: relay framework ready %s: %w", instanceID, err)
	}
	return nil
}

// HandleRelayedFrameworkReady records a relayed receipt when this schedd owns
// the instance's app and discards it otherwise. Ownership comes from the
// stored rows, not the payload. An instance that is gone or no longer running
// needs no stamp.
func (e *Engine) HandleRelayedFrameworkReady(ctx context.Context, payload string) error {
	var r frameworkReadyRelay
	if err := json.Unmarshal([]byte(payload), &r); err != nil {
		return fmt.Errorf("sched: decode relayed framework ready: %w", err)
	}
	if r.InstanceID == "" {
		return errors.New("sched: relayed framework ready has no instance_id")
	}
	ins, err := e.store.InstanceByID(ctx, r.InstanceID)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return nil
		}
		return fmt.Errorf("sched: relayed framework ready: load instance %s: %w", r.InstanceID, err)
	}
	app, err := e.store.AppByID(ctx, ins.AppID)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return nil
		}
		return fmt.Errorf("sched: relayed framework ready: load app %s: %w", ins.AppID, err)
	}
	if !e.ownsApp(app) {
		return nil
	}
	if err := e.ReportFrameworkReady(ctx, r.InstanceID); err != nil && !errors.Is(err, state.ErrConflict) && !errors.Is(err, state.ErrNotFound) {
		return err
	}
	return nil
}
