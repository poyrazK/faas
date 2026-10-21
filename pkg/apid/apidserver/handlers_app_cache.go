package apidserver

import (
	"context"
	"encoding/json"
	"net/http"
	"path"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

// purgeAppCache records a durable response-cache purge for every gateway. The
// database notification is the low-latency path; each gateway's replay cursor
// repairs missed notifications and reports actual invalidation in policy status.
func (s *server) purgeAppCache(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	query := r.URL.Query()
	pathGlob := query.Get("path")
	tagValues, hasTag := query["tag"]
	_, hasPath := query["path"]
	if hasPath && hasTag {
		api.WriteProblem(w, api.ErrValidation("cache purge accepts either path or tag, not both"))
		return
	}
	tag := ""
	if hasTag {
		if len(tagValues) != 1 {
			api.WriteProblem(w, api.ErrValidation("cache purge accepts one tag"))
			return
		}
		var err error
		tag, err = api.NormalizeCacheTag(tagValues[0])
		if err != nil {
			api.WriteProblem(w, api.ErrValidation(err.Error()))
			return
		}
	}
	// The glob is customer-supplied and lands in a pg_notify payload, which
	// PostgreSQL caps at 8000 bytes. path.Match validates syntax but accepts
	// an arbitrarily long pattern, so without a length bound an oversized
	// glob reached the database and came back as a 503 — a server-error
	// shape for what is plainly a bad request.
	if len(pathGlob) > api.CachePurgeGlobMaxBytes {
		api.WriteProblem(w, api.ErrValidation("cache path glob is too long"))
		return
	}
	if pathGlob != "" && pathGlob != "*" {
		if _, err := path.Match(pathGlob, "/"); err != nil {
			api.WriteProblem(w, api.ErrValidation("invalid cache path glob"))
			return
		}
	}
	if prob := s.requestCachePurge(r.Context(), app, pathGlob, tag); prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// requestCachePurge records a validated purge of an app's cached responses;
// route-insight fixes share it (ADR-20261020161310).
func (s *server) requestCachePurge(ctx context.Context, app state.App, pathGlob, tag string) *api.Problem {
	if purger, ok := s.store.(interface {
		CreateResponseCachePurge(context.Context, string, string, string) (int64, error)
	}); ok {
		if _, err := purger.CreateResponseCachePurge(ctx, app.ID, pathGlob, tag); err != nil {
			return api.ErrCapacity("could not request cache purge")
		}
		return nil
	}
	payload, err := json.Marshal(struct {
		AppID    string `json:"app_id"`
		PathGlob string `json:"path_glob"`
		Tag      string `json:"tag,omitempty"`
	}{AppID: app.ID, PathGlob: pathGlob, Tag: tag})
	if err != nil {
		return api.ErrInternal("could not encode cache purge request")
	}
	if s.notif == nil {
		return api.ErrCapacity("cache purge notifications are unavailable")
	}
	if err := s.notif.Notify(ctx, db.NotifyCachePurge, string(payload)); err != nil {
		return api.ErrCapacity("could not request cache purge")
	}
	return nil
}
