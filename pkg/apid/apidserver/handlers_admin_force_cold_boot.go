// adr: 1524
package apidserver

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) postForceColdBoot(w http.ResponseWriter, r *http.Request, acct state.Account) {
	s.submitOperatorRecovery(w, r, acct, state.OperatorIntentKindForceColdBoot)
}
