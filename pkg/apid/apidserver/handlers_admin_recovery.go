// adr: 1524
package apidserver

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/operatorrecovery"
	"github.com/onebox-faas/faas/pkg/state"
)

// Node-intent responses share the same advisory polling horizon.
const operatorIntentPollHorizon = api.OperatorRecoveryPollHorizon

func (s *server) submitOperatorRecovery(w http.ResponseWriter, r *http.Request, acct state.Account, kind state.OperatorIntentKind) {
	store, _ := s.store.(state.OperatorRecoverySubmissionStore)
	service := operatorrecovery.Service{Store: store, Notifier: s.notif, Log: s.log, Audit: s.audit}
	service.Submit(w, r, acct, kind)
}
