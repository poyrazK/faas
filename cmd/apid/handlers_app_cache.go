package main

import (
	"encoding/json"
	"net/http"
	"path"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

// purgeAppCache requests an in-process response-cache purge on every gateway.
// The purge is intentionally notification-backed: apid does not own gateway
// memory and a successful response means the request was accepted, not that
// every edge process has already consumed it.
func (s *server) purgeAppCache(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	pathGlob := r.URL.Query().Get("path")
	if pathGlob != "" && pathGlob != "*" {
		if _, err := path.Match(pathGlob, "/"); err != nil {
			api.WriteProblem(w, api.ErrValidation("invalid cache path glob"))
			return
		}
	}
	payload, err := json.Marshal(struct {
		AppID    string `json:"app_id"`
		PathGlob string `json:"path_glob"`
	}{AppID: app.ID, PathGlob: pathGlob})
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not encode cache purge request"))
		return
	}
	if s.notif == nil {
		api.WriteProblem(w, api.ErrCapacity("cache purge notifications are unavailable"))
		return
	}
	if err := s.notif.Notify(r.Context(), db.NotifyCachePurge, string(payload)); err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not request cache purge"))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
