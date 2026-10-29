package apidserver

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/onebox-faas/faas/pkg/apid/appevents"
	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/state"
)

// appEventsHost adapts *server to appevents.Host (ADR-977). It reads server
// fields at call time, so tests that swap s.store behave exactly as before.
type appEventsHost struct{ s *server }

var _ appevents.Host = appEventsHost{}

func (h appEventsHost) LoadApp(w http.ResponseWriter, r *http.Request, acct state.Account, slug string) (state.App, bool) {
	return h.s.loadApp(w, r, acct, slug)
}

func (h appEventsHost) WorkflowRuntimeEnabled() bool { return h.s.workflowRuntimeEnabled }

func (h appEventsHost) Store() state.Store { return h.s.store }

func (h appEventsHost) Logger() *slog.Logger { return h.s.log }

func (h appEventsHost) EmitAudit(ctx context.Context, kind string, accountID *string, data map[string]any) {
	h.s.audit.Emit(ctx, kind, accountID, data)
}

func (h appEventsHost) Notify(ctx context.Context, channel, payload string) error {
	return h.s.notif.Notify(ctx, channel, payload)
}

func (h appEventsHost) PublishLimiter() *events.PublishLimiter { return &h.s.eventPublishLimiter }
