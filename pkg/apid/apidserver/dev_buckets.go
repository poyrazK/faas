package apidserver

import (
	"errors"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/devloop"
	"github.com/onebox-faas/faas/pkg/apid/object"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
)

func (s *server) writeDevSession(w http.ResponseWriter, r *http.Request, acct state.Account, app state.App, request api.UpsertDevSessionRequest, status int) {
	postgres, err := s.ensureDevPostgres(r.Context(), acct, app, request.Postgres)
	if err != nil {
		managedPostgresProblem(w, err)
		return
	}
	if prob, err := s.object.EnsureDevBuckets(r.Context(), acct, app, request.Buckets); prob != nil || err != nil {
		if prob != nil {
			api.WriteProblem(w, prob)
		} else if errors.Is(err, state.ErrObjectNotificationInvalid) {
			api.WriteProblem(w, api.ErrValidation("invalid compute triggers, filters or destination"))
		} else {
			object.BucketProblem(w, err)
		}
		return
	}
	buckets, err := s.object.DescribeDevBuckets(r, app)
	if err != nil {
		object.BucketProblem(w, err)
		return
	}
	if err := devloop.SaveWatch(r.Context(), s.store, app.ID, request.Watch, appHostForDomain(app.Slug, s.domain)); err != nil {
		api.WriteProblem(w, api.ErrCapacity("store developer watch mode"))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, status, api.DevSessionResponse{App: s.appResponseWithContext(r.Context(), app, acct.Plan), ExpiresAt: *app.PreviewExpiresAt, Postgres: postgres, Buckets: buckets, Watch: request.Watch})
}
