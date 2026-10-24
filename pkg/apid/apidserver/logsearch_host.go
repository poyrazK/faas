package apidserver

import (
	"log/slog"
	"net/http"

	"github.com/onebox-faas/faas/pkg/apid/logsearch"
	"github.com/onebox-faas/faas/pkg/state"
)

// logSearchHost adapts *server to logsearch.Host (ADR-20261018083536),
// reading fields at call time so test store swaps take effect.
type logSearchHost struct{ s *server }

var _ logsearch.Host = logSearchHost{}

func (h logSearchHost) LoadApp(w http.ResponseWriter, r *http.Request, acct state.Account, slug string) (state.App, bool) {
	return h.s.loadApp(w, r, acct, slug) //nolint:contextcheck // loadApp uses r.Context().
}

func (h logSearchHost) LogEvents() state.LogEventStore { return h.s.store }

func (h logSearchHost) SavedSearches() state.LogSavedSearchStore { return h.s.store }

func (h logSearchHost) Logger() *slog.Logger { return h.s.log }
