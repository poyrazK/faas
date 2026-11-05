package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/capflags"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/runtimeconfig"
	"github.com/onebox-faas/faas/pkg/state"
)

// eventsCapability is the product capability whose flag pauses event
// publishing (ADR-20261027125625).
const eventsCapability = "events"

// guestEventSubjectTTL bounds how long a node reuses an app's flag subject,
// as schedd does for the work it holds.
const guestEventSubjectTTL = 30 * time.Second

// errGuestEventsPaused refuses an in-guest publish while events is off for
// the app's account or platform-wide. The guest answers it as a retryable
// 503, so the runtime keeps and resends the event.
var errGuestEventsPaused = errors.New("event publishing is paused for this account; retry the same event later")

// guestEventGate decides the events capability for in-guest publishes from
// the platform flags this node watches. A nil gate allows everything.
type guestEventGate struct {
	enabled  func(capability string, subject platformflags.Subject) bool
	mu       sync.Mutex
	subjects map[string]guestEventSubject
}

type guestEventSubject struct {
	subject platformflags.Subject
	at      time.Time
}

type guestEventApps interface {
	AppByID(context.Context, string) (state.App, error)
}

// startGuestEventGate keeps a platform-flag set current through the
// runtime-config watcher and returns the gate that reads it.
func startGuestEventGate(ctx context.Context, store state.Store, pool *pgxpool.Pool, log *slog.Logger) (*guestEventGate, error) {
	flags := platformflags.NewSet(capflags.Bootstrap(os.Getenv))
	decider, err := capflags.Load(flags.Snapshot)
	if err != nil {
		return nil, fmt.Errorf("vmmd: load capability catalog: %w", err)
	}
	keys, apply := runtimeconfig.WithPlatformFlags(nil, nil, flags)
	watcher := runtimeconfig.New(store, pool, keys, apply, log)
	watcher.Daemon = "vmmd" // ADR-1700 apply receipts
	if err := watcher.Reconcile(ctx); err != nil {
		log.Warn("vmmd: initial platform flag reconcile failed", "err", err)
	}
	go func() {
		if err := watcher.Run(ctx); err != nil && !runtimeconfig.IsContextDone(err) {
			log.Error("vmmd: platform flag watcher exited", "err", err)
		}
	}()
	return &guestEventGate{enabled: decider.Enabled}, nil
}

// allow refuses the publish while events is off for the account that owns
// appID. A failed lookup allows it, so a transient store error never stops a
// node's publishers; apid and schedd still decide the same flag.
func (g *guestEventGate) allow(ctx context.Context, store any, appID string) error {
	if g == nil || g.enabled == nil {
		return nil
	}
	subject, ok := g.subject(ctx, store, appID)
	if !ok {
		return nil
	}
	if !g.enabled(eventsCapability, subject) {
		return errGuestEventsPaused
	}
	return nil
}

func (g *guestEventGate) subject(ctx context.Context, store any, appID string) (platformflags.Subject, bool) {
	g.mu.Lock()
	cached, ok := g.subjects[appID]
	g.mu.Unlock()
	if ok && time.Since(cached.at) < guestEventSubjectTTL {
		return cached.subject, true
	}
	apps, ok := store.(guestEventApps)
	if !ok {
		return platformflags.Subject{}, false
	}
	app, err := apps.AppByID(ctx, appID)
	if err != nil {
		return platformflags.Subject{}, false
	}
	owners, ok := store.(state.AppOwnerReader)
	if !ok {
		return platformflags.Subject{}, false
	}
	acct, err := state.AppResourceAccount(ctx, owners, app)
	if err != nil {
		return platformflags.Subject{}, false
	}
	subject := platformflags.Subject{AccountID: acct.ID, Plan: string(acct.Plan), OrgID: app.OrgID}
	g.mu.Lock()
	if g.subjects == nil || len(g.subjects) >= 10000 {
		g.subjects = map[string]guestEventSubject{}
	}
	g.subjects[appID] = guestEventSubject{subject: subject, at: time.Now()}
	g.mu.Unlock()
	return subject, true
}
