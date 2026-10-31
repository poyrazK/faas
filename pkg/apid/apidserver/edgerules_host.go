package apidserver

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/onebox-faas/faas/pkg/apid/edgerules"
	"github.com/onebox-faas/faas/pkg/promql"
	"github.com/onebox-faas/faas/pkg/state"
)

// edgeRulesHost adapts *server to edgerules.Host (ADR-977). It reads server
// fields at call time, so tests that swap s.store, s.notif or s.promqlClient
// after construction behave exactly as they did before the lift.
type edgeRulesHost struct{ s *server }

var _ edgerules.Host = edgeRulesHost{}

func (h edgeRulesHost) LoadApp(w http.ResponseWriter, r *http.Request, acct state.Account, slug string) (state.App, bool) {
	return h.s.loadApp(w, r, acct, slug)
}

func (h edgeRulesHost) Store() state.Store { return h.s.store }

func (h edgeRulesHost) Logger() *slog.Logger { return h.s.log }

func (h edgeRulesHost) EmitAudit(ctx context.Context, kind string, accountID *string, data map[string]any) {
	h.s.audit.Emit(ctx, kind, accountID, data)
}

func (h edgeRulesHost) Notifier() edgerules.Notifier { return h.s.notif }

func (h edgeRulesHost) PromQL() *promql.Client { return h.s.promqlClient }

func (h edgeRulesHost) PlatformFlagEnabled(flagKey string, acct state.Account) bool {
	return h.s.platformFlagEnabled(flagKey, acct)
}
