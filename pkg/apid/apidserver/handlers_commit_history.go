package apidserver

import (
	"github.com/onebox-faas/faas/pkg/apid/commitapi"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
)

func (s *server) listCommitPublications(w http.ResponseWriter, r *http.Request, acct state.Account) {
	commitapi.ListCommitPublications(s.store, w, r, acct)
}
