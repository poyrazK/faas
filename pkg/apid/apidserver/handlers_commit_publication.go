package apidserver

import (
	"github.com/onebox-faas/faas/pkg/apid/commitapi"
	commitwork "github.com/onebox-faas/faas/pkg/commit"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
)

func (s *server) acceptCommitPublication(w http.ResponseWriter, r *http.Request, acct state.Account, source string, event commitwork.Event) {
	commitapi.AcceptCommitPublication(s.store, s.notif.Notify, w, r, acct, source, event)
}
