package gateway

import (
	"fmt"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/realtime"
)

// stampRealtimeHostAccount names, for realtimed, the account of the app the
// request's host is routed to (realtime.HostAccountHeader), replacing any
// client copy; realtimed answers 404 for another account's endpoint. Without
// it one tenant's endpoints answered on every tenant's hosts, inside their
// origin and connect-src policies. It only resolves the host: the managed
// namespace still skips wake, admission, edge rules and the drain tracker,
// so a quiet socket holds nothing (ADR-156, amendment of 2026-11-02). It
// reports false after writing the refusal for a host no app is routed to.
func (h *Handler) stampRealtimeHostAccount(w http.ResponseWriter, r *http.Request) bool {
	r.Header.Del(realtime.HostAccountHeader)
	appHost, _ := SplitHostSelectorUnder(hostname(r.Host), h.appsSuffix, h.selectorSuffixes)
	//nolint:contextcheck // request ctx is the canonical inbound ctx at the HTTP handler boundary.
	app, found, err := h.lookupAppPolicy(r, appHost)
	switch {
	case err != nil:
		api.WriteProblem(w, api.ErrRealtimeUnavailable())
		return false
	case !found || app.AccountID == "":
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound,
			"No such app", fmt.Sprintf("no app is routed to %q", appHost)))
		return false
	}
	r.Header.Set(realtime.HostAccountHeader, app.AccountID)
	return true
}
