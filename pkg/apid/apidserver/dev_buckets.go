package apidserver

import (
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/devloop"
	"github.com/onebox-faas/faas/pkg/apid/managedpg"
	"github.com/onebox-faas/faas/pkg/apid/object"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) writeDevSession(w http.ResponseWriter, r *http.Request, acct state.Account, app state.App, request api.UpsertDevSessionRequest, status int) {
	postgres, err := s.ensureDevPostgres(r.Context(), acct, app, request.Postgres)
	if err != nil {
		var conflict devPostgresSecretConflict
		if errors.As(err, &conflict) {
			api.WriteProblem(w, api.ErrDevPostgresDatabaseURLSecret(app.Slug, conflict.key))
			return
		}
		if errors.Is(err, managedpostgres.ErrQuotaExceeded) && !errors.Is(err, managedpostgres.ErrNotInPlan) {
			writeDevPostgresQuotaProblem(w, err)
			return
		}
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
	app, err = devloop.SaveGoCommand(r.Context(), s.store, app, request.GoCommand)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("store developer go command"))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, status, api.DevSessionResponse{App: s.appResponseWithContext(r.Context(), app, acct.Plan), ExpiresAt: *app.PreviewExpiresAt, Postgres: postgres, Buckets: buckets, Watch: request.Watch,
		GoCommand: app.Manifest.BuildGoCommand, LivePatches: devLivePatches()})
}

// writeDevPostgresQuotaProblem explains that developer databases share the
// plan's managed PostgreSQL database limit with every other database
// (owner decision 2026-11-15), and what frees a place.
func writeDevPostgresQuotaProblem(w http.ResponseWriter, err error) {
	problem := managedpg.LimitProblem(err)
	if problem == nil {
		status, code, title := managedpostgres.QuotaProblem(err)
		problem = api.NewProblem(status, code, title, "")
	}
	problem.Detail = "Developer databases from gregale dev --postgres count toward your plan's managed PostgreSQL database limit, together with your other databases. " +
		"Delete a database you no longer need (gregale postgres list), stop another developer environment that has one (listed on the Developers page of the dashboard), or run gregale dev without --postgres."
	api.WriteProblem(w, problem)
}

// devLivePatches reports patch delivery to the CLI, which picks watch mode
// automatically only when edits can reach the development server.
func devLivePatches() *bool {
	enabled := devPatchDeliveryEnabled()
	return &enabled
}
