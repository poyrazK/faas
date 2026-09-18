package main

import (
	"context"
	"encoding/json"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

// notifyRuntimeConfigChange wakes vmmd's live-config cache without carrying
// configuration values over pg_notify. Notification delivery is best effort:
// vmmd also expires cache entries shortly, so a dropped wake-up cannot leave a
// stale value indefinitely and must not turn a successful mutation into a 5xx.
func (s *server) notifyRuntimeConfigChange(ctx context.Context, channel string, acct state.Account, app state.App, kind, scope, key string) {
	if s == nil || s.notif == nil {
		return
	}
	payload, err := json.Marshal(db.RuntimeConfigChangedPayload{
		Kind:      kind,
		AppID:     app.ID,
		AccountID: acct.ID,
		Scope:     scope,
		Key:       key,
	})
	if err != nil {
		s.log.Warn("runtime config invalidation payload failed", "channel", channel, "app", app.ID, "err", err)
		return
	}
	if err := s.notif.Notify(ctx, channel, string(payload)); err != nil {
		s.log.Warn("runtime config invalidation notify failed", "channel", channel, "app", app.ID, "err", err)
	}
}
