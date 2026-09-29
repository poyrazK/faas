package sched

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// escalateEgressFanout records a fan-out recycle against the app's account
// and places the ADR-361 account abuse hold on the
// api.EgressFanoutHoldRecycles-th recycle within the hold window.
func (e *Engine) escalateEgressFanout(ctx context.Context, appID string, perMinute, limit int64) error {
	app, err := e.store.AppByID(ctx, appID)
	if err != nil {
		return fmt.Errorf("sched: egress fan-out: load app %s: %w", appID, err)
	}
	if !e.noteEgressFanoutRecycle(app.AccountID, e.clock()) {
		return nil
	}
	detail := map[string]any{"app": appID, "new_destinations_per_min": perMinute, "limit": limit,
		"recycles": api.EgressFanoutHoldRecycles, "window_seconds": api.EgressFanoutHoldWindowSeconds}
	return e.HoldAccountForAbuse(ctx, app.AccountID, state.AccountAbuseHoldEgressFanout, detail)
}

// noteEgressFanoutRecycle appends a recycle at now and reports whether the
// account has reached the hold threshold within the window.
func (e *Engine) noteEgressFanoutRecycle(accountID string, now time.Time) bool {
	e.egressFanoutMu.Lock()
	defer e.egressFanoutMu.Unlock()
	if e.egressFanoutRecycles == nil {
		e.egressFanoutRecycles = make(map[string][]time.Time)
	}
	cutoff := now.Add(-time.Duration(api.EgressFanoutHoldWindowSeconds) * time.Second)
	kept := e.egressFanoutRecycles[accountID][:0]
	for _, at := range e.egressFanoutRecycles[accountID] {
		if at.After(cutoff) {
			kept = append(kept, at)
		}
	}
	kept = append(kept, now)
	e.egressFanoutRecycles[accountID] = kept
	return len(kept) >= api.EgressFanoutHoldRecycles
}

// HoldAccountForAbuse places the ADR-361 account abuse hold and drains the
// account's apps this schedd owns; other owners drain theirs on the next
// reaper pass, and every boot path refuses the account from the moment the
// row commits. A hold that is already in place is left untouched.
func (e *Engine) HoldAccountForAbuse(ctx context.Context, accountID, reason string, detail map[string]any) error {
	holder, ok := e.store.(state.AccountAbuseHoldStore)
	if !ok {
		return fmt.Errorf("sched: abuse hold: store %T cannot hold accounts", e.store)
	}
	now := e.clock()
	placed, err := holder.SetAccountAbuseHold(ctx, accountID, reason, now)
	if err != nil {
		return fmt.Errorf("sched: abuse hold: %w", err)
	}
	if !placed {
		return nil
	}
	e.log.Error("abuse hold: account held for operator review", "account", accountID, "reason", reason, "detail", detail)
	if e.ops != nil {
		e.ops.AccountAbuseHold(reason).Inc()
	}
	payload := map[string]any{"account": accountID, "reason": reason, "held_at": now.Format(time.RFC3339Nano)}
	for k, v := range detail {
		payload[k] = v
	}
	if data, marshalErr := json.Marshal(payload); marshalErr == nil {
		subject := accountID
		if auditErr := e.store.AppendEvent(ctx, "schedd", "accounts.abuse_hold", &subject, data); auditErr != nil {
			e.log.Warn("abuse hold: audit write failed", "account", accountID, "err", auditErr)
		}
	}
	apps, err := e.store.ListApps(ctx, accountID)
	if err != nil {
		return fmt.Errorf("sched: abuse hold: list apps for %s: %w", accountID, err)
	}
	var errs []error
	for _, app := range apps {
		if _, parkErr := e.ParkApp(context.WithoutCancel(ctx), app.ID); parkErr != nil {
			errs = append(errs, fmt.Errorf("park %s: %w", app.ID, parkErr))
		}
	}
	if len(errs) > 0 {
		// The hold is durable; the reaper retries the drain.
		e.log.Warn("abuse hold: drain incomplete", "account", accountID, "err", fmt.Sprint(errs))
	}
	return nil
}

func (e *Engine) clock() time.Time {
	if e.now != nil {
		return e.now()
	}
	return time.Now()
}
