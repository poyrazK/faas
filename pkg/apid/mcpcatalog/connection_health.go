package mcpcatalog

import (
	"context"
	"errors"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
	"strconv"
	"time"
)

func ConnectionHealthHandler(store state.Store) httpkit.AccountHandler {
	return func(w http.ResponseWriter, r *http.Request, account state.Account) {
		source, ok := store.(state.MCPConnectionHealthStore)
		if !ok {
			respond(w, nil, errors.New("connection health unavailable"))
			return
		}
		offset := 0
		var err error
		if raw := r.URL.Query().Get("offset"); raw != "" {
			offset, err = strconv.Atoi(raw)
		}
		if err != nil || offset < 0 || offset > api.MCPConnectionHealthMaxOffset {
			api.WriteProblem(w, api.ErrValidation("invalid health offset"))
			return
		}
		hidden, err := hiddenApps(r, store, account)
		if err != nil {
			respond(w, nil, err)
			return
		}
		appID := ""
		if slug := r.URL.Query().Get("app"); slug != "" {
			app, e := store.AppBySlug(r.Context(), slug)
			if e != nil || app.AccountID != account.ID || hidden[app.ID] {
				respond(w, nil, state.ErrNotFound)
				return
			}
			appID = app.ID
		}
		read, cancel := context.WithTimeout(r.Context(), api.MCPPolicyConvergenceReadTimeout)
		defer cancel()
		out, err := state.VisibleMCPConnectionHealth(read, source, account.ID, appID, hidden, offset, time.Now().UTC())
		respond(w, out, err)
	}
}
