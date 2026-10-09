package main

// Production fork intent handlers (ADR-732).
//
//   POST   /v1/apps/{slug}/forks       create (deploy:write AND secrets:read, MFA)
//   GET    /v1/apps/{slug}/forks       list, newest first
//   GET    /v1/apps/{slug}/forks/{id}  read one
//   DELETE /v1/apps/{slug}/forks/{id}  cancel (idempotent)
//
// A fork copies production memory, secrets included, so creating one is
// treated as reading secrets: it needs the secrets:read scope as well as
// deploy:write, and every create and cancel is audited. Every route answers
// 501 app_forks_not_enabled until the operator sets FAAS_APP_FORKS=1, which
// stays off until schedd can restore forks.

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) requireAppForks(w http.ResponseWriter) bool {
	if s.appForksEnabled {
		return true
	}
	api.WriteProblem(w, api.ErrAppForksNotEnabled())
	return false
}

func (s *server) createAppFork(w http.ResponseWriter, r *http.Request, acct state.Account) {
	s.createAppForkFrom(w, r, acct, "")
}

// createAppForkFrom admits a fork of the app's live deployment, or of the
// ADR-733 crash capture captureID when it is set.
func (s *server) createAppForkFrom(w http.ResponseWriter, r *http.Request, acct state.Account, captureID string) {
	if !s.requireAppForks(w) {
		return
	}
	perApp, perAccount := acct.Plan.AppForkLimits()
	if perApp < 1 || perAccount < 1 {
		api.WriteProblem(w, api.ErrPlanAppForksNotAllowed(acct.Plan))
		return
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug")) //nolint:contextcheck // loadApp uses r.Context() for its own DB calls; helper is shared across every per-app handler.
	if !ok {
		return
	}
	ttl, target, problem := s.decodeAppForkRequest(r, captureID)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	token, tokenHash, err := api.NewAppForkAccessToken()
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not mint the fork access token"))
		return
	}
	fork, problem := s.admitAppFork(r, acct, app, target, ttl, perApp, perAccount, tokenHash)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	s.audit.Emit(r.Context(), "app.fork_created", &acct.ID, map[string]any{
		"app_id": app.ID, "fork_id": fork.ID, "deployment_id": fork.DeploymentID,
		"ttl_seconds": fork.TTLSeconds, "requested_by": fork.RequestedBy, "crash_capture_id": stringOrEmpty(fork.CrashCaptureID), "live": target.live,
	})
	resp := appForkResponse(fork)
	// The token is shown once; only its hash is stored.
	resp.AccessToken = token
	writeJSON(w, http.StatusAccepted, resp)
}

// decodeAppForkRequest reads the TTL and what the fork restores: captureID
// for a crash snapshot fork, or a capture taken now when the body asks for
// a live fork.
func (s *server) decodeAppForkRequest(r *http.Request, captureID string) (int, appForkTarget, *api.Problem) {
	var req api.CreateAppForkRequest
	if err := decodeJSONSized(r, &req, api.AppForkRequestMaxBytes); err != nil {
		return 0, appForkTarget{}, api.ErrValidation("invalid fork request body")
	}
	ttl, problem := req.ResolveTTL()
	if problem != nil {
		return 0, appForkTarget{}, problem
	}
	target := appForkTarget{captureID: captureID, live: req.IsLive()}
	switch {
	case target.live && captureID != "":
		return 0, appForkTarget{}, api.ErrValidation("a crash snapshot fork cannot also be live")
	case target.live && !s.crashSnapshotsEnabled:
		return 0, appForkTarget{}, api.ErrLiveForksNotEnabled()
	}
	return ttl, target, nil
}

// appForkTarget is what a fork restores: the live deployment's snapshot
// (zero value), a crash capture, or a capture taken now (live).
type appForkTarget struct {
	captureID string
	live      bool
}

// admitAppFork pins what the fork restores and records the intent under the
// plan's active-fork caps.
func (s *server) admitAppFork(r *http.Request, acct state.Account, app state.App, target appForkTarget, ttl, perApp, perAccount int, tokenHash []byte) (state.AppFork, *api.Problem) {
	unavailable := api.ErrAppForkUnavailable()
	deploymentID, captureID := "", target.captureID
	switch {
	case target.live:
		unavailable = api.ErrLiveForkRefused()
	case captureID != "":
		unavailable = api.ErrCrashCaptureNotReady()
	default:
		deployment, err := s.store.LiveDeployment(r.Context(), app.ID)
		if errors.Is(err, state.ErrNotFound) {
			return state.AppFork{}, unavailable
		}
		if err != nil {
			return state.AppFork{}, api.ErrInternal("could not select the deployment to fork")
		}
		deploymentID = deployment.ID
	}
	fork, err := s.store.CreateAppFork(r.Context(), state.CreateAppForkParams{
		AccountID: acct.ID, AppID: app.ID, DeploymentID: deploymentID, CrashCaptureID: captureID,
		RequestedBy: appForkActor(r, acct), TTLSeconds: ttl,
		MaxPerApp: perApp, MaxPerAccount: perAccount, CreatedAt: time.Now().UTC(),
		AccessTokenHash: tokenHash, Live: target.live, LiveCaptureCooldown: api.LiveForkCaptureCooldown,
	})
	var limitErr *state.AppForkLimitError
	switch {
	case errors.As(err, &limitErr):
		return state.AppFork{}, api.ErrAppForkLimit(limitErr.Scope, limitErr.Limit, limitErr.Observed)
	case errors.Is(err, state.ErrAppForkDeploymentUnavailable), errors.Is(err, state.ErrAppForkLiveCaptureRefused):
		return state.AppFork{}, unavailable
	case err != nil:
		return state.AppFork{}, api.ErrInternal("could not record the fork")
	}
	return fork, nil
}

