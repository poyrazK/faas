package apidserver

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/state"
)

// recordCliAuthRequester stores the address that asked for a CLI login
// code (account audit F10). It is best effort: a code still works without
// it, and the approval page then says the address is unknown.
func (h *cliAuthHandlers) recordCliAuthRequester(r *http.Request, hash []byte) {
	store, ok := h.srv.store.(state.CliAuthRequesterStore)
	if !ok {
		return
	}
	ip := clientIPFromRequest(r)
	if ip == "" {
		return
	}
	if err := store.RecordCliAuthRequester(r.Context(), hash, ip); err != nil {
		h.log.Warn("cli_auth.record_requester", "err", err)
	}
}

// cliAuthRequestView is what the approval page shows about where a code
// came from, next to the address of the browser approving it, so a person
// approving a code someone else sent sees the two differ.
type cliAuthRequestView struct {
	IP        string // "" when unknown
	At        string // UTC, minute precision
	BrowserIP string
	SameIP    bool
}

// cliAuthRequest returns the request facts of a pending code, or nil when
// the store does not keep them.
func (h *cliAuthHandlers) cliAuthRequest(r *http.Request, hash []byte) *cliAuthRequestView {
	store, ok := h.srv.store.(state.CliAuthRequesterStore)
	if !ok {
		return nil
	}
	req, err := store.CliAuthRequester(r.Context(), hash)
	if err != nil {
		return nil
	}
	browser := clientIPFromRequest(r)
	return &cliAuthRequestView{
		IP:        req.IP,
		At:        req.CreatedAt.UTC().Format("2006-01-02 15:04 UTC"),
		BrowserIP: browser,
		SameIP:    req.IP != "" && req.IP == browser,
	}
}
