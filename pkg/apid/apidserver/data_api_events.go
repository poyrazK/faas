package apidserver

import (
	"context"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/dataapievents"
	"github.com/onebox-faas/faas/pkg/state"
)

// reportDataAPIChangeEvents is the private change hooks route
// (ADR-20261022121642); workload identity only.
func (s *server) reportDataAPIChangeEvents(w http.ResponseWriter, r *http.Request) {
	dataapievents.Handler{Verifier: s.workloadVerifier, Store: s.store, Publisher: &s.appEvents,
		Admit: s.admitDataAPIChangeEvents}.ServeHTTP(w, r)
}

// admitDataAPIChangeEvents holds change hooks to the gates every other
// publication path applies: the events pause, platform-wide or for the app's
// account (ADR-20261027125625), and the account's publish rate
// (ADR-20261026140000). The route belongs to the data-api capability, so
// neither reaches it otherwise, and any app can mint its workload token
// (bug hunt 36).
func (s *server) admitDataAPIChangeEvents(_ context.Context, acct state.Account, app state.App, events int) *api.Problem {
	if problem := s.eventsPausedProblem(acct, app); problem != nil {
		return problem
	}
	return s.appEvents.PublishRateProblem(acct, events)
}
