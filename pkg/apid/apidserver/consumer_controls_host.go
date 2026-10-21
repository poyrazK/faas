package apidserver

import (
	"context"
	"net/http"

	"github.com/onebox-faas/faas/pkg/apid/consumercontrols"
	"github.com/onebox-faas/faas/pkg/state"
)

// consumerControlsHost adapts *server to consumercontrols.Host (ADR-982,
// ADR-1314, ADR-1315).
type consumerControlsHost struct{ s *server }

var _ consumercontrols.Host = consumerControlsHost{}

func (h consumerControlsHost) Store() state.Store { return h.s.store }

func (h consumerControlsHost) ConsumerRoute(w http.ResponseWriter, r *http.Request, acct state.Account) (state.App, state.APIConsumer, bool) {
	app, consumer, _, ok := h.s.apiConsumerUsageStatementStore(w, r, acct)
	return app, consumer, ok
}

func (h consumerControlsHost) AppRoute(w http.ResponseWriter, r *http.Request, acct state.Account) (state.App, bool) {
	if !h.s.consumerFeatureAllowed(w, acct) {
		return state.App{}, false
	}
	return h.s.consumerApp(w, r, acct)
}

func (h consumerControlsHost) Audit(ctx context.Context, event, accountID string, fields map[string]any) {
	h.s.audit.Emit(ctx, event, &accountID, fields)
}
