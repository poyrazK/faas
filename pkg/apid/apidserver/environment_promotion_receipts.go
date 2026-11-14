package apidserver

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/apid/promotion"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) getEnvironmentPromotionReceipt(w http.ResponseWriter, r *http.Request, acct state.Account) {
	promotion.ReadEnvironmentReceipt(w, r, acct.ID, s.store)
}