func (s *server) listAppForks(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.requireAppForks(w) {
		return
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug")) //nolint:contextcheck // shared per-app loader.
	if !ok {
		return
	}
	limit := 0
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 100 {
			api.WriteProblem(w, api.ErrValidation("limit must be between 1 and 100"))
			return
		}
		limit = parsed
	}
	forks, err := s.store.ListAppForks(r.Context(), acct.ID, app.ID, limit)
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not list forks"))
		return
	}
	out := api.AppForkListResponse{Items: make([]api.AppForkResponse, 0, len(forks))}
	for _, fork := range forks {
		out.Items = append(out.Items, appForkResponse(fork))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) getAppFork(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, forkID, ok := s.loadAppForkPath(w, r, acct)
	if !ok {
		return
	}
	fork, err := s.store.AppForkByID(r.Context(), acct.ID, app.ID, forkID)
	if !s.writeAppForkLookupError(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, appForkResponse(fork))
}

func (s *server) cancelAppFork(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, forkID, ok := s.loadAppForkPath(w, r, acct)
	if !ok {
		return
	}
	fork, err := s.store.RequestAppForkCancellation(r.Context(), acct.ID, app.ID, forkID, time.Now().UTC())
	if !s.writeAppForkLookupError(w, err) {
		return
	}
	s.audit.Emit(r.Context(), "app.fork_cancelled", &acct.ID, map[string]any{
		"app_id": app.ID, "fork_id": fork.ID, "status": string(fork.Status),
		"requested_by": appForkActor(r, acct),
	})
	writeJSON(w, http.StatusAccepted, appForkResponse(fork))
}

// loadAppForkPath runs the gate, loads the app, and parses {id}. A
// malformed id is a 404, byte-identical to an unknown one.
func (s *server) loadAppForkPath(w http.ResponseWriter, r *http.Request, acct state.Account) (state.App, string, bool) {
	if !s.requireAppForks(w) {
		return state.App{}, "", false
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug")) //nolint:contextcheck // shared per-app loader.
	if !ok {
		return state.App{}, "", false
	}
	forkID := r.PathValue("id")
	if _, err := uuid.Parse(forkID); err != nil {
		s.notFound(w, "no such fork")
		return state.App{}, "", false
	}
	return app, forkID, true
}

// writeAppForkLookupError writes the problem for a failed fork read and
// reports whether the caller may continue.
func (s *server) writeAppForkLookupError(w http.ResponseWriter, err error) bool {
	switch {
	case err == nil:
		return true
	case errors.Is(err, state.ErrNotFound):
		s.notFound(w, "no such fork")
	default:
		api.WriteProblem(w, api.ErrInternal("could not load the fork"))
	}
	return false
}

func appForkActor(r *http.Request, acct state.Account) string {
	principal, key, membership, _ := authmw.PrincipalFrom(r)
	switch {
	case key != nil:
		return "api_key:" + key.ID
	case membership != nil:
		return "account:" + membership.AccountID
	case principal.ID != "":
		return "account:" + principal.ID
	}
	return "account:" + acct.ID
}

func appForkResponse(fork state.AppFork) api.AppForkResponse {
	resp := api.AppForkResponse{
		ID: fork.ID, AppID: fork.AppID, DeploymentID: fork.DeploymentID, CrashCaptureID: fork.CrashCaptureID,
		Status: api.AppForkStatus(fork.Status), TTLSeconds: fork.TTLSeconds,
		ExpiresAt:         fork.ExpiresAt.UTC().Format(time.RFC3339Nano),
		CancelRequestedAt: appTaskTimeResponse(fork.CancelRequested),
		StartedAt:         appTaskTimeResponse(fork.StartedAt),
		FinishedAt:        appTaskTimeResponse(fork.FinishedAt),
		CreatedAt:         fork.CreatedAt.UTC().Format(time.RFC3339Nano),
		UpdatedAt:         fork.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}
	if fork.FailureCode != nil && fork.FailureMessage != nil {
		resp.Failure = &api.AppForkFailure{Code: *fork.FailureCode, Message: *fork.FailureMessage}
	}
	return resp
}
