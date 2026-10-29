// adr: 191 — explicit app wakes and runtime-config restarts leave Loop.Run's
// select goroutine and claim their durable outbox row (amendment 2026-10-26).
package sched

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/logsanitize"
)

// explicitWakeConsumer names the claims this delivery takes in
// notification_outbox, the same consumer as schedd's replay worker.
const explicitWakeConsumer = "schedd"

// dispatchExplicitWake hands an app_wake or runtime_config_restart delivery
// to the work pool. Either may boot a VM for the whole wake budget. Run on
// the select goroutine, each one stopped the reaper, cron, the watchdog and
// every other notification on this schedd for that long, and N explicit
// wakes took N boots end to end.
//
// A repeated delivery of one outbox row coalesces. A delivery the pool
// cannot take stays pending in the outbox; the replay worker delivers it
// after the wakeup grace period.
func (l *Loop) dispatchExplicitWake(ctx context.Context, n db.Notification) {
	kind := workAppWake
	if n.Channel == db.NotifyRuntimeConfigRestart {
		kind = workRuntimeConfigRestart
	}
	key := ""
	if n.OutboxID > 0 {
		key = strconv.FormatInt(n.OutboxID, 10)
	}
	l.submitWork(kind, key, func() {
		err := l.deliverExplicitWake(ctx, n)
		if err != nil && !errors.Is(err, db.ErrNotificationNotOwned) && ctx.Err() == nil {
			l.log.Warn("sched: explicit wake failed; the notification outbox retries it",
				"channel", n.Channel, "id", n.OutboxID, "err", err)
		}
	})
}

// deliverExplicitWake runs one explicit wake or runtime-config restart.
// Every schedd hears the broadcast and only the app's owner serves it, so a
// peer checks ownership before it claims the row: a peer's claim would fail
// the row into retry backoff while the owner waited. The owner renews its
// claim for the length of the boot, which keeps the replay worker from
// delivering the same row again meanwhile.
func (l *Loop) deliverExplicitWake(ctx context.Context, n db.Notification) error {
	if appID := explicitWakeAppID(n.Payload); appID != "" && !l.ownsExplicitWake(ctx, appID) {
		l.log.Debug("sched: explicit wake left to the owning node", "channel", n.Channel, "app", logsanitize.Field(appID))
		return nil
	}
	handler := func(ctx context.Context, n db.Notification) error {
		err := l.HandleDurableNotification(ctx, n)
		if errors.Is(err, ErrAppOwnedByAnotherNode) {
			// The app moved after the check: release the row unharmed.
			return errors.Join(db.ErrNotificationNotOwned, err)
		}
		return err
	}
	if l.pool == nil || n.OutboxID <= 0 {
		return handler(ctx, n)
	}
	return db.DeliverNotificationForNode(ctx, l.pool, explicitWakeConsumer, "", n, handler)
}

// ownsExplicitWake reports whether this schedd serves appID. An app it
// cannot read is left to the handler, which reports the failure through the
// outbox as before.
func (l *Loop) ownsExplicitWake(ctx context.Context, appID string) bool {
	app, err := l.engine.Store().AppByID(ctx, appID)
	return err != nil || l.engine.OwnsApp(app)
}

// explicitWakeAppID reads the app both payloads carry; a payload it cannot
// read is left to the handler's own decoding error.
func explicitWakeAppID(payload string) string {
	var p struct {
		AppID string `json:"app_id"`
	}
	if json.Unmarshal([]byte(payload), &p) != nil {
		return ""
	}
	return p.AppID
}
